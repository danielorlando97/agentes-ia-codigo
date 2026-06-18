// Tool calling paralelo.
//
// El modelo puede generar múltiples bloques tool_use en un único turno.
// El ejecutor los corre concurrentemente con goroutines + sync.WaitGroup
// y devuelve todos los tool_results en un único mensaje user.
//
// Regla crítica: todos los tool_results deben ir en un único mensaje user.
// Si se envían en mensajes separados, el modelo aprende a serializar
// tool calls en turnos futuros porque así "ve" que trabaja el sistema.
//
// Requisito: go get github.com/anthropics/anthropic-sdk-go

// Cómo ejecutar: make go FILE=go/05-herramientas/22-paralelo.go

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"
)

var (
	modelParalelo = envOr("MODEL", "claude-sonnet-4-6")
	apiURL = envBaseURL()
)

// --- Tipos para la API de Anthropic ---

type toolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type msgParalelo struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type blockParalelo struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   string          `json:"content,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
}

type apiRespParalelo struct {
	Content    []blockParalelo `json:"content"`
	StopReason string          `json:"stop_reason"`
}

var toolsParalelo = []toolDef{
	{
		Name:        "get_weather",
		Description: "Obtiene el clima actual de una ciudad.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"city": {"type": "string", "description": "Nombre de la ciudad"}
			},
			"required": ["city"]
		}`),
	},
	{
		Name:        "calculate",
		Description: "Evalúa una expresión matemática y devuelve el resultado.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"a": {"type": "number"},
				"b": {"type": "number"},
				"op": {"type": "string", "description": "Operación: add, sub, mul, div"}
			},
			"required": ["a", "b", "op"]
		}`),
	},
	{
		Name:        "search",
		Description: "Busca información sobre un tema.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"query": {"type": "string", "description": "Término de búsqueda"}
			},
			"required": ["query"]
		}`),
	},
}

// --- Herramientas mock ---

func mockWeatherParalelo(city string) string {
	time.Sleep(300 * time.Millisecond) // simular latencia de API
	table := map[string]string{
		"Madrid":  `{"city":"Madrid","temp_c":24,"condition":"sunny"}`,
		"Paris":   `{"city":"Paris","temp_c":18,"condition":"cloudy"}`,
		"Tokyo":   `{"city":"Tokyo","temp_c":29,"condition":"humid"}`,
		"default": `{"city":"unknown","temp_c":20,"condition":"unknown"}`,
	}
	if r, ok := table[city]; ok {
		return r
	}
	return table["default"]
}

func mockCalculateParalelo(a, b float64, op string) string {
	time.Sleep(50 * time.Millisecond)
	switch op {
	case "add":
		return fmt.Sprintf("%v", a+b)
	case "sub":
		return fmt.Sprintf("%v", a-b)
	case "mul":
		return fmt.Sprintf("%v", a*b)
	case "div":
		if b == 0 {
			return "Error: división por cero"
		}
		return fmt.Sprintf("%v", a/b)
	}
	return fmt.Sprintf("Error: operación '%s' desconocida", op)
}

func mockSearchParalelo(query string) string {
	time.Sleep(400 * time.Millisecond) // simular búsqueda
	return fmt.Sprintf(`{"query":%q,"hits":["Resultado 1 para %s","Resultado 2 para %s"]}`,
		query, query, query)
}

func ejecutarToolParalelo(name string, input json.RawMessage) string {
	var args map[string]interface{}
	_ = json.Unmarshal(input, &args)

	switch name {
	case "get_weather":
		city, _ := args["city"].(string)
		return mockWeatherParalelo(city)
	case "calculate":
		a, _ := args["a"].(float64)
		b, _ := args["b"].(float64)
		op, _ := args["op"].(string)
		return mockCalculateParalelo(a, b, op)
	case "search":
		query, _ := args["query"].(string)
		return mockSearchParalelo(query)
	}
	return fmt.Sprintf(`{"error":"herramienta '%s' desconocida"}`, name)
}

// --- Ejecutor paralelo con goroutines ---

type toolResultParalelo struct {
	Type      string `json:"type"`
	ToolUseID string `json:"tool_use_id"`
	Content   string `json:"content"`
	IsError   bool   `json:"is_error,omitempty"`
}

