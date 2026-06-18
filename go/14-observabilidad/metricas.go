// Cómo ejecutar: make go FILE=go/14-observabilidad/metricas.go
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"sort"
	"strconv"
	"time"
)

var modelMetricas = envOr("SMALL_MODEL", "claude-haiku-4-5-20251001")

const precioInputPorMtok = 0.80
const precioOutputPorMtok = 4.00

type ResultadoTarea struct {
	TaskID               string
	Completada           bool
	LatenciaMs           float64
	InputTokens          int
	OutputTokens         int
	ToolCallsExitosos    int
	ToolCallsFallidos    int
	Error                string
}

func (r ResultadoTarea) CosteUSD() float64 {
	return float64(r.InputTokens)*precioInputPorMtok/1_000_000 +
		float64(r.OutputTokens)*precioOutputPorMtok/1_000_000
}

type MetricasAgente struct {
	resultados []ResultadoTarea
}

func (m *MetricasAgente) Registrar(r ResultadoTarea) {
	m.resultados = append(m.resultados, r)
}

func (m *MetricasAgente) Resumen() map[string]float64 {
	r := m.resultados
	if len(r) == 0 {
		return map[string]float64{}
	}

	total := float64(len(r))
	completadas := 0.0
	for _, t := range r {
		if t.Completada {
			completadas++
		}
	}

	latencias := make([]float64, len(r))
	for i, t := range r {
		latencias[i] = t.LatenciaMs
	}
	sort.Float64s(latencias)
	n := len(latencias)

	costesTotal := 0.0
	for _, t := range r {
		costesTotal += t.CosteUSD()
	}

	toolOk := 0
	toolErr := 0
	for _, t := range r {
		toolOk += t.ToolCallsExitosos
		toolErr += t.ToolCallsFallidos
	}
	toolSuccessRate := 1.0
	if toolOk+toolErr > 0 {
		toolSuccessRate = float64(toolOk) / float64(toolOk+toolErr)
	}

	return map[string]float64{
		"task_completion_rate": completadas / total,
		"error_rate":           (total - completadas) / total,
		"latencia_p50_ms":      latencias[n/2],
		"latencia_p95_ms":      latencias[int(float64(n)*0.95)],
		"cost_per_task_usd":    costesTotal / total,
		"cost_total_usd":       costesTotal,
		"tool_success_rate":    toolSuccessRate,
		"total_tareas":         total,
	}
}

func (m *MetricasAgente) Alertas(umbralCompletion, umbralP95Ms float64) []string {
	s := m.Resumen()
	var problemas []string
	if v, ok := s["task_completion_rate"]; ok && v < umbralCompletion {
		problemas = append(problemas,
			fmt.Sprintf("task_completion_rate %.1f%% < %.0f%%", v*100, umbralCompletion*100))
	}
	if v, ok := s["latencia_p95_ms"]; ok && v > umbralP95Ms {
		problemas = append(problemas,
			fmt.Sprintf("P95 latencia %.0fms > %.0fms", v, umbralP95Ms))
	}
	return problemas
}

type MensajeMetricas struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"`
}

type ToolMetricas struct {
	Name        string             `json:"name"`
	Description string             `json:"description"`
	InputSchema InputSchemaMetricas `json:"input_schema"`
}

type InputSchemaMetricas struct {
	Type       string                        `json:"type"`
	Properties map[string]PropertyMetricas   `json:"properties"`
	Required   []string                      `json:"required"`
}

type PropertyMetricas struct {
	Type string `json:"type"`
}

type APIRequestMetricas struct {
	Model     string            `json:"model"`
	MaxTokens int               `json:"max_tokens"`
	Tools     []ToolMetricas    `json:"tools,omitempty"`
	Messages  []MensajeMetricas `json:"messages"`
}

type ContentBlockMetricas struct {
	Type  string                 `json:"type"`
	Text  string                 `json:"text,omitempty"`
	ID    string                 `json:"id,omitempty"`
	Name  string                 `json:"name,omitempty"`
	Input map[string]interface{} `json:"input,omitempty"`
}

type UsageMetricas struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type APIResponseMetricas struct {
	Content    []ContentBlockMetricas `json:"content"`
	StopReason string                 `json:"stop_reason"`
	Usage      UsageMetricas          `json:"usage"`
}

var toolsMetricas = []ToolMetricas{
	{
		Name:        "calcular",
		Description: "Evalúa una expresión matemática simple.",
		InputSchema: InputSchemaMetricas{
			Type:       "object",
			Properties: map[string]PropertyMetricas{"expresion": {Type: "string"}},
			Required:   []string{"expresion"},
		},
	},
}

func ejecutarHerramientaMetricas(nombre string, params map[string]interface{}) (string, bool) {
	if nombre == "calcular" {
		expresion, _ := params["expresion"].(string)
		val, err := strconv.ParseFloat(expresion, 64)
		if err == nil {
			return fmt.Sprintf("%g", val), true
		}
		return "expresión no soportada", false
	}
	return "desconocida", false
}

