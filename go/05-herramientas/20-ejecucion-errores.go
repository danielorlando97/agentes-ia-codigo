// Ejecución y manejo de errores en tool calling.
//
// El 20-40% de tool calls en producción encuentran algún tipo de error.
// Este ejecutor distingue entre errores transitorios (retry con backoff)
// y errores determinísticos (fail fast), y devuelve errores formativos
// al modelo para que pueda autocorregir su llamada.
//
// El agent loop tiene cinco stop_reason posibles:
// end_turn, tool_use, max_tokens, pause_turn, refusal.

// Cómo ejecutar: make go FILE=go/05-herramientas/20-ejecucion-errores.go

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"time"
)

const (
	maxIterations   = 20
)

var (
	modelEjecucion = envOr("MODEL", "claude-sonnet-4-6")
	ejecucionAPIURL = envBaseURL()
)

// --- Tipos de error ---

type toolNotFoundErr struct{ Name string }
type toolTimeoutErr struct{ Name string; Ms int }
type authErr struct{ Resource string }
type rateLimitErr struct{ RetryAfterMs int }

func (e *toolNotFoundErr) Error() string { return fmt.Sprintf("herramienta '%s' no registrada", e.Name) }
func (e *toolTimeoutErr) Error() string  { return fmt.Sprintf("%s no completo en %dms", e.Name, e.Ms) }
func (e *authErr) Error() string         { return fmt.Sprintf("sin permisos para acceder a %s", e.Resource) }
func (e *rateLimitErr) Error() string    { return fmt.Sprintf("rate limit: reintenta en %dms", e.RetryAfterMs) }

// --- Herramientas mock ---

type toolFn func(args map[string]interface{}) (string, error)

func toolFetchData(args map[string]interface{}) (string, error) {
	source, _ := args["source"].(string)
	switch source {
	case "restricted":
		return "", &authErr{Resource: source}
	case "slow":
		time.Sleep(600 * time.Millisecond) // excederá el timeout de 500ms
		return "datos muy tardíos", nil
	}
	data, _ := json.Marshal(map[string]interface{}{
		"data": fmt.Sprintf("datos de %s", source),
		"rows": 42,
	})
	return string(data), nil
}

func toolCalculate(args map[string]interface{}) (string, error) {
	var a, b float64
	fmt.Sscanf(fmt.Sprint(args["a"]), "%f", &a)
	fmt.Sscanf(fmt.Sprint(args["b"]), "%f", &b)
	op, _ := args["op"].(string)
	switch op {
	case "add":
		return fmt.Sprintf("%g", a+b), nil
	case "mul":
		return fmt.Sprintf("%g", a*b), nil
	case "sub":
		return fmt.Sprintf("%g", a-b), nil
	case "div":
		if b == 0 {
			return "", fmt.Errorf("division por cero")
		}
		return fmt.Sprintf("%g", a/b), nil
	}
	return fmt.Sprintf("%g op %s %g", a, op, b), nil
}

func toolSaveFile(args map[string]interface{}) (string, error) {
	path, _ := args["path"].(string)
	content, _ := args["content"].(string)
	if path == "" || content == "" {
		return "", fmt.Errorf("path y content son requeridos")
	}
	return fmt.Sprintf("Archivo guardado: %s (%d bytes)", path, len(content)), nil
}

var toolRegistry = map[string]toolFn{
	"fetch_data": toolFetchData,
	"calculate":  toolCalculate,
	"save_file":  toolSaveFile,
}

var toolsEjecucion = []map[string]interface{}{
	{
		"name":        "fetch_data",
		"description": "Obtiene datos de una fuente. source: 'database', 'api', 'cache', 'restricted' (sin permisos), 'slow' (timeout).",
		"input_schema": map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{"source": map[string]string{"type": "string"}},
			"required":   []string{"source"},
		},
	},
	{
		"name":        "calculate",
		"description": "Evalua a op b. op: add, sub, mul, div.",
		"input_schema": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"a":  map[string]string{"type": "number"},
				"b":  map[string]string{"type": "number"},
				"op": map[string]string{"type": "string"},
			},
			"required": []string{"a", "b", "op"},
		},
	},
	{
		"name":        "save_file",
		"description": "Guarda contenido en un archivo.",
		"input_schema": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"path":    map[string]string{"type": "string"},
				"content": map[string]string{"type": "string"},
			},
			"required": []string{"path", "content"},
		},
	},
}

