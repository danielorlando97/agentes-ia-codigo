// Cómo ejecutar: make go FILE=go/14-observabilidad/logs.go
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"os"
	"time"
)

var modelLogs = envOr("SMALL_MODEL", "claude-haiku-4-5-20251001")

type StructLogger struct {
	ctx map[string]interface{}
}

func newStructLogger(ctx map[string]interface{}) *StructLogger {
	return &StructLogger{ctx: ctx}
}

func (l *StructLogger) Bind(extra map[string]interface{}) *StructLogger {
	merged := map[string]interface{}{}
	for k, v := range l.ctx {
		merged[k] = v
	}
	for k, v := range extra {
		merged[k] = v
	}
	return &StructLogger{ctx: merged}
}

func (l *StructLogger) emit(nivel, evento string, campos map[string]interface{}) {
	registro := map[string]interface{}{
		"ts":     time.Now().UTC().Format("2006-01-02T15:04:05Z"),
		"nivel":  nivel,
		"evento": evento,
	}
	for k, v := range l.ctx {
		registro[k] = v
	}
	for k, v := range campos {
		registro[k] = v
	}
	data, _ := json.Marshal(registro)
	log.Println(string(data))
}

func (l *StructLogger) Info(evento string, campos map[string]interface{}) {
	l.emit("INFO", evento, campos)
}

func (l *StructLogger) Error(evento string, campos map[string]interface{}) {
	l.emit("ERROR", evento, campos)
}

func (l *StructLogger) Warn(evento string, campos map[string]interface{}) {
	l.emit("WARN", evento, campos)
}

var baseLoggerLogs = newStructLogger(map[string]interface{}{
	"agente_version": "1.0.0",
	"entorno":        "demo",
})

func crearLoggerSesion(threadID, userID, sessionID string) *StructLogger {
	return baseLoggerLogs.Bind(map[string]interface{}{
		"thread_id":  threadID,
		"user_id":    userID,
		"session_id": sessionID,
	})
}

type MensajeLogs struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"`
}

type ToolLogs struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema InputSchemaLogs `json:"input_schema"`
}

type InputSchemaLogs struct {
	Type       string                     `json:"type"`
	Properties map[string]PropertyLogs    `json:"properties"`
	Required   []string                   `json:"required"`
}

type PropertyLogs struct {
	Type string `json:"type"`
}

type APIRequestLogs struct {
	Model     string        `json:"model"`
	MaxTokens int           `json:"max_tokens"`
	Tools     []ToolLogs    `json:"tools,omitempty"`
	Messages  []MensajeLogs `json:"messages"`
}

type ContentBlockLogs struct {
	Type  string                 `json:"type"`
	Text  string                 `json:"text,omitempty"`
	ID    string                 `json:"id,omitempty"`
	Name  string                 `json:"name,omitempty"`
	Input map[string]interface{} `json:"input,omitempty"`
}

type UsageLogs struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type APIResponseLogs struct {
	Content    []ContentBlockLogs `json:"content"`
	StopReason string             `json:"stop_reason"`
	Usage      UsageLogs          `json:"usage"`
}

var toolsLogs = []ToolLogs{
	{
		Name:        "buscar_info",
		Description: "Busca información sobre un tema.",
		InputSchema: InputSchemaLogs{
			Type:       "object",
			Properties: map[string]PropertyLogs{"tema": {Type: "string"}},
			Required:   []string{"tema"},
		},
	},
}

func ejecutarHerramientaLogs(nombre string, params map[string]interface{}) (string, bool) {
	time.Sleep(30 * time.Millisecond)
	if nombre == "buscar_info" {
		tema, _ := params["tema"].(string)
		return fmt.Sprintf("Información sobre %s: dato relevante de ejemplo.", tema), true
	}
	return "Herramienta no reconocida.", false
}

func generateIDLogs() string {
	const hex = "0123456789abcdef"
	b := make([]byte, 32)
	for i := range b {
		b[i] = hex[rand.Intn(16)]
	}
	return string(b)
}

