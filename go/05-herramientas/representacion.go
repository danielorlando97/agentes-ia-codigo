// Representación de herramientas: descripción, input_schema y errores de selección.
//
// Una herramienta es un contrato textual: nombre, descripción en lenguaje natural,
// y JSON Schema de parámetros. La calidad de ese contrato determina si el modelo
// elige la herramienta correcta (selección) y si genera los argumentos válidos
// (parametrización). IAC (Insufficient API Calls) — el modelo no invoca la tool
// cuando debería — es el error más frecuente, causado por descripciones pobres.

// Cómo ejecutar: make go FILE=go/05-herramientas/representacion.go

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

var (
	modelRepr = envOr("SMALL_MODEL", "claude-haiku-4-5-20251001")
	reprAPIURL = envBaseURL()
)

// --- Tipos para la API ---

type reprProperty struct {
	Type        string   `json:"type"`
	Description string   `json:"description,omitempty"`
	Enum        []string `json:"enum,omitempty"`
	Minimum     *int     `json:"minimum,omitempty"`
	Maximum     *int     `json:"maximum,omitempty"`
}

type reprInputSchema struct {
	Type                 string                  `json:"type"`
	Properties           map[string]reprProperty `json:"properties"`
	Required             []string                `json:"required"`
	AdditionalProperties *bool                   `json:"additionalProperties,omitempty"`
}

type reprTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema reprInputSchema `json:"input_schema"`
}

type reprMessage struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"`
}

type reprToolResultContent struct {
	Type      string `json:"type"`
	ToolUseID string `json:"tool_use_id"`
	Content   string `json:"content"`
	IsError   bool   `json:"is_error"`
}

type reprAPIRequest struct {
	Model     string        `json:"model"`
	MaxTokens int           `json:"max_tokens"`
	Tools     []reprTool    `json:"tools"`
	Messages  []reprMessage `json:"messages"`
}

type reprContentBlock struct {
	Type  string          `json:"type"`
	Text  string          `json:"text,omitempty"`
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
}

type reprAPIResponse struct {
	Content    []reprContentBlock `json:"content"`
	StopReason string             `json:"stop_reason"`
}

// --- Definiciones de herramientas ---

func boolPtr(b bool) *bool { return &b }
func intPtr(i int) *int    { return &i }

// Herramienta con descripción pobre — solo describe el mecanismo.
// Causa IAC: el modelo responde desde memoria en lugar de invocarla.
var toolMala = reprTool{
	Name:        "get_account_info",
	Description: "Gets account information from the database.",
	InputSchema: reprInputSchema{
		Type: "object",
		Properties: map[string]reprProperty{
			"id": {Type: "string"},
		},
		Required: []string{"id"},
	},
}

// Herramienta con descripción efectiva — incluye cuándo usarla, qué no hace,
// y qué campos devuelve. Resuelve la ambigüedad de selección.
var toolBuena = reprTool{
	Name: "get_account_info",
	Description: "Retrieves complete account information for a customer. " +
		"Use this when the user asks about their account status, balance, " +
		"subscription plan, or any account-specific detail. " +
		"Do NOT use this for order-specific questions — use get_order_info instead. " +
		"Returns: account_id, email, subscription_plan, account_balance, created_at.",
	InputSchema: reprInputSchema{
		Type: "object",
		Properties: map[string]reprProperty{
			"account_id": {
				Type: "string",
				Description: "Customer account ID (format: ACC-XXXXXX). " +
					"If not provided, uses the ID from the current conversation context.",
			},
		},
		Required:             []string{}, // Opcional: el modelo puede inferirlo
		AdditionalProperties: boolPtr(false),
	},
}

// Herramienta con schema bien documentado para formatos no obvios.
var toolBusqueda = reprTool{
	Name: "search_orders",
	Description: "Searches orders within a date range and optional status filter. " +
		"Use when the user asks to find, list, or review orders. " +
		"Do NOT use for a single known order ID — use get_order_info instead.",
	InputSchema: reprInputSchema{
		Type: "object",
		Properties: map[string]reprProperty{
			"date_range": {
				Type: "string",
				Description: "Date range in ISO 8601 format: 'YYYY-MM-DD/YYYY-MM-DD'. " +
					"Example: '2024-01-01/2024-03-31'",
			},
			"status": {
				Type:        "string",
				Enum:        []string{"active", "inactive", "pending"},
				Description: "Account status filter. Use 'active' for currently subscribed accounts.",
			},
			"limit": {
				Type:        "integer",
				Minimum:     intPtr(1),
				Maximum:     intPtr(100),
				Description: "Maximum number of results. Default is 20. Use higher values only for exports.",
			},
		},
		Required:             []string{"date_range"},
		AdditionalProperties: boolPtr(false),
	},
}

