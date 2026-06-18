// Loop minimo: LLM + tools + iteracion hasta end_turn.
// Sin SDK: HTTP directo contra la API de Anthropic.

// Cómo ejecutar: make go FILE=go/01-que-es-un-agente/agente-minimo.go

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
	maxIterations = 10
)

var (
	model = envOr("MODEL", "claude-sonnet-4-6")
	apiURL = envBaseURL()
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

type response struct {
	Content    []block `json:"content"`
	StopReason string  `json:"stop_reason"`
}

var tools = []map[string]any{
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
	return fmt.Sprintf("Tool '%s' no existe", name)
}

func callAPI(messages []message) (*response, error) {
	body, _ := json.Marshal(map[string]any{
		"model":      model,
		"max_tokens": 1024,
		"messages":   messages,
		"tools":      tools,
	})
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
	var r response
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("parse %s: %w", string(data), err)
	}
	return &r, nil
}

func runAgent(task string) string {
	rawTask, _ := json.Marshal(task)
	messages := []message{{Role: "user", Content: rawTask}}

	for i := 0; i < maxIterations; i++ {
		resp, err := callAPI(messages)
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
	fmt.Println(runAgent("Que hora es en Tokio (UTC+9), y cuanto es 47 + 89?"))
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
