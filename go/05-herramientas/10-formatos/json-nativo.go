// Tool calling con JSON nativo (Anthropic).
//
// El formato Anthropic serializa la llamada como un bloque tool_use con
// input como objeto ya parseado (no string JSON). El resultado vuelve
// como tool_result en un mensaje de role "user".
//
// Diferencias clave vs OpenAI Chat Completions:
//
//	Anthropic: stop_reason="tool_use", input=objeto, role="user", is_error
//	OpenAI:    finish_reason="tool_calls", arguments=string, role="tool", sin is_error

// Cómo ejecutar: make go FILE=go/05-herramientas/10-formatos/json-nativo.go

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

var modelJSON = envOr("SMALL_MODEL", "claude-haiku-4-5-20251001")

// --- Tipos ---

type JNTool struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema interface{} `json:"input_schema"`
}

type JNMsg struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"`
}

type JNBlock struct {
	Type  string          `json:"type"`
	Text  string          `json:"text,omitempty"`
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
}

type JNToolResult struct {
	Type      string `json:"type"`
	ToolUseID string `json:"tool_use_id"`
	Content   string `json:"content"`
	IsError   bool   `json:"is_error"`
}

type JNResp struct {
	Content    []JNBlock `json:"content"`
	StopReason string    `json:"stop_reason"`
}

// --- Definición de tools ---

var herramientasJSON = []JNTool{
	{
		Name: "get_weather",
		Description: "Get current weather for a city. " +
			"Use when the user asks about weather conditions, temperature, or forecast. " +
			"Do NOT use for historical weather — use get_weather_history instead.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"location": map[string]interface{}{
					"type":        "string",
					"description": "City and country, e.g. 'Madrid, Spain'",
				},
				"unit": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"celsius", "fahrenheit"},
					"description": "Temperature unit. Default: celsius.",
				},
			},
			"required":             []string{"location"},
			"additionalProperties": false,
		},
	},
	{
		Name:        "get_time",
		Description: "Get current local time for a timezone or city.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"timezone": map[string]interface{}{
					"type":        "string",
					"description": "IANA timezone string, e.g. 'Europe/Madrid'",
				},
			},
			"required":             []string{"timezone"},
			"additionalProperties": false,
		},
	},
}

// --- Mock de ejecución ---

func ejecutarHerramientaJSON(nombre string, inputRaw json.RawMessage) string {
	var args map[string]interface{}
	json.Unmarshal(inputRaw, &args)

	switch nombre {
	case "get_weather":
		unit := "celsius"
		if u, ok := args["unit"].(string); ok {
			unit = u
		}
		result, _ := json.Marshal(map[string]interface{}{
			"location":   args["location"],
			"temperature": 22,
			"unit":       unit,
			"conditions": "parcialmente nublado",
		})
		return string(result)
	case "get_time":
		result, _ := json.Marshal(map[string]interface{}{
			"timezone":   args["timezone"],
			"local_time": "14:35:00",
		})
		return string(result)
	}
	return fmt.Sprintf(`{"error": "herramienta desconocida: %s"}`, nombre)
}

// --- Llamada API ---

func llamarAnthropicJSON(msgs []JNMsg) (*JNResp, error) {
	body, _ := json.Marshal(map[string]interface{}{
		"model":      modelJSON,
		"max_tokens": 1024,
		"tools":      herramientasJSON,
		"messages":   msgs,
	})
	req, _ := http.NewRequest("POST", envBaseURL(), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", os.Getenv("ANTHROPIC_API_KEY"))
	req.Header.Set("anthropic-version", "2023-06-01")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, respBody)
	}
	var result JNResp
	json.Unmarshal(respBody, &result)
	return &result, nil
}

// --- Loop de tool use ---

func toolUseLoop(pregunta string) (string, error) {
	mensajes := []JNMsg{{Role: "user", Content: pregunta}}

	for paso := 0; paso < 10; paso++ {
		resp, err := llamarAnthropicJSON(mensajes)
		if err != nil {
			return "", err
		}

		if resp.StopReason == "end_turn" {
			var sb strings.Builder
			for _, b := range resp.Content {
				if b.Type == "text" {
					sb.WriteString(b.Text)
				}
			}
			return sb.String(), nil
		}

		if resp.StopReason == "tool_use" {
			// Añadir respuesta del asistente (texto + tool_use blocks)
			mensajes = append(mensajes, JNMsg{Role: "assistant", Content: resp.Content})

			// Ejecutar todas las tool calls del turno (pueden ser paralelas)
			var resultados []JNToolResult
			for _, b := range resp.Content {
				if b.Type == "tool_use" {
					// input es un objeto JSON ya parseado como RawMessage
					resultado := ejecutarHerramientaJSON(b.Name, b.Input)
					display := resultado
					if len(display) > 60 {
						display = display[:60]
					}
					fmt.Printf("  → %s(%s) = %s\n", b.Name, string(b.Input), display)
					resultados = append(resultados, JNToolResult{
						Type:      "tool_result",
						ToolUseID: b.ID,  // mismo ID del tool_use block
						Content:   resultado,
						IsError:   false, // campo exclusivo de Anthropic
					})
				}
			}

			// Todos los resultados en UN solo mensaje de role "user"
			mensajes = append(mensajes, JNMsg{Role: "user", Content: resultados})
		}
	}
	return "[límite de pasos alcanzado]", nil
}

func main() {
	fmt.Println("=== Tool calling JSON nativo (Anthropic) ===\n")

	// Caso 1: tool call simple
	fmt.Println("Pregunta: ¿Qué tiempo hace en Madrid?")
	r1, err := toolUseLoop("¿Qué tiempo hace en Madrid?")
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
	fmt.Printf("Respuesta: %s\n\n", r1)

	// Caso 2: parallel tool calls — el modelo genera múltiples bloques en un turno
	fmt.Println("Pregunta: ¿Qué tiempo y hora es en Tokio?")
	r2, err := toolUseLoop("¿Qué tiempo y hora es en Tokio ahora mismo?")
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
	fmt.Printf("Respuesta: %s\n", r2)
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
