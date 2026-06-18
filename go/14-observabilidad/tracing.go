// Cómo ejecutar: make go FILE=go/14-observabilidad/tracing.go
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"time"
)

var model = envOr("SMALL_MODEL", "claude-haiku-4-5-20251001")

func generateID(n int) string {
	const hex = "0123456789abcdef"
	b := make([]byte, n)
	for i := range b {
		b[i] = hex[rand.Intn(16)]
	}
	return string(b)
}

type Span struct {
	Nombre    string
	TraceID   string
	SpanID    string
	ParentID  string
	Atributos map[string]interface{}
	InicioMs  int64
	FinMs     int64
}

func newSpan(nombre, traceID, parentID string) *Span {
	return &Span{
		Nombre:    nombre,
		TraceID:   traceID,
		SpanID:    generateID(16),
		ParentID:  parentID,
		Atributos: map[string]interface{}{},
		InicioMs:  time.Now().UnixMilli(),
	}
}

func (s *Span) SetAttribute(key string, value interface{}) {
	s.Atributos[key] = value
}

func (s *Span) End() {
	s.FinMs = time.Now().UnixMilli()
}

func (s *Span) DuracionMs() int64 {
	if s.FinMs == 0 {
		return 0
	}
	return s.FinMs - s.InicioMs
}

type Tracer struct {
	Nombre string
	spans  []*Span
	activo *Span
}

func newTracer(nombre string) *Tracer {
	return &Tracer{Nombre: nombre}
}

func (t *Tracer) StartSpan(nombre string, traceID string) *Span {
	tid := traceID
	if tid == "" {
		if t.activo != nil {
			tid = t.activo.TraceID
		} else {
			tid = generateID(32)
		}
	}
	parentID := ""
	if t.activo != nil {
		parentID = t.activo.SpanID
	}
	span := newSpan(nombre, tid, parentID)
	t.spans = append(t.spans, span)
	t.activo = span
	return span
}

func (t *Tracer) EndSpan(span *Span) {
	span.End()
	if span.ParentID != "" {
		for i := len(t.spans) - 1; i >= 0; i-- {
			if t.spans[i].SpanID == span.ParentID {
				t.activo = t.spans[i]
				return
			}
		}
	}
	t.activo = nil
}

func (t *Tracer) Report() {
	fmt.Println("\n─── Trace report ───")
	for _, s := range t.spans {
		indent := ""
		if s.ParentID != "" {
			indent = "  "
		}
		attrs, _ := json.Marshal(s.Atributos)
		fmt.Printf("%s[%s] %dms | %s\n", indent, s.Nombre, s.DuracionMs(), attrs)
	}
}

var tracer = newTracer("agente")

type Mensaje struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"`
}

type InputSchema struct {
	Type       string              `json:"type"`
	Properties map[string]Property `json:"properties"`
	Required   []string            `json:"required"`
}

type Property struct {
	Type string `json:"type"`
}

type Tool struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema InputSchema `json:"input_schema"`
}

type AnthropicRequest struct {
	Model     string      `json:"model"`
	MaxTokens int         `json:"max_tokens"`
	Tools     []Tool      `json:"tools,omitempty"`
	Messages  []Mensaje   `json:"messages"`
}

type ContentBlock struct {
	Type  string                 `json:"type"`
	Text  string                 `json:"text,omitempty"`
	ID    string                 `json:"id,omitempty"`
	Name  string                 `json:"name,omitempty"`
	Input map[string]interface{} `json:"input,omitempty"`
}

type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type AnthropicResponse struct {
	Content    []ContentBlock `json:"content"`
	StopReason string         `json:"stop_reason"`
	Usage      Usage          `json:"usage"`
}

var tools = []Tool{
	{
		Name:        "obtener_clima",
		Description: "Devuelve el clima actual de una ciudad.",
		InputSchema: InputSchema{
			Type:       "object",
			Properties: map[string]Property{"ciudad": {Type: "string"}},
			Required:   []string{"ciudad"},
		},
	},
}

func ejecutarHerramienta(nombre string, params map[string]interface{}) string {
	time.Sleep(50 * time.Millisecond)
	if nombre == "obtener_clima" {
		ciudad, _ := params["ciudad"].(string)
		return fmt.Sprintf("El clima en %s es soleado, 22°C.", ciudad)
	}
	return fmt.Sprintf("Herramienta '%s' no reconocida.", nombre)
}