func generateIDMetricas(n int) string {
	const hex = "0123456789abcdef"
	b := make([]byte, n)
	for i := range b {
		b[i] = hex[rand.Intn(16)]
	}
	return string(b)
}

func llamarAPIMetricas(mensajes []MensajeMetricas) (*APIResponseMetricas, error) {
	payload := APIRequestMetricas{
		Model:     modelMetricas,
		MaxTokens: 256,
		Tools:     toolsMetricas,
		Messages:  mensajes,
	}
	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", envBaseURL(), bytes.NewReader(body))
	req.Header.Set("x-api-key", os.Getenv("ANTHROPIC_API_KEY"))
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("content-type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)
	var ar APIResponseMetricas
	if err := json.Unmarshal(data, &ar); err != nil || len(ar.Content) == 0 {
		return nil, fmt.Errorf("respuesta inesperada: %s", data)
	}
	return &ar, nil
}

func ejecutarTareaConMetricasGo(tarea string) ResultadoTarea {
	taskID := generateIDMetricas(8)
	t0 := time.Now()
	inputTokens := 0
	outputTokens := 0
	toolOk := 0
	toolErr := 0

	mensajes := []MensajeMetricas{{Role: "user", Content: tarea}}

	for i := 0; i < 10; i++ {
		resp, err := llamarAPIMetricas(mensajes)
		if err != nil {
			errStr := err.Error()
			if len(errStr) > 200 {
				errStr = errStr[:200]
			}
			return ResultadoTarea{
				TaskID: taskID, Completada: false,
				LatenciaMs: float64(time.Since(t0).Milliseconds()),
				InputTokens: inputTokens, OutputTokens: outputTokens, Error: errStr,
			}
		}
		inputTokens += resp.Usage.InputTokens
		outputTokens += resp.Usage.OutputTokens
		mensajes = append(mensajes, MensajeMetricas{Role: "assistant", Content: resp.Content})

		if resp.StopReason == "end_turn" {
			return ResultadoTarea{
				TaskID: taskID, Completada: true,
				LatenciaMs:        float64(time.Since(t0).Milliseconds()),
				InputTokens:       inputTokens,
				OutputTokens:      outputTokens,
				ToolCallsExitosos: toolOk,
				ToolCallsFallidos: toolErr,
			}
		}

		var toolResults []map[string]interface{}
		for _, bloque := range resp.Content {
			if bloque.Type != "tool_use" {
				continue
			}
			resultado, ok := ejecutarHerramientaMetricas(bloque.Name, bloque.Input)
			if ok {
				toolOk++
			} else {
				toolErr++
			}
			toolResults = append(toolResults, map[string]interface{}{
				"type":        "tool_result",
				"tool_use_id": bloque.ID,
				"content":     resultado,
			})
		}
		mensajes = append(mensajes, MensajeMetricas{Role: "user", Content: toolResults})
	}

	return ResultadoTarea{
		TaskID: taskID, Completada: false,
		LatenciaMs:        float64(time.Since(t0).Milliseconds()),
		InputTokens:       inputTokens,
		OutputTokens:      outputTokens,
		ToolCallsExitosos: toolOk,
		ToolCallsFallidos: toolErr,
		Error:             "max iteraciones",
	}
}

func main() {
	fmt.Println("=== Métricas de agente ===\n")
	metricas := &MetricasAgente{}

	tareas := []string{
		"¿Cuánto es 15 * 23?",
		"Calcula la raíz cuadrada de 144.",
		"¿Cuántos días tiene un año normal?",
	}

	for _, tarea := range tareas {
		t := tarea
		if len(t) > 60 {
			t = t[:60]
		}
		fmt.Printf("Ejecutando: %s\n", t)
		resultado := ejecutarTareaConMetricasGo(tarea)
		metricas.Registrar(resultado)
		estado := "✗"
		if resultado.Completada {
			estado = "✓"
		}
		fmt.Printf("  [%s] %.0fms | $%.5f\n", estado, resultado.LatenciaMs, resultado.CosteUSD())
	}

	fmt.Println("\n─── Resumen de métricas ───")
	s := metricas.Resumen()
	keys := []string{
		"task_completion_rate", "error_rate", "latencia_p50_ms", "latencia_p95_ms",
		"cost_per_task_usd", "cost_total_usd", "tool_success_rate", "total_tareas",
	}
	for _, k := range keys {
		v := s[k]
		if v == float64(int(v)) {
			fmt.Printf("  %s: %d\n", k, int(v))
		} else {
			fmt.Printf("  %s: %.3f\n", k, v)
		}
	}

	alertas := metricas.Alertas(0.95, 30_000)
	if len(alertas) > 0 {
		fmt.Printf("\n[ALERTA] %v\n", alertas)
	} else {
		fmt.Println("\n[OK] Todas las métricas dentro de umbral")
	}
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
