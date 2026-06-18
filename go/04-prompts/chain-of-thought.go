// Comparación directa vs CoT explícito vs zero-shot CoT en problema aritmético multi-paso.
//
// Demuestra:
// - Variante 1: prompt directo — el modelo responde sin razonar
// - Variante 2: CoT explícito — el prompt describe los pasos intermedios a seguir
// - Variante 3: zero-shot CoT — trigger phrase "piensa paso a paso"
// - Métricas: accuracy, tokens de output (proxy de razonamiento), latencia
// Sin SDK: HTTP directo contra la API de Anthropic.

// Cómo ejecutar: make go FILE=go/04-prompts/chain-of-thought.go

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

var (
	cotModel = envOr("MODEL", "claude-sonnet-4-6")
	cotAPIURL = envBaseURL()
)

// ─── 1. Tipos ─────────────────────────────────────────────────────────────────

type problem struct {
	Question    string
	AnswerExact string
	Explanation string
}

type cotAPIRequest struct {
	Model     string       `json:"model"`
	MaxTokens int          `json:"max_tokens"`
	System    string       `json:"system"`
	Messages  []cotMessage `json:"messages"`
}

type cotMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type cotAPIResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Usage struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

type solveResult struct {
	Variant      string
	Output       string
	TokensInput  int
	TokensOutput int
	LatencyMs    float64
}

// ─── 2. Problemas ────────────────────────────────────────────────────────────

var problems = []problem{
	{
		Question: "Una tienda vende manzanas a 3 por €1. " +
			"Juan compra 12 manzanas y paga con un billete de €10. " +
			"¿Cuánto cambio recibe? " +
			"Nota: la tienda tiene una oferta especial hoy: si compras más de 10 manzanas, " +
			"obtienes un 20% de descuento en el total.",
		AnswerExact: "6.80",
		Explanation: "12 manzanas a 3/€1 = €4.00 base. Descuento 20%: €4.00 × 0.80 = €3.20. Cambio: €10.00 − €3.20 = €6.80",
	},
	{
		Question: "Un tren parte de Madrid a las 8:00 y llega a Barcelona a las 10:30. " +
			"Otro tren parte de Barcelona a las 9:00 y llega a Madrid a las 11:30. " +
			"Los trenes viajan en sentidos opuestos por la misma vía. " +
			"¿A qué hora se cruzan si Madrid y Barcelona están a 600 km?",
		AnswerExact: "9:45",
		Explanation: "Tren A: 240 km/h. A las 9:00 lleva 240 km. Quedan 360 km. Se acercan a 480 km/h. 360/480 = 0.75 h = 45 min → 9:45.",
	},
	{
		Question: "Una pizzería vende pizzas pequeñas por €8 y grandes por €14. " +
			"Ayer vendió 15 pizzas en total y ganó €162. " +
			"¿Cuántas pizzas grandes vendió?",
		AnswerExact: "7",
		Explanation: "g + p = 15; 14g + 8p = 162 → 6g = 42 → g = 7.",
	},
}

// ─── 3. System prompts ───────────────────────────────────────────────────────

const systemDirect = "Resuelve el siguiente problema matemático. " +
	"Responde solo con el número o valor final, sin explicaciones."

const systemCoTExplicit = "Resuelve el siguiente problema matemático siguiendo estos pasos:\n" +
	"1. Identifica los datos conocidos\n" +
	"2. Escribe la ecuación o proceso necesario\n" +
	"3. Realiza el cálculo paso a paso\n" +
	"4. Verifica el resultado\n" +
	"5. Da la respuesta final claramente indicada\n" +
	"Muestra cada paso explícitamente."

const systemZeroShotCoT = "Resuelve el siguiente problema matemático. " +
	"Piensa paso a paso antes de responder. " +
	"Muestra tu razonamiento completo y da la respuesta final al final."

// ─── 4. Llamada a la API ──────────────────────────────────────────────────────

func callCotAPI(system, question string) (*cotAPIResponse, float64, error) {
	payload := cotAPIRequest{
		Model:     cotModel,
		MaxTokens: 800,
		System:    system,
		Messages:  []cotMessage{{Role: "user", Content: question}},
	}

	body, _ := json.Marshal(payload)
	req, _ := http.NewRequestWithContext(context.Background(), "POST", cotAPIURL, bytes.NewReader(body))
	req.Header.Set("x-api-key", os.Getenv("ANTHROPIC_API_KEY"))
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("content-type", "application/json")

	t0 := time.Now()
	resp, err := http.DefaultClient.Do(req)
	latency := time.Since(t0).Seconds() * 1000
	if err != nil {
		return nil, latency, err
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)
	var r cotAPIResponse
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, latency, fmt.Errorf("parse %s: %w", string(data), err)
	}
	return &r, latency, nil
}