func llamarAPILogs(mensajes []MensajeLogs) (*APIResponseLogs, error) {
	payload := APIRequestLogs{
		Model:     modelLogs,
		MaxTokens: 512,
		Tools:     toolsLogs,
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
	var ar APIResponseLogs
	if err := json.Unmarshal(data, &ar); err != nil || len(ar.Content) == 0 {
		return nil, fmt.Errorf("respuesta inesperada: %s", data)
	}
	return &ar, nil
}

func ejecutarAgenteLogs(tarea, userID string) (string, error) {
	threadID := generateIDLogs()
	sessionID := generateIDLogs()
	l := crearLoggerSesion(threadID, userID, sessionID)

	tareaLog := tarea
	if len(tareaLog) > 200 {
		tareaLog = tareaLog[:200]
	}
	l.Info("task.started", map[string]interface{}{"tarea": tareaLog, "modelo": modelLogs})
	tInicio := time.Now()

	mensajes := []MensajeLogs{{Role: "user", Content: tarea}}
	step := 0
	tokensInput := 0
	tokensOutput := 0
	var ultimaResp *APIResponseLogs

	for i := 0; i < 10; i++ {
		l.Info("llm.call.started", map[string]interface{}{"step": step, "modelo": modelLogs})
		t0 := time.Now()

		resp, err := llamarAPILogs(mensajes)
		if err != nil {
			l.Error("llm.call.failed", map[string]interface{}{
				"step":      step,
				"error_msg": err.Error(),
			})
			return "", err
		}
		latencia := time.Since(t0).Milliseconds()
		tokensInput += resp.Usage.InputTokens
		tokensOutput += resp.Usage.OutputTokens
		ultimaResp = resp
		l.Info("llm.call.completed", map[string]interface{}{
			"step":          step,
			"input_tokens":  resp.Usage.InputTokens,
			"output_tokens": resp.Usage.OutputTokens,
			"finish_reason": resp.StopReason,
			"latencia_ms":   latencia,
		})

		mensajes = append(mensajes, MensajeLogs{Role: "assistant", Content: resp.Content})

		if resp.StopReason == "end_turn" {
			break
		}

		var toolResults []map[string]interface{}
		for _, bloque := range resp.Content {
			if bloque.Type != "tool_use" {
				continue
			}
			inputJSON, _ := json.Marshal(bloque.Input)
			inputStr := string(inputJSON)
			if len(inputStr) > 300 {
				inputStr = inputStr[:300]
			}
			l.Info("tool.execution.started", map[string]interface{}{
				"step":   step,
				"tool":   bloque.Name,
				"params": inputStr,
			})
			t1 := time.Now()

			resultado, ok := ejecutarHerramientaLogs(bloque.Name, bloque.Input)
			latenciaTool := time.Since(t1).Milliseconds()

			if ok {
				l.Info("tool.execution.completed", map[string]interface{}{
					"step":        step,
					"tool":        bloque.Name,
					"success":     true,
					"latencia_ms": latenciaTool,
				})
			} else {
				errStr := resultado
				if len(errStr) > 300 {
					errStr = errStr[:300]
				}
				l.Error("tool.execution.failed", map[string]interface{}{
					"step":        step,
					"tool":        bloque.Name,
					"error":       errStr,
					"latencia_ms": latenciaTool,
				})
			}

			toolResults = append(toolResults, map[string]interface{}{
				"type":        "tool_result",
				"tool_use_id": bloque.ID,
				"content":     resultado,
			})
		}
		mensajes = append(mensajes, MensajeLogs{Role: "user", Content: toolResults})
		step++
	}

	duracion := time.Since(tInicio).Milliseconds()
	l.Info("task.completed", map[string]interface{}{
		"duracion_ms":   duracion,
		"steps":         step + 1,
		"tokens_input":  tokensInput,
		"tokens_output": tokensOutput,
	})

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
	fmt.Println("=== Logging estructurado ===\n")
	resultado, err := ejecutarAgenteLogs("¿Qué es la computación cuántica?", "user_demo")
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
