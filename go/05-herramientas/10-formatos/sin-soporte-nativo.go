// Function calling sin soporte nativo — constrained via system prompt + retry.
//
// Cuando el modelo no tiene fine-tuning para tool calling, se describe
// el formato JSON esperado en el system prompt y se valida la respuesta.
// Si el JSON es inválido, se reintenta con el error acumulado en el prompt
// (máx 3 intentos).
//
// Tasa de fallo sin fine-tuning: 15-40%. Con retry x3 y 80% de accuracy/intento,
// la probabilidad de fallo total ≈ 0.8%.

// Cómo ejecutar: make go FILE=go/05-herramientas/10-formatos/sin-soporte-nativo.go

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
)

const (
	maxRetriesSin    = 3
)

var (
	modelSinSoporte = envOr("MODEL", "claude-sonnet-4-6")
	sinSoporteAPIURL = envBaseURL()
)

// --- System prompt con descripción del formato JSON ---

const toolsDescription = `Tienes acceso a las siguientes herramientas:

- search_database(query: string, limit?: number)
  Busca en la base de datos. limit debe ser entre 1 y 100.

- calculate(expression: string)
  Evalua una expresion matematica. Solo operadores +, -, *, /.

Para usar una herramienta, responde UNICAMENTE con JSON valido en este formato:
{
  "tool": "nombre_herramienta",
  "arguments": {
    "param1": "valor1",
    "param2": valor2
  }
}

Si no necesitas una herramienta, responde con texto normal.
NO incluyas texto adicional antes o despues del JSON cuando uses una herramienta.`

// --- Tipos ---

type toolCallSchema struct {
	Tool      string                 `json:"tool"`
	Arguments map[string]interface{} `json:"arguments"`
}

type validationError struct {
	Message string
	Hint    string
}

// --- Validación del JSON ---

// Extraer JSON de texto que puede incluir markdown code blocks
var codeBlockRegexp = regexp.MustCompile("```(?:json)?\\s*([\\s\\S]*?)\\s*```")
var jsonObjectRegexp = regexp.MustCompile(`(\{[\s\S]*\})`)

func extraerJSON(texto string) string {
	if m := codeBlockRegexp.FindStringSubmatch(texto); m != nil {
		return strings.TrimSpace(m[1])
	}
	if m := jsonObjectRegexp.FindStringSubmatch(texto); m != nil {
		return strings.TrimSpace(m[1])
	}
	return strings.TrimSpace(texto)
}

