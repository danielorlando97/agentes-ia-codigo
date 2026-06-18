// Copiloto: sugerencia inline disparada por evento del editor. Sin loop, sin estado.
// Sin SDK: HTTP directo contra la API de Anthropic.

// Cómo ejecutar: make go FILE=go/01-que-es-un-agente/copiloto.go

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
)

var (
	model = envOr("MODEL", "claude-sonnet-4-6")
	apiURL = envBaseURL()
)

type response struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

func suggest(buffer string) (string, error) {
	body, _ := json.Marshal(map[string]any{
		"model":      model,
		"max_tokens": 256,
		"system":     "Eres un copiloto de codigo. Dado un fragmento de codigo, sugiere la continuacion mas probable. Responde solo con el codigo sugerido, sin explicaciones.",
		"messages": []map[string]string{
			{"role": "user", "content": fmt.Sprintf("Completa:\n\n```\n%s\n```", buffer)},
		},
	})
	req, _ := http.NewRequestWithContext(context.Background(), "POST", apiURL, bytes.NewReader(body))
	req.Header.Set("x-api-key", os.Getenv("ANTHROPIC_API_KEY"))
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("content-type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	var r response
	if err := json.Unmarshal(data, &r); err != nil {
		return "", fmt.Errorf("parse %s: %w", string(data), err)
	}
	var text string
	for _, b := range r.Content {
		if b.Type == "text" {
			text += b.Text
		}
	}
	return text, nil
}

func main() {
	code := "def fibonacci(n):\n    "
	fmt.Printf("Buffer:\n%s\n", code)
	s, err := suggest(code)
	if err != nil {
		fmt.Println("Error:", err)
		return
	}
	fmt.Printf("Sugerencia: %s\n", s)
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
