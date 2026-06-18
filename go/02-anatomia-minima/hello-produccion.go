// Hello agent de producción: system prompt + error handling + logging.
// Combina V2 (system prompt), V3 (errores gestionados) y V4 (logging).
// Sin SDK: HTTP directo contra la API de Anthropic.

// Cómo ejecutar: make go FILE=go/02-anatomia-minima/hello-produccion.go

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"
)

const (
	hpMaxIterations = 20
)

var (
	hpModel = envOr("MODEL", "claude-sonnet-4-6")
	hpAPIURL = envBaseURL()
)

const hpSystem = `Eres un asistente de productividad. Responde en español, sé conciso.
Cuando el usuario pida una hora, usa get_time con el offset UTC correcto.
Cuando el usuario pida una suma, usa add.
Si no puedes completar una tarea con las herramientas disponibles, dilo claramente.`

var hpTools = []map[string]any{
	{
		"name":        "get_time",
		"description": "Devuelve la hora actual en una zona horaria (offset UTC en horas).",
		"input_schema": map[string]any{
			"type":       "object",
			"properties": map[string]any{"utc_offset": map[string]string{"type": "number"}},
			"required":   []string{"utc_offset"},
		},
	},
	{
		"name":        "add",
		"description": "Suma dos números.",
		"input_schema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"a": map[string]string{"type": "number"},
				"b": map[string]string{"type": "number"},
			},
			"required": []string{"a", "b"},
		},
	},
}

type hpBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   string          `json:"content,omitempty"`
}

type hpUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type hpResponse struct {
	Content    []hpBlock `json:"content"`
	StopReason string    `json:"stop_reason"`
	Usage      hpUsage   `json:"usage"`
}

type hpMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

func hpExecuteTool(name string, input json.RawMessage) string {
	var args map[string]float64
	_ = json.Unmarshal(input, &args)

	switch name {
	case "get_time":
		offset := args["utc_offset"]
		if offset < -12 || offset > 14 {
			return fmt.Sprintf("Error: utc_offset %.1f fuera de rango [-12, 14]", offset)
		}
		dur := time.Duration(offset * float64(time.Hour))
		return time.Now().UTC().Add(dur).Format(time.RFC3339)
	case "add":
		return fmt.Sprintf("%v", args["a"]+args["b"])
	}
	return fmt.Sprintf("Error: herramienta '%s' desconocida", name)
}

func hpCallAPI(messages []hpMessage) (*hpResponse, error) {
	body, _ := json.Marshal(map[string]any{
		"model":      hpModel,
		"max_tokens": 2048,
		"system":     hpSystem,
		"tools":      hpTools,
		"messages":   messages,
	})

	req, _ := http.NewRequestWithContext(context.Background(), "POST", hpAPIURL, bytes.NewReader(body))
	req.Header.Set("x-api-key", os.Getenv("ANTHROPIC_API_KEY"))
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("content-type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)
	var r hpResponse
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("parse error: %s: %w", string(data), err)
	}
	return &r, nil
}

func hpRunAgent(task string) string {
	rawTask, _ := json.Marshal(task)
	messages := []hpMessage{{Role: "user", Content: rawTask}}

	for iter := range hpMaxIterations {
		resp, err := hpCallAPI(messages)
		if err != nil {
			return fmt.Sprintf("Error API: %v", err)
		}

		log.Printf("iter=%d/%d stop=%s tokens=%d+%d",
			iter+1, hpMaxIterations, resp.StopReason,
			resp.Usage.InputTokens, resp.Usage.OutputTokens)

		switch resp.StopReason {
		case "end_turn", "stop_sequence":
			for _, b := range resp.Content {
				if b.Type == "text" {
					return b.Text
				}
			}
			return ""

		case "tool_use":
			// Añadir respuesta del modelo al historial
			assistantContent, _ := json.Marshal(resp.Content)
			messages = append(messages, hpMessage{Role: "assistant", Content: assistantContent})

			// Ejecutar todas las tool calls y construir tool_results en un único mensaje
			var toolResults []hpBlock
			for _, b := range resp.Content {
				if b.Type != "tool_use" {
					continue
				}
				result := hpExecuteTool(b.Name, b.Input)
				log.Printf("  → %s = %s", b.Name, result)
				toolResults = append(toolResults, hpBlock{
					Type:      "tool_result",
					ToolUseID: b.ID,
					Content:   result,
				})
			}
			toolResultsJSON, _ := json.Marshal(toolResults)
			messages = append(messages, hpMessage{Role: "user", Content: toolResultsJSON})

		default:
			log.Printf("stop_reason inesperado: %s", resp.StopReason)
			return "[stop_reason inesperado]"
		}
	}

	return "[max iteraciones]"
}

func main() {
	resultado := hpRunAgent("¿Qué hora es en Tokio (UTC+9) y cuánto es 47 + 89?")
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