// --- Retry con backoff exponencial ---

func conBackoff(fn func() (string, error), maxRetries int, baseDelayMs int) (string, error) {
	for intento := 0; intento < maxRetries; intento++ {
		result, err := fn()
		if err == nil {
			return result, nil
		}
		if intento == maxRetries-1 {
			return "", err
		}
		// Solo retry para errores transitorios
		switch err.(type) {
		case *authErr, *toolNotFoundErr:
			return "", err
		}
		delay := float64(baseDelayMs) * float64(int(1)<<intento)
		jitter := delay * 0.1 * (rand.Float64()*2 - 1)
		wait := time.Duration(delay+jitter) * time.Millisecond
		fmt.Printf("    [backoff] intento %d/%d fallo, esperando %v\n", intento+1, maxRetries, wait.Round(time.Millisecond))
		time.Sleep(wait)
	}
	return "", fmt.Errorf("no deberia llegar aqui")
}

// --- Ejecutar con timeout ---

func conTimeout(fn toolFn, args map[string]interface{}, timeoutMs int, toolName string) (string, error) {
	done := make(chan struct{ result string; err error }, 1)
	go func() {
		r, e := fn(args)
		done <- struct{ result string; err error }{r, e}
	}()
	select {
	case res := <-done:
		return res.result, res.err
	case <-time.After(time.Duration(timeoutMs) * time.Millisecond):
		return "", &toolTimeoutErr{Name: toolName, Ms: timeoutMs}
	}
}

// --- Error formativo ---

func construirErrorFormativo(toolName string, err error, input map[string]interface{}) string {
	switch e := err.(type) {
	case *toolNotFoundErr:
		tools := make([]string, 0, len(toolRegistry))
		for k := range toolRegistry {
			tools = append(tools, k)
		}
		return fmt.Sprintf("Herramienta '%s' no existe. Herramientas disponibles: %v.", e.Name, tools)

	case *toolTimeoutErr:
		inputJSON, _ := json.Marshal(input)
		return fmt.Sprintf("%s no completo en %dms con input %s. Intenta con un scope mas pequeno.", e.Name, e.Ms, inputJSON)

	case *authErr:
		return fmt.Sprintf("Sin permisos para acceder a '%s'. No reintentes — usa una fuente diferente.", e.Resource)

	case *rateLimitErr:
		return fmt.Sprintf("Rate limit excedido. Reintenta en %dms.", e.RetryAfterMs)
	}

	return fmt.Sprintf("%T en %s: %v", err, toolName, err)
}

// --- Dispatcher ---

type toolResultEjecucion struct {
	Type      string `json:"type"`
	ToolUseID string `json:"tool_use_id"`
	Content   string `json:"content"`
	IsError   bool   `json:"is_error,omitempty"`
}

func despacharTool(toolName string, input json.RawMessage) toolResultEjecucion {
	var args map[string]interface{}
	_ = json.Unmarshal(input, &args)

	fn, ok := toolRegistry[toolName]
	if !ok {
		err := &toolNotFoundErr{Name: toolName}
		return toolResultEjecucion{
			Type:    "tool_result",
			Content: construirErrorFormativo(toolName, err, args),
			IsError: true,
		}
	}

	// Timeout de 500ms para herramientas "slow"
	result, err := conBackoff(func() (string, error) {
		return conTimeout(fn, args, 500, toolName)
	}, 2, 100)

	if err != nil {
		return toolResultEjecucion{
			Type:    "tool_result",
			Content: construirErrorFormativo(toolName, err, args),
			IsError: true,
		}
	}

	return toolResultEjecucion{
		Type:    "tool_result",
		Content: result,
	}
}