func llamarAPI(mensajes []Mensaje) (*AnthropicResponse, error) {
	payload := AnthropicRequest{
		Model:     model,
		MaxTokens: 512,
		Tools:     tools,
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
	var ar AnthropicResponse
	if err := json.Unmarshal(data, &ar); err != nil || len(ar.Content) == 0 {
		return nil, fmt.Errorf("respuesta inesperada: %s", data)
	}
	return &ar, nil
}

func ejecutarAgente(tarea, threadID string) (string, error) {
	spanRaiz := tracer.StartSpan("agent.run", "")
	spanRaiz.SetAttribute("thread_id", threadID)
	if len(tarea) > 200 {
		spanRaiz.SetAttribute("tarea", tarea[:200])
	} else {
		spanRaiz.SetAttribute("tarea", tarea)
	}
	spanRaiz.SetAttribute("gen_ai.request.model", model)
	defer func() {
		tracer.EndSpan(spanRaiz)
		tracer.Report()
	}()

	mensajes := []Mensaje{{Role: "user", Content: tarea}}
	tokensTotales := 0
	step := 0
	var ultimaResp *AnthropicResponse

	for i := 0; i < 10; i++ {
		spanLlm := tracer.StartSpan("llm.call", spanRaiz.TraceID)
		spanLlm.SetAttribute("step", step)
		t0 := time.Now()

		resp, err := llamarAPI(mensajes)
		if err != nil {
			tracer.EndSpan(spanLlm)
			return "", err
		}
		latencia := time.Since(t0).Milliseconds()
		ultimaResp = resp

		spanLlm.SetAttribute("gen_ai.usage.input_tokens", resp.Usage.InputTokens)
		spanLlm.SetAttribute("gen_ai.usage.output_tokens", resp.Usage.OutputTokens)
		spanLlm.SetAttribute("gen_ai.response.finish_reason", resp.StopReason)
		spanLlm.SetAttribute("latencia_ms", latencia)
		tokensTotales += resp.Usage.InputTokens + resp.Usage.OutputTokens
		tracer.EndSpan(spanLlm)

		mensajes = append(mensajes, Mensaje{Role: "assistant", Content: resp.Content})

		if resp.StopReason == "end_turn" {
			break
		}

		var toolResults []map[string]interface{}
		for _, bloque := range resp.Content {
			if bloque.Type != "tool_use" {
				continue
			}
			spanTool := tracer.StartSpan("tool.call", spanRaiz.TraceID)
			spanTool.SetAttribute("tool.name", bloque.Name)
			inputJSON, _ := json.Marshal(bloque.Input)
			inputStr := string(inputJSON)
			if len(inputStr) > 300 {
				inputStr = inputStr[:300]
			}
			spanTool.SetAttribute("tool.input", inputStr)
			t1 := time.Now()

			resultado := ejecutarHerramienta(bloque.Name, bloque.Input)
			ok := len(resultado) < 20 || resultado[:15] != "Herramienta '"

			spanTool.SetAttribute("tool.latencia_ms", time.Since(t1).Milliseconds())
			spanTool.SetAttribute("tool.success", ok)
			tracer.EndSpan(spanTool)

			toolResults = append(toolResults, map[string]interface{}{
				"type":        "tool_result",
				"tool_use_id": bloque.ID,
				"content":     resultado,
			})
		}
		mensajes = append(mensajes, Mensaje{Role: "user", Content: toolResults})
		step++
	}

	spanRaiz.SetAttribute("tokens_totales", tokensTotales)
	spanRaiz.SetAttribute("steps_totales", step+1)

	if ultimaResp != nil {
		for _, b := range ultimaResp.Content {
			if b.Type == "text" {
				return b.Text, nil
			}
		}
	}
	return "", nil
}

func main() {
	threadID := generateID(32)
	fmt.Println("=== Agente con tracing ===")
	resultado, err := ejecutarAgente("¿Qué tiempo hace en Madrid hoy?", threadID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if len(resultado) > 300 {
		resultado = resultado[:300]
	}
	fmt.Printf("\nRespuesta: %s\n", resultado)
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
