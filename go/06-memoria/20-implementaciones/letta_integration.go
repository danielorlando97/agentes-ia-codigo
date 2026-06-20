// Integración con Letta via REST API: agente que gestiona su propia memoria.
//
// En Letta, el LLM controla explícitamente qué guardar y qué recuperar
// mediante tool calls de memoria nativas (core_memory_replace,
// archival_memory_insert, archival_memory_search).
//
// Requiere:
//
//	export LETTA_API_KEY=...  (para cloud Letta)
//	# o letta server --port 8283 y LETTA_BASE_URL=http://localhost:8283
//
// Cómo ejecutar:
//
//	export LETTA_API_KEY=tu-clave
//	make go FILE=go/06-memoria/20-implementaciones/letta_integration.go
//
// Qué esperar:
//
//	Crea el agente, envía dos turnos y muestra el estado final de core_memory.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
)

var (
	lettaBase = func() string {
		if v := os.Getenv("LETTA_BASE_URL"); v != "" {
			return v
		}
		return "https://api.letta.ai"
	}()
	lettaKey = os.Getenv("LETTA_API_KEY")
	model    = func() string {
		if v := os.Getenv("MODEL"); v != "" {
			return v
		}
		return "claude-sonnet-4-6"
	}()
)

func lettaRequest(method, path string, body any) ([]byte, error) {
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		bodyReader = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, lettaBase+path, bodyReader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if lettaKey != "" {
		req.Header.Set("Authorization", "Bearer "+lettaKey)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("Letta API %d: %s", resp.StatusCode, string(data[:min(len(data), 200)]))
	}
	return data, nil
}

func crearAgente() (string, error) {
	payload := map[string]any{
		"name":      "asistente-tecnico-go",
		"model":     model,
		"embedding": "letta-free",
		"memory": map[string]any{
			"memory_blocks": []map[string]any{
				{"label": "human", "value": "El usuario es un desarrollador de software.", "limit": 5000},
				{
					"label": "persona",
					"value": "Eres un asistente técnico que recuerda las preferencias del usuario y las usa para dar respuestas personalizadas.",
					"limit": 5000,
				},
			},
		},
	}
	data, err := lettaRequest("POST", "/v1/agents/", payload)
	if err != nil {
		return "", err
	}
	var resp struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return "", err
	}
	return resp.ID, nil
}

func enviarMensaje(agentID, mensaje string) (string, error) {
	payload := map[string]any{
		"messages": []map[string]string{{"role": "user", "content": mensaje}},
	}
	data, err := lettaRequest("POST", "/v1/agents/"+agentID+"/messages", payload)
	if err != nil {
		return "", err
	}
	var resp struct {
		Messages []struct {
			MessageType string `json:"message_type"`
			Content     string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return "", err
	}
	texto := ""
	for _, m := range resp.Messages {
		if m.MessageType == "assistant_message" {
			texto += m.Content + " "
		}
	}
	if texto == "" {
		return "[sin respuesta de texto]", nil
	}
	return texto, nil
}

func verMemoriaCore(agentID string) (human, persona string, err error) {
	data, err := lettaRequest("GET", "/v1/agents/"+agentID+"/core-memory", nil)
	if err != nil {
		return "", "", err
	}
	var resp struct {
		Memory map[string]struct {
			Value string `json:"value"`
		} `json:"memory"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return "", "", err
	}
	return resp.Memory["human"].Value, resp.Memory["persona"].Value, nil
}

func eliminarAgente(agentID string) {
	lettaRequest("DELETE", "/v1/agents/"+agentID, nil) //nolint:errcheck
}

func main() {
	if lettaKey == "" {
		fmt.Fprintln(os.Stderr, "LETTA_API_KEY no configurada.")
		fmt.Fprintln(os.Stderr, "Exporta la clave o inicia letta server --port 8283 y configura LETTA_BASE_URL.")
		os.Exit(1)
	}

	fmt.Println("Creando agente Letta...")
	agentID, err := crearAgente()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creando agente: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Agente creado: %s\n\n", agentID)

	// Turno 1: el agente aprende la preferencia y la guarda en core_memory
	r1, err := enviarMensaje(agentID, "Prefiero trabajar con Python 3.12 en producción.")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error turno 1: %v\n", err)
	} else {
		fmt.Printf("Agente: %s\n\n", r1[:min(len(r1), 150)])
	}

	// Turno 2: el agente usa la memoria para responder
	r2, err := enviarMensaje(agentID, "¿Qué lenguaje debería usar para el nuevo microservicio?")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error turno 2: %v\n", err)
	} else {
		fmt.Printf("Agente: %s\n\n", r2[:min(len(r2), 150)])
	}

	// Ver qué tiene en core_memory tras los dos turnos
	human, persona, err := verMemoriaCore(agentID)
	if err == nil {
		fmt.Println("--- core memory ---")
		if len(human) > 200 {
			human = human[:200]
		}
		if len(persona) > 100 {
			persona = persona[:100]
		}
		fmt.Printf("human:   %s\n", human)
		fmt.Printf("persona: %s\n", persona)
	}

	// Limpiar
	eliminarAgente(agentID)
	fmt.Println("\nAgente eliminado.")
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