// ejecutarToolsParalelas corre todos los bloques tool_use concurrentemente.
// Devuelve todos los resultados listos para incluir en un único mensaje user.
func ejecutarToolsParalelas(bloques []blockParalelo) []toolResultParalelo {
	resultados := make([]toolResultParalelo, len(bloques))
	t0 := time.Now()

	var wg sync.WaitGroup
	for i, bloque := range bloques {
		wg.Add(1)
		go func(idx int, b blockParalelo) {
			defer wg.Done()
			var content string
			var isErr bool
			func() {
				defer func() {
					if r := recover(); r != nil {
						content = fmt.Sprintf("panic en %s: %v", b.Name, r)
						isErr = true
					}
				}()
				content = ejecutarToolParalelo(b.Name, b.Input)
			}()
			resultados[idx] = toolResultParalelo{
				Type:      "tool_result",
				ToolUseID: b.ID,
				Content:   content,
				IsError:   isErr,
			}
		}(i, bloque)
	}

	wg.Wait()
	fmt.Printf("  [paralelo] %d tools → %dms (max individual, no suma)\n",
		len(bloques), time.Since(t0).Milliseconds())
	return resultados
}

// --- Cliente HTTP para la API de Anthropic ---

func callAnthropicParalelo(payload map[string]interface{}) (*apiRespParalelo, error) {
	body, _ := json.Marshal(payload)
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
	var r apiRespParalelo
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("parse error: %s — %w", string(data), err)
	}
	return &r, nil
}

// --- Agent loop ---

func agentLoopParalelo(tarea string) string {
	taskJSON, _ := json.Marshal(tarea)
	messages := []msgParalelo{{Role: "user", Content: taskJSON}}

	for iter := 0; iter < 10; iter++ {
		resp, err := callAnthropicParalelo(map[string]interface{}{
			"model":      modelParalelo,
			"max_tokens": 4096,
			"tools":      toolsParalelo,
			"messages":   messages,
		})
		if err != nil {
			return err.Error()
		}

		// Contar tool_use blocks
		toolUseCount := 0
		for _, b := range resp.Content {
			if b.Type == "tool_use" {
				toolUseCount++
			}
		}
		fmt.Printf("  [iter=%d] stop_reason=%s, tool_calls=%d\n",
			iter+1, resp.StopReason, toolUseCount)

		switch resp.StopReason {
		case "end_turn":
			out := ""
			for _, b := range resp.Content {
				if b.Type == "text" {
					out += b.Text
				}
			}
			return out

		case "tool_use":
			// Recopilar bloques tool_use
			var toolUseBlocks []blockParalelo
			for _, b := range resp.Content {
				if b.Type == "tool_use" {
					toolUseBlocks = append(toolUseBlocks, b)
				}
			}

			// Ejecutar todos en paralelo con goroutines
			resultados := ejecutarToolsParalelas(toolUseBlocks)

			// Añadir respuesta del asistente
			asstJSON, _ := json.Marshal(resp.Content)
			messages = append(messages, msgParalelo{Role: "assistant", Content: asstJSON})

			// CORRECTO: todos los tool_results en UN solo mensaje user
			userJSON, _ := json.Marshal(resultados)
			messages = append(messages, msgParalelo{Role: "user", Content: userJSON})

		default:
			return fmt.Sprintf("[stop_reason inesperado: %s]", resp.StopReason)
		}
	}

	return "[max iteraciones]"
}

func main() {
	fmt.Println("=== Tool calling paralelo (goroutines) ===\n")

	// Esta tarea debería generar múltiples tool_use blocks en un turno:
	// clima de dos ciudades + cálculo + búsqueda — todos independientes
	tarea := "Necesito: 1) el clima actual de Madrid y Paris, " +
		"2) multiplica 1234 por 56 (usa op=mul, a=1234, b=56), y " +
		"3) busca 'parallel tool calling LLM'. " +
		"Puedes hacer todas estas consultas a la vez."

	fmt.Printf("Tarea: %s\n\n", tarea)

	resultado := agentLoopParalelo(tarea)
	fmt.Printf("\nRespuesta del modelo:\n%s\n", resultado)
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
