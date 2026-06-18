// Streaming SSE: muestra tokens del agente en tiempo real con eventos de tool calls

// Cómo ejecutar: make go FILE=go/17-produccion/streaming.go

package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

var streamingModel = envOr("MODEL", "claude-sonnet-4-6")

var herramientas = []map[string]interface{}{
	{
		"name":        "buscar_docs",
		"description": "Busca en la documentación. Úsala cuando el usuario pregunte por APIs o funciones.",
		"input_schema": map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{"query": map[string]string{"type": "string"}},
			"required":   []string{"query"},
		},
	},
}

func ejecutarHerramienta(nombre string, params map[string]interface{}) string {
	if nombre == "buscar_docs" {
		q, _ := params["query"].(string)
		return fmt.Sprintf("Documentación para '%s': función disponible desde v2.0, acepta str y devuelve dict.", q)
	}
	return fmt.Sprintf("Error: herramienta '%s' no encontrada.", nombre)
}

type streamRequest struct {
	Model     string                   `json:"model"`
	MaxTokens int                      `json:"max_tokens"`
	Tools     []map[string]interface{} `json:"tools"`
	Messages  []map[string]interface{} `json:"messages"`
	Stream    bool                     `json:"stream"`
}

type streamEvent struct {
	Type  string          `json:"type"`
	Index int             `json:"index"`
	Delta json.RawMessage `json:"delta"`
}

type deltaContent struct {
	Type        string `json:"type"`
	Text        string `json:"text"`
	PartialJSON string `json:"partial_json"`
}

type contentBlock struct {
	Type  string                 `json:"type"`
	Text  string                 `json:"text"`
	ID    string                 `json:"id"`
	Name  string                 `json:"name"`
	Input map[string]interface{} `json:"input"`
}

type messageResponse struct {
	StopReason string         `json:"stop_reason"`
	Content    []contentBlock `json:"content"`
}

func postStream(payload interface{}) (*http.Response, error) {
	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", envBaseURL(), bytes.NewReader(body))
	req.Header.Set("x-api-key", os.Getenv("ANTHROPIC_API_KEY"))
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("content-type", "application/json")
	return http.DefaultClient.Do(req)
}

func streamAgenteSimple(pregunta string) error {
	payload := streamRequest{
		Model:     streamingModel,
		MaxTokens: 1024,
		Tools:     herramientas,
		Messages:  []map[string]interface{}{{"role": "user", "content": pregunta}},
		Stream:    true,
	}

	resp, err := postStream(payload)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var finalMsg messageResponse
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")
			if data == "[DONE]" {
				break
			}
			var ev streamEvent
			if json.Unmarshal([]byte(data), &ev) != nil {
				continue
			}
			switch ev.Type {
			case "content_block_delta":
				var d deltaContent
				if json.Unmarshal(ev.Delta, &d) == nil && d.Type == "text_delta" {
					fmt.Print(d.Text)
				}
			case "message_delta":
				var md struct {
					StopReason string `json:"stop_reason"`
				}
				json.Unmarshal(ev.Delta, &md)
				finalMsg.StopReason = md.StopReason
			case "content_block_start":
				var bs struct {
					ContentBlock contentBlock `json:"content_block"`
				}
				if json.Unmarshal([]byte(data), &bs) == nil {
					finalMsg.Content = append(finalMsg.Content, bs.ContentBlock)
				}
			}
		}
	}

	for _, bloque := range finalMsg.Content {
		if bloque.Type == "tool_use" {
			fmt.Printf("\n[tool: %s(%v)]\n", bloque.Name, bloque.Input)
			resultado := ejecutarHerramienta(bloque.Name, bloque.Input)
			if len(resultado) > 100 {
				resultado = resultado[:100]
			}
			fmt.Printf("[resultado: %s]\n", resultado)
		}
	}
	fmt.Println()
	return nil
}

type sseEvent struct {
	Type    string `json:"type"`
	Content string `json:"content,omitempty"`
	Tool    string `json:"tool,omitempty"`
}

func streamLoopReact(pregunta string, queue chan sseEvent) {
	mensajes := []map[string]interface{}{{"role": "user", "content": pregunta}}
	const maxPasos = 10

	for paso := 0; paso < maxPasos; paso++ {
		payload := streamRequest{
			Model:     streamingModel,
			MaxTokens: 1024,
			Tools:     herramientas,
			Messages:  mensajes,
			Stream:    true,
		}

		resp, err := postStream(payload)
		if err != nil {
			queue <- sseEvent{Type: "error", Content: err.Error()}
			return
		}

		var bloques []contentBlock
		var stopReason string

		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := strings.TrimPrefix(line, "data: ")
			if data == "[DONE]" {
				break
			}
			var ev streamEvent
			if json.Unmarshal([]byte(data), &ev) != nil {
				continue
			}
			switch ev.Type {
			case "content_block_delta":
				var d deltaContent
				if json.Unmarshal(ev.Delta, &d) == nil && d.Type == "text_delta" {
					queue <- sseEvent{Type: "text", Content: d.Text}
				}
			case "message_delta":
				var md struct {
					StopReason string `json:"stop_reason"`
				}
				json.Unmarshal(ev.Delta, &md)
				stopReason = md.StopReason
			case "content_block_start":
				var bs struct {
					ContentBlock contentBlock `json:"content_block"`
				}
				if json.Unmarshal([]byte(data), &bs) == nil {
					bloques = append(bloques, bs.ContentBlock)
				}
			}
		}
		resp.Body.Close()

		if stopReason == "end_turn" {
			queue <- sseEvent{Type: "done"}
			return
		}

		var toolResults []map[string]interface{}
		for _, bloque := range bloques {
			if bloque.Type == "tool_use" {
				queue <- sseEvent{Type: "tool_start", Tool: bloque.Name}
				resultado := ejecutarHerramienta(bloque.Name, bloque.Input)
				queue <- sseEvent{Type: "tool_done", Tool: bloque.Name}
				toolResults = append(toolResults, map[string]interface{}{
					"type":        "tool_result",
					"tool_use_id": bloque.ID,
					"content":     resultado,
				})
			}
		}
		if len(toolResults) > 0 {
			mensajes = append(mensajes, map[string]interface{}{"role": "user", "content": toolResults})
		}
	}

	queue <- sseEvent{Type: "error", Content: "Límite de pasos alcanzado"}
}

func consumirStream(queue chan sseEvent) {
	for evento := range queue {
		switch evento.Type {
		case "text":
			fmt.Print(evento.Content)
		case "tool_start":
			fmt.Printf("\n[iniciando %s...]\n", evento.Tool)
		case "tool_done":
			fmt.Printf("[%s completado]\n", evento.Tool)
		case "done":
			fmt.Println()
			return
		case "error":
			fmt.Printf("\n[error: %s]\n", evento.Content)
			return
		}
	}
}

func main() {
	fmt.Println("=== Stream simple ===")
	if err := streamAgenteSimple("¿Qué hace la función filter_context?"); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
	}

	fmt.Println("\n=== Loop ReAct con streaming ===")
	queue := make(chan sseEvent, 100)

	go streamLoopReact("Busca cómo funciona filter_context y explícamelo.", queue)
	consumirStream(queue)
}

var _ = io.Discard

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