func validarToolCall(texto string) (*toolCallSchema, *validationError) {
	jsonStr := extraerJSON(texto)

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &parsed); err != nil {
		return nil, &validationError{
			Message: fmt.Sprintf("JSON invalido: %v", err),
			Hint:    "Asegurate de que la respuesta sea JSON puro sin texto adicional.",
		}
	}

	// Validar campo 'tool'
	toolVal, ok := parsed["tool"]
	if !ok {
		return nil, &validationError{
			Message: `Campo requerido faltante: "tool"`,
			Hint:    `El JSON debe tener un campo "tool" con el nombre de la herramienta.`,
		}
	}
	toolName, ok := toolVal.(string)
	if !ok {
		return nil, &validationError{
			Message: fmt.Sprintf(`Campo "tool" debe ser string, recibido: %T`, toolVal),
		}
	}

	validTools := map[string]bool{"search_database": true, "calculate": true}
	if !validTools[toolName] {
		tools := []string{"search_database", "calculate"}
		return nil, &validationError{
			Message: fmt.Sprintf("Herramienta desconocida: %q", toolName),
			Hint:    fmt.Sprintf("Herramientas disponibles: %s", strings.Join(tools, ", ")),
		}
	}

	// Validar campo 'arguments'
	argsVal, ok := parsed["arguments"]
	if !ok {
		return nil, &validationError{
			Message: `Campo requerido faltante: "arguments"`,
			Hint:    `El JSON debe tener un campo "arguments" con los parametros.`,
		}
	}
	args, ok := argsVal.(map[string]interface{})
	if !ok {
		return nil, &validationError{
			Message: `"arguments" debe ser un objeto.`,
		}
	}

	// Validaciones específicas por herramienta
	switch toolName {
	case "search_database":
		q, ok := args["query"]
		if !ok || q == nil {
			return nil, &validationError{
				Message: `search_database requiere "query" (string)`,
				Hint:    `Ejemplo: {"tool": "search_database", "arguments": {"query": "usuarios activos", "limit": 10}}`,
			}
		}
		if _, ok := q.(string); !ok {
			return nil, &validationError{
				Message: `"query" debe ser string`,
			}
		}
		if limitVal, exists := args["limit"]; exists {
			limit, ok := limitVal.(float64)
			if !ok || limit < 1 || limit > 100 {
				return nil, &validationError{
					Message: `"limit" debe ser un entero entre 1 y 100.`,
				}
			}
		}

	case "calculate":
		expr, ok := args["expression"]
		if !ok || expr == nil {
			return nil, &validationError{
				Message: `calculate requiere "expression" (string)`,
				Hint:    `Ejemplo: {"tool": "calculate", "arguments": {"expression": "15 * 8 + 3"}}`,
			}
		}
		if _, ok := expr.(string); !ok {
			return nil, &validationError{
				Message: `"expression" debe ser string`,
			}
		}
	}

	return &toolCallSchema{
		Tool:      toolName,
		Arguments: args,
	}, nil
}

// --- Herramientas mock ---

func ejecutarToolSin(call *toolCallSchema) string {
	switch call.Tool {
	case "search_database":
		query, _ := call.Arguments["query"].(string)
		limit := 10
		if l, ok := call.Arguments["limit"].(float64); ok {
			limit = int(l)
		}
		result := map[string]interface{}{
			"results": []map[string]interface{}{
				{"id": 1, "texto": fmt.Sprintf("Resultado 1 para %q", query)},
				{"id": 2, "texto": fmt.Sprintf("Resultado 2 para %q", query)},
			},
			"total": 2,
			"limit": limit,
		}
		data, _ := json.Marshal(result)
		return string(data)

	case "calculate":
		expr, _ := call.Arguments["expression"].(string)
		// Calculadora simple de demostración
		var a, b float64
		if n, _ := fmt.Sscanf(expr, "%f * %f", &a, &b); n == 2 {
			return fmt.Sprintf("%g", a*b)
		}
		if n, _ := fmt.Sscanf(expr, "%f + %f", &a, &b); n == 2 {
			return fmt.Sprintf("%g", a+b)
		}
		return fmt.Sprintf("resultado de %s", expr)
	}
	return "herramienta no encontrada"
}

// --- Cliente HTTP para Anthropic ---

type sinSoporteMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type sinSoporteRequest struct {
	Model     string              `json:"model"`
	MaxTokens int                 `json:"max_tokens"`
	System    string              `json:"system"`
	Messages  []sinSoporteMessage `json:"messages"`
}

type sinSoporteResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

func callAnthropicSin(req sinSoporteRequest) (string, error) {
	body, _ := json.Marshal(req)
	httpReq, _ := http.NewRequestWithContext(context.Background(), "POST", sinSoporteAPIURL, bytes.NewReader(body))
	httpReq.Header.Set("x-api-key", os.Getenv("ANTHROPIC_API_KEY"))
	httpReq.Header.Set("anthropic-version", "2023-06-01")
	httpReq.Header.Set("content-type", "application/json")

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)
	var r sinSoporteResponse
	if err := json.Unmarshal(data, &r); err != nil {
		return "", fmt.Errorf("parse: %s — %w", string(data), err)
	}
	out := ""
	for _, b := range r.Content {
		if b.Type == "text" {
			out += b.Text
		}
	}
	return out, nil
}

