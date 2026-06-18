// Variante ReAct: mismo loop que agente-minimo, pero con CoT explicito (Thought antes de Action).

// Cómo ejecutar: make go FILE=go/01-que-es-un-agente/agente-react.go

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

const (
	reactMaxIterations = 10
)

var (
	reactModel = envOr("MODEL", "claude-sonnet-4-6")
	reactAPIURL = envBaseURL()
)

const reactSystem = "Eres un agente ReAct. Antes de cada llamada a herramienta escribe una linea " +
	"que empiece por 'Thought:' explicando tu razonamiento; luego usa la herramienta. " +
	"Cuando tengas la respuesta final, escribela despues de un 'Final answer:'."

type reactMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type reactBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   string          `json:"content,omitempty"`
}

type reactResponse struct {
	Content    []reactBlock `json:"content"`
	StopReason string       `json:"stop_reason"`
}

var reactTools = []map[string]any{
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
		"description": "Suma dos numeros.",
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

func reactExecuteTool(name string, input json.RawMessage) string {
	var args map[string]float64
	_ = json.Unmarshal(input, &args)
	switch name {
	case "get_time":
		offset := time.Duration(args["utc_offset"] * float64(time.Hour))
		return time.Now().UTC().Add(offset).Format(time.RFC3339)
	case "add":
		return fmt.Sprintf("%v", args["a"]+args["b"])
	}
	return fmt.Sprintf("Tool '%s' no existe", name)
}

func reactCallAPI(messages []reactMessage) (*reactResponse, error) {
	body, _ := json.Marshal(map[string]any{
		"model":      reactModel,
		"max_tokens": 1024,
		"system":     reactSystem,
		"messages":   messages,
		"tools":      reactTools,
	})
	req, _ := http.NewRequestWithContext(context.Background(), "POST", reactAPIURL, bytes.NewReader(body))
	req.Header.Set("x-api-key", os.Getenv("ANTHROPIC_API_KEY"))
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("content-type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	var r reactResponse
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("parse %s: %w", string(data), err)
	}
	return &r, nil
}

func runReact(task string) string {
	rawTask, _ := json.Marshal(task)
	messages := []reactMessage{{Role: "user", Content: rawTask}}
	trace := []string{}

	for i := 0; i < reactMaxIterations; i++ {
		resp, err := reactCallAPI(messages)
		if err != nil {
			return err.Error()
		}

		for _, b := range resp.Content {
			if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
				trace = append(trace, strings.TrimSpace(b.Text))
			} else if b.Type == "tool_use" {
				trace = append(trace, fmt.Sprintf("Action: %s(%s)", b.Name, string(b.Input)))
			}
		}

		if resp.StopReason == "end_turn" || resp.StopReason == "stop_sequence" {
			return strings.Join(trace, "\n")
		}

		if resp.StopReason == "tool_use" {
			results := []reactBlock{}
			for _, b := range resp.Content {
				if b.Type == "tool_use" {
					out := reactExecuteTool(b.Name, b.Input)
					trace = append(trace, "Observation: "+out)
					results = append(results, reactBlock{
						Type:      "tool_result",
						ToolUseID: b.ID,
						Content:   out,
					})
				}
			}
			asst, _ := json.Marshal(resp.Content)
			user, _ := json.Marshal(results)
			messages = append(messages, reactMessage{Role: "assistant", Content: asst})
			messages = append(messages, reactMessage{Role: "user", Content: user})
			continue
		}

		break
	}

	return strings.Join(append(trace, "[max iteraciones]"), "\n")
}

func main() {
	fmt.Println(runReact("Que hora es en Tokio (UTC+9), y cuanto es 47 + 89?"))
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