// --- Mock de herramientas ---

func mockGetAccountInfo(input json.RawMessage) string {
	var args map[string]string
	_ = json.Unmarshal(input, &args)

	accountID, ok := args["account_id"]
	if !ok {
		accountID = args["id"]
	}
	if accountID == "" {
		accountID = "ACC-000000"
	}

	result := map[string]interface{}{
		"account_id":        accountID,
		"email":             "usuario@ejemplo.com",
		"subscription_plan": "Pro",
		"account_balance":   42.50,
		"created_at":        "2023-05-15",
	}
	b, _ := json.Marshal(result)
	return string(b)
}

// --- Cliente HTTP para la API de Anthropic ---

func callAnthropicRepr(req reprAPIRequest) (*reprAPIResponse, error) {
	body, _ := json.Marshal(req)
	httpReq, _ := http.NewRequestWithContext(context.Background(), "POST", reprAPIURL, bytes.NewReader(body))
	httpReq.Header.Set("x-api-key", os.Getenv("ANTHROPIC_API_KEY"))
	httpReq.Header.Set("anthropic-version", "2023-06-01")
	httpReq.Header.Set("content-type", "application/json")

	c := &http.Client{Timeout: 60 * time.Second}
	resp, err := c.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)
	var r reprAPIResponse
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("parse: %s — %w", string(data), err)
	}
	return &r, nil
}

// --- Demo: diferencia entre descripción mala y buena ---

func demoDescripcion(descripcion string, tool reprTool, pregunta string) {
	fmt.Printf("\n%s\n", strings.Repeat("=", 60))
	fmt.Printf("[%s]\n", descripcion)
	fmt.Printf("Pregunta: %s\n", pregunta)
	descPreview := tool.Description
	if len(descPreview) > 80 {
		descPreview = descPreview[:80]
	}
	fmt.Printf("Descripción de la herramienta: \"%s...\"\n", descPreview)

	resp, err := callAnthropicRepr(reprAPIRequest{
		Model:     modelRepr,
		MaxTokens: 512,
		Tools:     []reprTool{tool},
		Messages: []reprMessage{
			{Role: "user", Content: pregunta},
		},
	})
	if err != nil {
		fmt.Printf("  Error: %v\n", err)
		return
	}

	if resp.StopReason == "tool_use" {
		// Encontrar el bloque tool_use
		var toolBlock *reprContentBlock
		for i := range resp.Content {
			if resp.Content[i].Type == "tool_use" {
				toolBlock = &resp.Content[i]
				break
			}
		}
		if toolBlock == nil {
			fmt.Println("  [error] stop_reason=tool_use pero no hay bloque tool_use")
			return
		}

		fmt.Printf("\n  → El modelo invocó '%s'\n", toolBlock.Name)
		fmt.Printf("    input: %s\n", string(toolBlock.Input))

		// Devolver el resultado y obtener respuesta final
		toolResult := mockGetAccountInfo(toolBlock.Input)

		toolResultContent := reprToolResultContent{
			Type:      "tool_result",
			ToolUseID: toolBlock.ID,
			Content:   toolResult,
			IsError:   false,
		}

		// El assistant turn completo (incluyendo el bloque tool_use)
		final, err := callAnthropicRepr(reprAPIRequest{
			Model:     modelRepr,
			MaxTokens: 256,
			Tools:     []reprTool{tool},
			Messages: []reprMessage{
				{Role: "user", Content: pregunta},
				{Role: "assistant", Content: resp.Content},
				{Role: "user", Content: []reprToolResultContent{toolResultContent}},
			},
		})
		if err != nil {
			fmt.Printf("  Error en respuesta final: %v\n", err)
			return
		}

		for _, b := range final.Content {
			if b.Type == "text" {
				preview := b.Text
				if len(preview) > 120 {
					preview = preview[:120]
				}
				fmt.Printf("  ← Respuesta final: %s\n", preview)
			}
		}
	} else {
		// IAC: el modelo respondió sin llamar la herramienta
		fmt.Println("\n  [IAC] El modelo respondió desde memoria sin invocar la herramienta.")
		for _, b := range resp.Content {
			if b.Type == "text" {
				preview := b.Text
				if len(preview) > 120 {
					preview = preview[:120]
				}
				fmt.Printf("  Respuesta: %s\n", preview)
			}
		}
	}
}