func solveProblem(system, question, variantName string) (solveResult, error) {
	resp, latency, err := callCotAPI(system, question)
	if err != nil {
		return solveResult{}, err
	}
	output := ""
	if len(resp.Content) > 0 {
		output = strings.TrimSpace(resp.Content[0].Text)
	}
	return solveResult{
		Variant:      variantName,
		Output:       output,
		TokensInput:  resp.Usage.InputTokens,
		TokensOutput: resp.Usage.OutputTokens,
		LatencyMs:    latency,
	}, nil
}

func checkAnswer(output, answerExact string) bool {
	return strings.Contains(strings.ToLower(output), strings.ToLower(answerExact))
}

// ─── 5. Impresión de resultados ───────────────────────────────────────────────

func printProblemResults(p problem, results []solveResult) {
	fmt.Printf("\n%s\n", strings.Repeat("═", 72))
	q := p.Question
	if len(q) > 80 {
		q = q[:80] + "..."
	}
	fmt.Printf("  PROBLEMA: %s\n", q)
	fmt.Printf("  Respuesta correcta: %s\n", p.AnswerExact)
	fmt.Printf("  Lógica: %s\n", p.Explanation)
	fmt.Printf("%s\n", strings.Repeat("─", 72))

	for _, r := range results {
		correct := checkAnswer(r.Output, p.AnswerExact)
		status := "✓ CORRECTO"
		if !correct {
			status = "✗ INCORRECTO"
		}
		fmt.Printf("\n  [%s] %s\n", r.Variant, status)
		fmt.Printf("  Tokens input/output: %d / %d\n", r.TokensInput, r.TokensOutput)
		fmt.Printf("  Latencia: %.0f ms\n", r.LatencyMs)
		preview := r.Output
		if len(preview) > 200 {
			preview = preview[:200] + "..."
		}
		fmt.Printf("  Output: %s\n", preview)
	}
}

func printSummary(allResults [][]solveResult, probs []problem) {
	type stat struct {
		correct  int
		tokensOut int
		latency  float64
	}
	variantNames := make([]string, len(allResults[0]))
	for i, r := range allResults[0] {
		variantNames[i] = r.Variant
	}
	stats := make(map[string]*stat)
	for _, v := range variantNames {
		stats[v] = &stat{}
	}

	for i, probResults := range allResults {
		for _, r := range probResults {
			if checkAnswer(r.Output, probs[i].AnswerExact) {
				stats[r.Variant].correct++
			}
			stats[r.Variant].tokensOut += r.TokensOutput
			stats[r.Variant].latency += r.LatencyMs
		}
	}

	n := float64(len(probs))
	fmt.Printf("\n%s\n", strings.Repeat("═", 72))
	fmt.Println("  TABLA COMPARATIVA AGREGADA")
	fmt.Printf("%s\n", strings.Repeat("═", 72))
	fmt.Printf("  %-30s %10s %12s %12s\n", "Variante", "Accuracy", "Tokens out", "Latencia")
	fmt.Printf("  %s\n", strings.Repeat("-", 64))
	for _, v := range variantNames {
		s := stats[v]
		fmt.Printf("  %-30s %9.0f%% %11.0f %9.0f ms\n",
			v, float64(s.correct)/n*100, float64(s.tokensOut)/n, s.latency/n)
	}
	fmt.Println("\n  Nota: 'Tokens out' es proxy del razonamiento generado.")
	fmt.Println("  CoT produce más tokens porque muestra pasos intermedios.")
}

// ─── 6. Main ──────────────────────────────────────────────────────────────────

func main() {
	type variant struct {
		name   string
		system string
	}
	variants := []variant{
		{"Directo (sin CoT)", systemDirect},
		{"CoT explícito", systemCoTExplicit},
		{"Zero-shot CoT", systemZeroShotCoT},
	}

	var allResults [][]solveResult

	for _, p := range problems {
		var probResults []solveResult
		for _, v := range variants {
			r, err := solveProblem(v.system, p.Question, v.name)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			probResults = append(probResults, r)
		}
		printProblemResults(p, probResults)
		allResults = append(allResults, probResults)
	}

	printSummary(allResults, problems)
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
