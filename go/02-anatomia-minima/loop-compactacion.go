// Variante V5: loop con compactación de contexto.
//
// Cuando el historial se acerca al límite de la ventana, un paso intermedio
// comprime los mensajes antiguos en un resumen. Permite sesiones de horas
// sin agotar el contexto.
// Sin SDK: HTTP directo contra la API de Anthropic.

// Cómo ejecutar: make go FILE=go/02-anatomia-minima/loop-compactacion.go

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

const (
	contextThreshold = 40_000                      // tokens; umbral conservador para este ejemplo
	maxIterations    = 50
)

var (
	mainModel = envOr("MODEL", "claude-sonnet-4-6")
	compactModel = envOr("SMALL_MODEL", "claude-haiku-4-5-20251001")  // // modelo barato para compactar
	apiEndpoint = envBaseURL()
)

type message struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type block struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   string          `json:"content,omitempty"`
}

type apiResponse struct {
	Content    []block `json:"content"`
	StopReason string  `json:"stop_reason"`
}

var tools = []map[string]any{
	{
		"name":        "get_time",
		"description": "Returns the current time in a timezone (UTC offset in hours).",
		"input_schema": map[string]any{
			"type":       "object",
			"properties": map[string]any{"utc_offset": map[string]string{"type": "number"}},
			"required":   []string{"utc_offset"},
		},
	},
	{
		"name":        "add",
		"description": "Sums two numbers.",
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

func executeTool(name string, input json.RawMessage) string {
	var args map[string]float64
	_ = json.Unmarshal(input, &args)
	switch name {
	case "get_time":
		offset := time.Duration(args["utc_offset"] * float64(time.Hour))
		return time.Now().UTC().Add(offset).Format(time.RFC3339)
	case "add":
		return fmt.Sprintf("%v", args["a"]+args["b"])
	}
	return fmt.Sprintf("Tool '%s' desconocida", name)
}

// estimateTokens aproxima len(json.dumps(messages)) // 4 del original Python.
func estimateTokens(messages []message) int {
	data, _ := json.Marshal(messages)
	return len(data) / 4
}

// callAPIRaw realiza una petición HTTP a la API de Anthropic con el payload dado.
func callAPIRaw(payload map[string]any) (*apiResponse, error) {
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
	return &r, nil
}

// compact comprime el historial intermedio en un resumen.
//
// Conserva los primeros 2 mensajes (tarea original) y los últimos 6 (estado reciente).
// El intermedio se resume en una llamada al modelo barato.
func compact(messages []message) ([]message, error) {
	if len(messages) <= 8 {
		return messages, nil
	}

	first := messages[:2]      // tarea original — siempre conservada
	recent := messages[len(messages)-6:] // estado reciente — siempre conservado
	toCompress := messages[2 : len(messages)-6]

	if len(toCompress) == 0 {
		return messages, nil
	}

	fmt.Printf("  [compactación] comprimiendo %d mensajes intermedios...\n", len(toCompress))

	// Serializar el historial a comprimir; truncar a 15000 chars para no sobrepasar
	// el contexto del modelo barato.
	rawHistory, _ := json.Marshal(toCompress)
	historial := string(rawHistory)
	if len(historial) > 15000 {
		historial = historial[:15000]
	}

	promptContent := "Resume este historial de un agente. Preserva exactamente:\n" +
		"- Cada herramienta llamada y su resultado\n" +
		"- Cada archivo leído o modificado\n" +
		"- Cada decisión tomada y por qué\n" +
		"- El estado actual de la tarea\n\n" +
		"Historial: " + historial

	rawPrompt, _ := json.Marshal(promptContent)
	summaryResp, err := callAPIRaw(map[string]any{
		"model":      compactModel,
		"max_tokens": 1500,
		"messages": []message{
			{Role: "user", Content: rawPrompt},
		},
	})
	if err != nil {
		return messages, fmt.Errorf("compact: %w", err)
	}

	// Extraer texto del resumen
	summaryText := ""
	for _, b := range summaryResp.Content {
		if b.Type == "text" {
			summaryText += b.Text
		}
	}

	compressedContent := fmt.Sprintf("[HISTORIAL COMPRIMIDO]\n%s\n[FIN]", summaryText)
	rawCompressed, _ := json.Marshal(compressedContent)
	compressed := message{Role: "user", Content: rawCompressed}

	result := make([]message, 0, len(first)+1+len(recent))
	result = append(result, first...)
	result = append(result, compressed)
	result = append(result, recent...)
	return result, nil
}

func runCompactAgent(task string) string {
	// Loop con compactación automática cuando el contexto crece.
	rawTask, _ := json.Marshal(task)
	messages := []message{{Role: "user", Content: rawTask}}

	for iteration := 0; iteration < maxIterations; iteration++ {
		// Compactar si el contexto supera el umbral
		currentTokens := estimateTokens(messages)
		if currentTokens > contextThreshold {
			var err error
			messages, err = compact(messages)
			if err != nil {
				return err.Error()
			}
			fmt.Printf("  [iter=%d] contexto compactado → ~%d tokens\n", iteration+1, estimateTokens(messages))
		} else {
			fmt.Printf("  [iter=%d] contexto ~%d tokens\n", iteration+1, currentTokens)
		}

		resp, err := callAPIRaw(map[string]any{
			"model":      mainModel,
			"max_tokens": 4096,
			"messages":   messages,
			"tools":      tools,
		})
		if err != nil {
			return err.Error()
		}

		if resp.StopReason == "end_turn" || resp.StopReason == "stop_sequence" {
			out := ""
			for _, b := range resp.Content {
				if b.Type == "text" {
					out += b.Text
				}
			}
			return out
		}

		if resp.StopReason == "tool_use" {
			results := []block{}
			for _, b := range resp.Content {
				if b.Type == "tool_use" {
					results = append(results, block{
						Type:      "tool_result",
						ToolUseID: b.ID,
						Content:   executeTool(b.Name, b.Input),
					})
				}
			}
			asst, _ := json.Marshal(resp.Content)
			user, _ := json.Marshal(results)
			messages = append(messages, message{Role: "assistant", Content: asst})
			messages = append(messages, message{Role: "user", Content: user})
			continue
		}

		break
	}

	return "[max iteraciones]"
}

func main() {
	result := runCompactAgent("¿Qué hora es en Tokio (UTC+9) y cuánto es 47 + 89?")
	fmt.Printf("\nRespuesta: %s\n", result)
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
