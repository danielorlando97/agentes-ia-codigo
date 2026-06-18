// Demostración del pipeline de sampling: temperature, top-p y min-p.
//
// Muestra:
//  1. Efecto de temperature 0.0 / 0.5 / 1.0 en varianza de output
//  2. Diversidad léxica (TTR) como métrica de varianza
//  3. Tasa de JSON malformado en tool calling con temperature alta (1.5)
//  4. Tabla resumen comparativa
//
// Sin SDK: HTTP directo contra la API de Anthropic.

// Cómo ejecutar: make go FILE=go/03-motor-llm/sampling.go

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
	"unicode"
)

const (
	promptCreativo = "En dos oraciones, explica por qué el cielo es azul. Sé creativo y variado en tu respuesta."
	promptTool     = "Crea una tarea para revisar el informe de ventas del Q3. Prioridad alta, estimación 2.5 horas."
)

var (
	mainModel = envOr("MODEL", "claude-sonnet-4-6")
	smallModel = envOr("SMALL_MODEL", "claude-haiku-4-5-20251001")
	apiEndpoint = envBaseURL()
)

// ─── Estructuras de la API ─────────────────────────────────────────────────

type apiMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type apiBlock struct {
	Type  string          `json:"type"`
	Text  string          `json:"text,omitempty"`
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
}

type apiResponse struct {
	Content []apiBlock `json:"content"`
	Usage   struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

var toolSchema = []map[string]any{
	{
		"name":        "crear_tarea",
		"description": "Crea una tarea en el gestor de proyectos.",
		"input_schema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"titulo":    map[string]string{"type": "string", "description": "Título corto de la tarea"},
				"prioridad": map[string]any{"type": "string", "enum": []string{"alta", "media", "baja"}},
				"estimacion_horas": map[string]string{"type": "number", "description": "Estimación en horas"},
			},
			"required": []string{"titulo", "prioridad", "estimacion_horas"},
		},
	},
}

// ─── Cliente HTTP ──────────────────────────────────────────────────────────

func callAPI(payload map[string]any) (*apiResponse, error) {
	body, _ := json.Marshal(payload)
	req, _ := http.NewRequestWithContext(context.Background(), "POST", apiEndpoint, bytes.NewReader(body))
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
	if r.Error != nil {
		return nil, fmt.Errorf("API error: %s", r.Error.Message)
	}
	return &r, nil
}

func extractText(resp *apiResponse) string {
	var sb strings.Builder
	for _, b := range resp.Content {
		if b.Type == "text" {
			sb.WriteString(b.Text)
		}
	}
	return sb.String()
}

// ─── 1. Métricas ───────────────────────────────────────────────────────────

// diversidadLexica calcula el Type-Token Ratio (TTR).
func diversidadLexica(texto string) float64 {
	re := regexp.MustCompile(`\b\w+\b`)
	palabras := re.FindAllString(strings.ToLower(texto), -1)
	if len(palabras) == 0 {
		return 0
	}
	unicos := make(map[string]struct{})
	for _, p := range palabras {
		unicos[p] = struct{}{}
	}
	return float64(len(unicos)) / float64(len(palabras))
}

func contarPalabras(texto string) int {
	return len(strings.Fields(texto))
}

// ─── 2. Varianza de output por temperature ─────────────────────────────────

func medirVarianzaTemperature(temperatures []float64, repeticiones int) {
	fmt.Println("\n[varianza de output por temperature]")
	fmt.Printf("  Prompt: '%s...'\n", promptCreativo[:60])
	fmt.Printf("  Repeticiones por temperatura: %d\n\n", repeticiones)

	for _, temp := range temperatures {
		var longitudes []int
		var ttrs []float64
		var outputs []string

		for rep := 0; rep < repeticiones; rep++ {
			payload := map[string]any{
				"model":      smallModel,
				"max_tokens": 120,
				"messages": []apiMessage{
					{Role: "user", Content: promptCreativo},
				},
			}
			if temp > 0 {
				payload["temperature"] = temp
			}

			resp, err := callAPI(payload)
			if err != nil {
				fmt.Printf("  T=%.1f rep%d: error — %v\n", temp, rep+1, err)
				continue
			}

			texto := extractText(resp)
			longitudes = append(longitudes, contarPalabras(texto))
			ttrs = append(ttrs, diversidadLexica(texto))
			outputs = append(outputs, texto)
		}

		if len(longitudes) == 0 {
			continue
		}

		var sumLen, sumTTR float64
		maxLen, minLen := longitudes[0], longitudes[0]
		for i, l := range longitudes {
			sumLen += float64(l)
			sumTTR += ttrs[i]
			if l > maxLen {
				maxLen = l
			}
			if l < minLen {
				minLen = l
			}
		}
		avgLen := sumLen / float64(len(longitudes))
		avgTTR := sumTTR / float64(len(ttrs))
		rangoLen := maxLen - minLen

		fmt.Printf("  T=%.1f  avg_palabras=%5.1f  rango_len=%3d  TTR=%.3f\n",
			temp, avgLen, rangoLen, avgTTR)
		for i, out := range outputs {
			truncated := out
			if len(truncated) > 90 {
				truncated = truncated[:90]
			}
			fmt.Printf("         rep%d: %q\n", i+1, truncated)
		}
		fmt.Println()
	}
}