// --- Tipos para la API ---

type msgEjecucion struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type blockEjecucion struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   string          `json:"content,omitempty"`
}

type apiRespEjecucion struct {
	Content    []blockEjecucion `json:"content"`
	StopReason string           `json:"stop_reason"`
}

func callAnthropicEjecucion(payload map[string]interface{}) (*apiRespEjecucion, error) {
	body, _ := json.Marshal(payload)
	req, _ := http.NewRequestWithContext(context.Background(), "POST", ejecucionAPIURL, bytes.NewReader(body))
	req.Header.Set("x-api-key", os.Getenv("ANTHROPIC_API_KEY"))
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("content-type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	var r apiRespEjecucion
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("parse: %s — %w", string(data), err)
	}
	return &r, nil
}

// --- Agent loop con manejo de stop_reason ---

func agentLoopEjecucion(tarea string) string {
	taskJSON, _ := json.Marshal(tarea)
	messages := []msgEjecucion{{Role: "user", Content: taskJSON}}

	for iter := 0; iter < maxIterations; iter++ {
		resp, err := callAnthropicEjecucion(map[string]interface{}{
			"model":      modelEjecucion,
			"max_tokens": 4096,
			"tools":      toolsEjecucion,
			"messages":   messages,
		})
		if err != nil {
			return err.Error()
		}

		fmt.Printf("\n[iter=%d] stop_reason=%s\n", iter+1, resp.StopReason)

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
			var results []toolResultEjecucion
			for _, b := range resp.Content {
				if b.Type != "tool_use" {
					continue
				}
				fmt.Printf("  → %s(%s)\n", b.Name, string(b.Input))
				res := despacharTool(b.Name, b.Input)
				res.ToolUseID = b.ID

				status := "OK"
				if res.IsError {
					status = "ERROR"
				}
				preview := res.Content
				if len(preview) > 100 {
					preview = preview[:100]
				}
				fmt.Printf("  ← [%s] %s\n", status, preview)
				results = append(results, res)
			}

			// CRÍTICO: todos los tool_results en un único mensaje user
			asstJSON, _ := json.Marshal(resp.Content)
			userJSON, _ := json.Marshal(results)
			messages = append(messages,
				msgEjecucion{Role: "assistant", Content: asstJSON},
				msgEjecucion{Role: "user", Content: userJSON},
			)

		case "max_tokens":
			// Verificar si el último bloque es un tool_use truncado
			if len(resp.Content) > 0 {
				last := resp.Content[len(resp.Content)-1]
				if last.Type == "tool_use" {
					fmt.Println("  [warn] tool_use block truncado por max_tokens")
				}
			}
			return "[respuesta truncada — max_tokens alcanzado]"

		case "pause_turn":
			// Continuar sin añadir mensajes nuevos
			fmt.Println("  [pause_turn] continuando...")
			asstJSON, _ := json.Marshal(resp.Content)
			messages = append(messages, msgEjecucion{Role: "assistant", Content: asstJSON})

		default:
			return fmt.Sprintf("[stop_reason inesperado: %s]", resp.StopReason)
		}
	}

	return "[max iteraciones alcanzadas]"
}

func main() {
	fmt.Println("=== Ejecucion y manejo de errores en tool calling ===\n")

	tarea := "Necesito: 1) obtener datos de 'database', " +
		"2) intentar obtener datos de 'restricted' (esto fallara), " +
		"3) calcula 15 mas 3 (usa op=add, a=15, b=3), " +
		"4) guarda el resultado en /tmp/resultado.txt. " +
		"Si algo falla, describelo en tu respuesta final."

	fmt.Printf("Tarea: %s\n", tarea)

	resultado := agentLoopEjecucion(tarea)
	fmt.Printf("\n=== Respuesta final ===\n%s\n", resultado)
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
