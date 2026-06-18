// Comparación 0-shot vs 3-shot vs 6-shot en clasificación de sentimiento.
//
// Demuestra:
// - Cómo el número de ejemplos afecta accuracy y consistencia del formato
// - Majority label bias: con ejemplos desbalanceados, el modelo predice la clase dominante
// - Tokens consumidos por cada variante
// Sin SDK: HTTP directo contra la API de Anthropic.

// Cómo ejecutar: make go FILE=go/04-prompts/few-shot.go

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
)

const (
	maxTokens  = 20
)

var (
	model = envOr("MODEL", "claude-sonnet-4-6")
	apiURL = envBaseURL()
)

// ─── 1. Tipos ─────────────────────────────────────────────────────────────────

type review struct {
	Text  string
	Label string // "positivo" | "negativo" | "neutro"
}

type apiRequest struct {
	Model     string      `json:"model"`
	MaxTokens int         `json:"max_tokens"`
	System    string      `json:"system"`
	Messages  []message   `json:"messages"`
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type apiResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Usage struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

type classResult struct {
	Text          string
	LabelReal     string
	Prediccion    string
	Correcto      bool
	FormatoValido bool
	TokensInput   int
	TokensOutput  int
}

type metrics struct {
	Accuracy         float64
	FormatConsistency float64
	AvgInputTokens   float64
	AvgOutputTokens  float64
	TotalInputTokens int
}

// ─── 2. Dataset y ejemplos ───────────────────────────────────────────────────

var reviews = []review{
	{"El producto llegó perfectamente empaquetado y funciona exactamente como se describe. Muy satisfecho.", "positivo"},
	{"Terrible experiencia. El artículo llegó roto y el servicio de atención al cliente no respondió.", "negativo"},
	{"El producto cumple lo básico. No es el mejor que he usado pero tampoco es malo. Precio razonable.", "neutro"},
	{"¡Increíble calidad! Superó todas mis expectativas. Lo recomendaría sin dudar.", "positivo"},
	{"Llegó tarde y el embalaje estaba dañado. El producto funciona pero la experiencia de compra fue mala.", "negativo"},
}

var examples3Shot = []review{
	{"Excelente producto, muy buena calidad y entrega rápida.", "positivo"},
	{"No me gustó nada. Tuve que devolverlo al día siguiente.", "negativo"},
	{"Hace lo que promete. Ni más ni menos.", "neutro"},
}

var examples6Shot = append(examples3Shot,
	review{"Mejor compra del año, totalmente recomendado para todos.", "positivo"},
	review{"Producto defectuoso. Una pérdida de dinero total.", "negativo"},
	review{"Está bien para lo que cuesta. No hay mucho que decir.", "neutro"},
)

// ─── 3. Construcción de prompts ──────────────────────────────────────────────

func buildExamplesBlock(examples []review) string {
	var sb strings.Builder
	sb.WriteString("<examples>\n")
	for _, ex := range examples {
		sb.WriteString("  <example>\n")
		fmt.Fprintf(&sb, "    <texto>%s</texto>\n", ex.Text)
		fmt.Fprintf(&sb, "    <sentimiento>%s</sentimiento>\n", ex.Label)
		sb.WriteString("  </example>\n")
	}
	sb.WriteString("</examples>")
	return sb.String()
}

func buildSystemPrompt(examples []review) string {
	base := "Clasifica el sentimiento de reseñas de producto como: positivo, negativo o neutro.\n" +
		"Responde SOLO con una de estas tres palabras: positivo, negativo, neutro.\n" +
		"Sin explicaciones adicionales.\n"
	if len(examples) == 0 {
		return base
	}
	return base + "\n" + buildExamplesBlock(examples)
}

// ─── 4. Llamada a la API ──────────────────────────────────────────────────────

func callAPI(system, userContent string) (*apiResponse, error) {
	payload := apiRequest{
		Model:     model,
		MaxTokens: maxTokens,
		System:    system,
		Messages:  []message{{Role: "user", Content: userContent}},
	}

	body, _ := json.Marshal(payload)
	req, _ := http.NewRequestWithContext(context.Background(), "POST", apiURL, bytes.NewReader(body))
	req.Header.Set("x-api-key", os.Getenv("ANTHROPIC_API_KEY"))
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("content-type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)
	var r apiResponse
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("parse %s: %w", string(data), err)
	}
	return &r, nil
}

// ─── 5. Clasificación ────────────────────────────────────────────────────────

var labelPattern = regexp.MustCompile(`\b(positivo|negativo|neutro)\b`)
var validLabels = map[string]bool{"positivo": true, "negativo": true, "neutro": true}

func classifyReviews(systemPrompt string, reviews []review) ([]classResult, error) {
	var results []classResult
	for _, rev := range reviews {
		resp, err := callAPI(systemPrompt, rev.Text)
		if err != nil {
			return nil, err
		}
		rawOutput := ""
		if len(resp.Content) > 0 {
			rawOutput = strings.ToLower(strings.TrimSpace(resp.Content[0].Text))
		}
		predicted := rawOutput
		if m := labelPattern.FindString(rawOutput); m != "" {
			predicted = m
		}
		text := rev.Text
		if len(text) > 50 {
			text = text[:50] + "..."
		}
		results = append(results, classResult{
			Text:          text,
			LabelReal:     rev.Label,
			Prediccion:    predicted,
			Correcto:      predicted == rev.Label,
			FormatoValido: validLabels[predicted],
			TokensInput:   resp.Usage.InputTokens,
			TokensOutput:  resp.Usage.OutputTokens,
		})
	}
	return results, nil
}

// ─── 6. Métricas ─────────────────────────────────────────────────────────────

func computeMetrics(results []classResult) metrics {
	n := float64(len(results))
	var correct, valid, totalIn, totalOut int
	for _, r := range results {
		if r.Correcto {
			correct++
		}
		if r.FormatoValido {
			valid++
		}
		totalIn += r.TokensInput
		totalOut += r.TokensOutput
	}
	return metrics{
		Accuracy:          float64(correct) / n,
		FormatConsistency: float64(valid) / n,
		AvgInputTokens:    float64(totalIn) / n,
		AvgOutputTokens:   float64(totalOut) / n,
		TotalInputTokens:  totalIn,
	}
}

// ─── 7. Impresión ────────────────────────────────────────────────────────────

func printResultsTable(variantName string, results []classResult, m metrics) {
	fmt.Printf("\n%s\n", strings.Repeat("═", 70))
	fmt.Printf("  %s\n", variantName)
	fmt.Printf("%s\n", strings.Repeat("═", 70))
	fmt.Printf("  %-40s %-12s %-12s %s\n", "Reseña (extracto)", "Real", "Predicción", "OK")
	fmt.Printf("  %s\n", strings.Repeat("-", 66))
	for _, r := range results {
		ok := "✓"
		if !r.Correcto {
			ok = "✗"
		}
		fmt.Printf("  %-40s %-12s %-12s %s\n", r.Text, r.LabelReal, r.Prediccion, ok)
	}
	fmt.Printf("\n  Accuracy:             %.0f%%\n", m.Accuracy*100)
	fmt.Printf("  Consistencia formato: %.0f%%\n", m.FormatConsistency*100)
	fmt.Printf("  Tokens input (prom):  %.0f\n", m.AvgInputTokens)
	fmt.Printf("  Tokens output (prom): %.1f\n", m.AvgOutputTokens)
	fmt.Printf("  Tokens input total:   %d\n", m.TotalInputTokens)
}

func printComparisonTable(allMetrics map[string]metrics, order []string) {
	fmt.Printf("\n%s\n", strings.Repeat("═", 70))
	fmt.Println("  TABLA COMPARATIVA")
	fmt.Printf("%s\n", strings.Repeat("═", 70))
	fmt.Printf("  %-20s %10s %10s %12s %14s\n", "Variante", "Accuracy", "Formato", "Tokens/call", "Total tokens")
	fmt.Printf("  %s\n", strings.Repeat("-", 68))
	for _, name := range order {
		m := allMetrics[name]
		fmt.Printf("  %-20s %9.0f%% %9.0f%% %11.0f %13d\n",
			name, m.Accuracy*100, m.FormatConsistency*100, m.AvgInputTokens, m.TotalInputTokens)
	}
	fmt.Println("\n  Nota: 'Tokens/call' = promedio de tokens de input por clasificación")
}

// ─── 8. Main ──────────────────────────────────────────────────────────────────

func main() {
	type variant struct {
		name     string
		examples []review
	}
	variants := []variant{
		{"0-shot (sin ejemplos)", nil},
		{"3-shot (3 ejemplos)", examples3Shot},
		{"6-shot (6 ejemplos)", examples6Shot},
	}

	allMetrics := make(map[string]metrics)
	order := make([]string, 0, len(variants))

	for _, v := range variants {
		system := buildSystemPrompt(v.examples)
		results, err := classifyReviews(system, reviews)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error en variante %s: %v\n", v.name, err)
			os.Exit(1)
		}
		m := computeMetrics(results)
		printResultsTable(v.name, results, m)
		allMetrics[v.name] = m
		order = append(order, v.name)
	}

	printComparisonTable(allMetrics, order)

	base := allMetrics["0-shot (sin ejemplos)"].AvgInputTokens
	t3 := allMetrics["3-shot (3 ejemplos)"].AvgInputTokens
	t6 := allMetrics["6-shot (6 ejemplos)"].AvgInputTokens
	fmt.Printf("\n  Overhead de tokens por ejemplos:\n")
	fmt.Printf("    3-shot vs 0-shot: +%.0f tokens/llamada\n", t3-base)
	fmt.Printf("    6-shot vs 0-shot: +%.0f tokens/llamada\n", t6-base)
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envBaseURL() string {
	if v := os.Getenv("ANTHROPIC_BASE_URL"); v != "" {
		return v + "/v1/messages"
	}
	return "https://api.anthropic.com/v1/messages"
}
