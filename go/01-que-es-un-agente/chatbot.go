// Chatbot: conversacion turn-by-turn con memoria de sesion. Sin tools, sin loop autonomo.
// Sin SDK: HTTP directo contra la API de Anthropic.

// Cómo ejecutar: make go FILE=go/01-que-es-un-agente/chatbot.go

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

var (
	model = envOr("MODEL", "claude-sonnet-4-6")
	apiURL = envBaseURL()
)

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type response struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

func callAPI(messages []message) (*response, error) {
	body, _ := json.Marshal(map[string]any{
		"model":      model,
		"max_tokens": 1024,
		"system":     "Eres un asistente util. Responde de forma concisa.",
		"messages":   messages,
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

func main() {
	session := []message{}
	reader := bufio.NewReader(os.Stdin)
	fmt.Println("Chatbot iniciado. Escribe 'salir' para terminar.")
	for {
		fmt.Print("> ")
		msg, _ := reader.ReadString('\n')
		msg = strings.TrimSpace(msg)
		if strings.ToLower(msg) == "salir" {
			break
		}
		session = append(session, message{Role: "user", Content: msg})
		resp, err := callAPI(session)
		if err != nil {
			fmt.Println("Error:", err)
			continue
		}
		var text string
		for _, b := range resp.Content {
			if b.Type == "text" {
				text += b.Text
			}
		}
		session = append(session, message{Role: "assistant", Content: text})
		fmt.Println(text)
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
