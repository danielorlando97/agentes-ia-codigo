// Integración de Mem0 como capa de memoria para un agente existente.
//
// Mem0 extrae memorias automáticamente de cada turno conversacional
// mediante un LLM auxiliar. La integración mínima requiere 3 llamadas:
// add() al final de cada turno, search() al inicio, getAll() para contexto completo.
//
// Requiere:
//
//	export MEM0_API_KEY=...
//	export ANTHROPIC_API_KEY=...
//
// Cómo ejecutar:
//
//	export MEM0_API_KEY=tu-clave
//	export ANTHROPIC_API_KEY=tu-clave
//	make go FILE=go/06-memoria/20-implementaciones/mem0_integration.go
//
// Qué esperar:
//
//	El agente extrae memorias automáticamente de cada turno. Muestra el ciclo
//	add() → search() → getAll() en acción.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
)

const (
	mem0Base = "https://api.mem0.ai/v1"
	apiBase  = "https://api.anthropic.com/v1"
	userID   = "usuario-demo-go"
)

var (
	mem0Key    = os.Getenv("MEM0_API_KEY")
	anthropicKey = os.Getenv("ANTHROPIC_API_KEY")
	modelo = func() string {
		if v := os.Getenv("MODEL"); v != "" {
			return v
		}
		return "claude-sonnet-4-6"
	}()
)

type Mem0Memory struct {
	ID     string `json:"id"`
	Memory string `json:"memory"`
}

func mem0Add(messages []map[string]string, uid string) error {
	body, _ := json.Marshal(map[string]any{"messages": messages, "user_id": uid})
	req, _ := http.NewRequest("POST", mem0Base+"/memories/", bytes.NewReader(body))
	req.Header.Set("Authorization", "Token "+mem0Key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("Mem0 add error %d: %s", resp.StatusCode, string(data[:minI(len(data), 200)]))
	}
	return nil
}

func mem0Search(query, uid string, limit int) ([]Mem0Memory, error) {
	params := url.Values{"query": {query}, "user_id": {uid}, "limit": {fmt.Sprint(limit)}}
	req, _ := http.NewRequest("GET", mem0Base+"/memories/search/?"+params.Encode(), nil)
	req.Header.Set("Authorization", "Token "+mem0Key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var mems []Mem0Memory
	json.NewDecoder(resp.Body).Decode(&mems) //nolint:errcheck
	return mems, nil
}

func mem0GetAll(uid string) ([]Mem0Memory, error) {
	params := url.Values{"user_id": {uid}}
	req, _ := http.NewRequest("GET", mem0Base+"/memories/?"+params.Encode(), nil)
	req.Header.Set("Authorization", "Token "+mem0Key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var mems []Mem0Memory
	json.NewDecoder(resp.Body).Decode(&mems) //nolint:errcheck
	return mems, nil
}

func recuperarContexto(query string) string {
	mems, err := mem0Search(query, userID, 5)
	if err != nil || len(mems) == 0 {
		return ""
	}
	out := "## Memoria recuperada\n"
	for _, m := range mems {
		out += "- " + m.Memory + "\n"
	}
	return out
}

type Mensaje struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func turno(historial []Mensaje, mensaje string) (string, []Mensaje, error) {
	// 1. Recuperar contexto relevante
	system := "Eres un asistente técnico."
	if ctx := recuperarContexto(mensaje); ctx != "" {
		system += "\n\n" + ctx
	}

	historial = append(historial, Mensaje{Role: "user", Content: mensaje})

	body, _ := json.Marshal(map[string]any{
		"model":      modelo,
		"max_tokens": 1024,
		"system":     system,
		"messages":   historial,
	})
	req, _ := http.NewRequest("POST", apiBase+"/messages", bytes.NewReader(body))
	req.Header.Set("x-api-key", anthropicKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", historial, err
	}
	defer resp.Body.Close()

	var result struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil || len(result.Content) == 0 {
		return "", historial, fmt.Errorf("error decodificando respuesta")
	}
	respuesta := result.Content[0].Text
	historial = append(historial, Mensaje{Role: "assistant", Content: respuesta})

	// 2. Guardar turno para sesiones futuras (post-turno — fuera del hot path ideal)
	mem0Add([]map[string]string{
		{"role": "user", "content": mensaje},
		{"role": "assistant", "content": respuesta},
	}, userID) //nolint:errcheck

	return respuesta, historial, nil
}

func main() {
	if mem0Key == "" {
		fmt.Fprintln(os.Stderr, "MEM0_API_KEY no configurada. Exporta la clave y reintenta.")
		os.Exit(1)
	}
	if anthropicKey == "" {
		fmt.Fprintln(os.Stderr, "ANTHROPIC_API_KEY no configurada.")
		os.Exit(1)
	}

	var historial []Mensaje

	// Turno 1: el agente aprende la preferencia
	r1, historial, err := turno(historial, "Prefiero trabajar con Python 3.12 en producción.")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error turno 1: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Agente: %s\n\n", r1[:minI(len(r1), 120)])

	// Turno 2: nueva sesión — historial vacío, pero Mem0 recupera la preferencia
	var historialNuevo []Mensaje
	r2, _, err := turno(historialNuevo, "¿Qué lenguaje debería usar para el nuevo servicio?")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error turno 2: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Agente (sesión nueva): %s\n", r2[:minI(len(r2), 120)])

	// Ver todas las memorias guardadas
	fmt.Println("\n--- memorias almacenadas ---")
	mems, _ := mem0GetAll(userID)
	for _, m := range mems {
		fmt.Printf("  %s\n", m.Memory)
	}

	_ = historial
}

func minI(a, b int) int {
	if a < b {
		return a
	}
	return b
}