// ─── 3. Tasa de JSON malformado en tool calling ────────────────────────────

func medirTasaJsonMalformado(temperatures []float64, intentos int) {
	fmt.Println("\n[tasa de JSON malformado en tool calling]")
	fmt.Printf("  Intentos por temperatura: %d\n\n", intentos)

	validPrioridades := map[string]bool{"alta": true, "media": true, "baja": true}

	for _, temp := range temperatures {
		fallos := 0
		var errores []string

		for i := 0; i < intentos; i++ {
			payload := map[string]any{
				"model":      smallModel,
				"max_tokens": 256,
				"tools":      toolSchema,
				"messages": []apiMessage{
					{Role: "user", Content: promptTool},
				},
			}
			if temp > 0 {
				payload["temperature"] = temp
			}

			resp, err := callAPI(payload)
			if err != nil {
				fallos++
				errores = append(errores, fmt.Sprintf("API error: %v", err))
				continue
			}

			// Buscar tool_use en el response
			var toolInput map[string]any
			found := false
			for _, b := range resp.Content {
				if b.Type == "tool_use" {
					if err := json.Unmarshal(b.Input, &toolInput); err != nil {
						fallos++
						errores = append(errores, fmt.Sprintf("JSON parse error: %v", err))
						found = true
						break
					}
					found = true
					break
				}
			}

			if !found {
				fallos++
				errores = append(errores, "sin tool_use en respuesta")
				continue
			}

			if toolInput != nil {
				required := []string{"titulo", "prioridad", "estimacion_horas"}
				var missing []string
				for _, k := range required {
					if _, ok := toolInput[k]; !ok {
						missing = append(missing, k)
					}
				}
				if len(missing) > 0 {
					fallos++
					errores = append(errores, fmt.Sprintf("campos faltantes: %v", missing))
				} else if prio, ok := toolInput["prioridad"].(string); ok {
					if !validPrioridades[prio] {
						fallos++
						errores = append(errores, fmt.Sprintf("prioridad inválida: %q", prio))
					}
				}
			}
		}

		tasa := float64(fallos) / float64(intentos)
		fmt.Printf("  T=%.1f  fallos=%d/%d  tasa_error=%.0f%%\n",
			temp, fallos, intentos, tasa*100)
		for _, e := range errores {
			fmt.Printf("         ✗ %s\n", e)
		}
	}
	fmt.Println()
}

// ─── 4. Tabla resumen ──────────────────────────────────────────────────────

func tablaResumen() {
	fmt.Println("\n[tabla resumen: temperatura vs uso recomendado]")
	type fila struct {
		temp, dist, div, coh, uso string
	}
	filas := []fila{
		{"0.0", "Greedy",      "Mínima",  "Máxima local", "Tool calling, JSON, extracción estructurada"},
		{"0.5", "Concentrada", "Baja",    "Alta",          "Q&A factual, código, análisis"},
		{"1.0", "Original",    "Media",   "Buena",         "Chatbot conversacional, texto general"},
		{"1.5", "Plana",       "Alta",    "Menor",         "Escritura creativa (usar min-p=0.05)"},
	}
	header := fmt.Sprintf("  %4s  %-15s  %-12s  %-12s  Uso", "T", "Distribución", "Diversidad", "Coherencia")
	sep := "  " + strings.Repeat("-", len([]rune(header))-2)
	fmt.Println(header)
	fmt.Println(sep)
	for _, f := range filas {
		fmt.Printf("  %4s  %-15s  %-12s  %-12s  %s\n", f.temp, f.dist, f.div, f.coh, f.uso)
	}
}

// ─── Utilidad para suprimir warning de unicode ────────────────────────────

var _ = unicode.IsLetter // mantener import si se necesita

func main() {
	fmt.Println("=== Sampling: temperatura, diversidad y fiabilidad ===")
	medirVarianzaTemperature([]float64{0.0, 0.5, 1.0}, 3)
	medirTasaJsonMalformado([]float64{0.0, 0.5, 1.0, 1.5}, 5)
	tablaResumen()
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