// --- Loop con retry ---

type retryResult struct {
	toolCall *toolCallSchema
	texto    string
	intentos int
}

func llamarConRetry(pregunta string) (*retryResult, error) {
	var mensajesError []string

	for intento := 1; intento <= maxRetriesSin; intento++ {
		// Construir el prompt acumulando errores previos
		userContent := pregunta
		if len(mensajesError) > 0 {
			userContent += "\n\n[ERRORES PREVIOS — corrige estos problemas en tu respuesta:]\n"
			for i, e := range mensajesError {
				userContent += fmt.Sprintf("Intento %d: %s\n", i+1, e)
			}
		}

		texto, err := callAnthropicSin(sinSoporteRequest{
			Model:     modelSinSoporte,
			MaxTokens: 512,
			System:    toolsDescription,
			Messages:  []sinSoporteMessage{{Role: "user", Content: userContent}},
		})
		if err != nil {
			return nil, err
		}

		preview := texto
		if len(preview) > 120 {
			preview = preview[:120] + "..."
		}
		fmt.Printf("  [intento %d] respuesta: %s\n", intento, preview)

		// Intentar validar como tool call
		call, valErr := validarToolCall(texto)
		if valErr == nil {
			return &retryResult{toolCall: call, intentos: intento}, nil
		}

		// Acumular el error
		errMsg := valErr.Message
		if valErr.Hint != "" {
			errMsg += " — " + valErr.Hint
		}
		mensajesError = append(mensajesError, errMsg)
		fmt.Printf("  [intento %d] error de validacion: %s\n", intento, errMsg)

		// En el último intento, si no hay JSON, tratar como respuesta normal
		if intento == maxRetriesSin && !strings.Contains(texto, "{") {
			return &retryResult{texto: texto, intentos: intento}, nil
		}
	}

	return nil, fmt.Errorf("no se obtuvo JSON valido tras %d intentos", maxRetriesSin)
}

func main() {
	fmt.Println("=== Function calling sin soporte nativo (system prompt + retry) ===\n")

	casos := []struct {
		descripcion string
		pregunta    string
	}{
		{
			descripcion: "Caso normal: debería generar JSON válido",
			pregunta:    "Busca los usuarios que se registraron en el ultimo mes, maximo 20 resultados.",
		},
		{
			descripcion: "Caso aritmetico: deberia usar calculate",
			pregunta:    "¿Cuanto es 1234 * 56?",
		},
	}

	for _, caso := range casos {
		fmt.Printf("\n--- %s ---\n", caso.descripcion)
		fmt.Printf("Pregunta: %s\n", caso.pregunta)

		resultado, err := llamarConRetry(caso.pregunta)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			continue
		}

		if resultado.toolCall != nil {
			fmt.Printf("\nTool call validada (%d intento/s):\n", resultado.intentos)
			data, _ := json.MarshalIndent(resultado.toolCall, "", "  ")
			fmt.Println(string(data))

			// Ejecutar la herramienta
			toolResult := ejecutarToolSin(resultado.toolCall)
			fmt.Printf("\nResultado de la herramienta: %s\n", toolResult)

			// Devolver el resultado al modelo para respuesta final
			respuestaFinal, err := callAnthropicSin(sinSoporteRequest{
				Model:     modelSinSoporte,
				MaxTokens: 512,
				System:    toolsDescription,
				Messages: []sinSoporteMessage{
					{Role: "user", Content: caso.pregunta},
					{Role: "assistant", Content: string(data)},
					{Role: "user", Content: "Resultado de la herramienta: " + toolResult},
				},
			})
			if err != nil {
				fmt.Printf("Error en respuesta final: %v\n", err)
			} else {
				fmt.Printf("\nRespuesta final: %s\n", respuestaFinal)
			}
		} else {
			fmt.Printf("\nRespuesta directa (sin tool call): %s\n", resultado.texto)
		}
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