func demoSchemaDetallado() {
	pregunta := "Muéstrame los pedidos pendientes de los últimos 3 meses, máximo 50."
	fmt.Printf("\n%s\n", strings.Repeat("=", 60))
	fmt.Println("[Schema con descripciones de parámetros]")
	fmt.Printf("Pregunta: %s\n", pregunta)

	resp, err := callAnthropicRepr(reprAPIRequest{
		Model:     modelRepr,
		MaxTokens: 512,
		Tools:     []reprTool{toolBusqueda},
		Messages: []reprMessage{
			{Role: "user", Content: pregunta},
		},
	})
	if err != nil {
		fmt.Printf("  Error: %v\n", err)
		return
	}

	if resp.StopReason == "tool_use" {
		for _, b := range resp.Content {
			if b.Type == "tool_use" {
				var pretty bytes.Buffer
				_ = json.Indent(&pretty, b.Input, "    ", "  ")
				fmt.Printf("\n  → El modelo invocó '%s'\n", b.Name)
				fmt.Printf("    input: %s\n", pretty.String())
				fmt.Println("\n  Notar: date_range en ISO 8601, status y limit correctamente inferidos.")
			}
		}
	} else {
		for _, b := range resp.Content {
			if b.Type == "text" {
				preview := b.Text
				if len(preview) > 120 {
					preview = preview[:120]
				}
				fmt.Printf("\n  Respuesta directa: %s\n", preview)
			}
		}
	}
}

func main() {
	fmt.Println("=== Representación de herramientas: descripción y schema ===")
	fmt.Println()
	fmt.Println("Principios clave:")
	fmt.Println("  - Descripción efectiva: CUÁNDO usar la tool + QUÉ hace + QUÉ NO hace")
	fmt.Println("  - Schema: descripción de parámetros con ejemplos para formatos no obvios")
	fmt.Println("  - additionalProperties: false equivale a strict (Anthropic aplica")
	fmt.Println("    constrained decoding sobre input_schema por defecto, sin flag opt-in)")
	fmt.Println()
	fmt.Println("Tipos de error:")
	fmt.Println("  - IAC (Insufficient API Calls): el modelo no llama la tool cuando debería")
	fmt.Println("    Causa: descripción que solo describe el mecanismo ('Gets X from DB')")
	fmt.Println("  - Llamada incorrecta: el modelo invoca la tool equivocada")
	fmt.Println("    Causa: falta de diferenciación entre tools similares")

	// Caso 1: descripción pobre → potencial IAC
	demoDescripcion(
		"Descripción POBRE — solo describe el mecanismo",
		toolMala,
		"¿Puedes verificar mi cuenta? Mi ID es ACC-123456.",
	)

	// Caso 2: descripción efectiva → selección correcta
	demoDescripcion(
		"Descripción EFECTIVA — incluye cuándo usar, qué no usar, qué devuelve",
		toolBuena,
		"¿Puedes verificar mi cuenta? Mi ID es ACC-123456.",
	)

	// Caso 3: schema con parámetros documentados
	demoSchemaDetallado()

	fmt.Printf("\n%s\n", strings.Repeat("=", 60))
	fmt.Println("Nota sobre strict / constrained decoding:")
	fmt.Println("  OpenAI: campo 'strict: true' en la function — incompatible con parallel_tool_calls")
	fmt.Println("  Anthropic: constrained decoding siempre activo sobre input_schema")
	fmt.Println("             sin flag, compatible con parallel tool calls")
	fmt.Println("  En ambos casos: reduce fallo de formato de 2-5% a <0.1%")
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
