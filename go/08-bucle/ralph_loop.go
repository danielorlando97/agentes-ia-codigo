// Ralph Loop — patrón de Claude Code (arXiv:2604.14228).
//
// Sin límite explícito de iteraciones; la condición de salida es semántica
// (el modelo responde sin tool_use). Características propias del patrón:
//   - compactarCascada: 4 capas de reducción de contexto ordenadas por coste
//   - ejecutarConPermisos: 5 niveles de autorización para tool use
//   - diminishingReturnsCheck: detiene el loop si varias iters no producen output útil
//
// Requiere: ANTHROPIC_API_KEY en el entorno.

// Cómo ejecutar: make go FILE=go/08-bucle/ralph_loop.go

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

const (
	compactionThreshold        = 0.80
	historyBudget              = 80_000
	diminishingReturnsTokens   = 100
	diminishingReturnsIters    = 3
)

var (
	ralphModel = envOr("MODEL", "claude-sonnet-4-6")
)

// --- Permission levels ---

type permissionLevel int

const (
	readOnly       permissionLevel = 1
	workspaceRead  permissionLevel = 2
	workspaceWrite permissionLevel = 3
	networkAccess  permissionLevel = 4
	dangerFull     permissionLevel = 5
)

func (p permissionLevel) String() string {
	switch p {
	case readOnly:
		return "READ_ONLY"
	case workspaceRead:
		return "WORKSPACE_READ"
	case workspaceWrite:
		return "WORKSPACE_WRITE"
	case networkAccess:
		return "NETWORK_ACCESS"
	case dangerFull:
		return "DANGER_FULL"
	}
	return "UNKNOWN"
}

// --- Tool spec ---

type ralphToolFn func(args map[string]interface{}) string

type ralphToolSpec struct {
	Name        string
	Description string
	InputSchema map[string]interface{}
	Permission  permissionLevel
	Fn          ralphToolFn
}

// --- API types ---

type ralphMsg struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"`
}

type ralphAPITool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"input_schema"`
}

type ralphRequest struct {
	Model     string         `json:"model"`
	MaxTokens int            `json:"max_tokens"`
	System    string         `json:"system,omitempty"`
	Messages  []ralphMsg     `json:"messages"`
	Tools     []ralphAPITool `json:"tools,omitempty"`
}

type ralphContentBlock struct {
	Type  string                 `json:"type"`
	Text  string                 `json:"text,omitempty"`
	ID    string                 `json:"id,omitempty"`
	Name  string                 `json:"name,omitempty"`
	Input map[string]interface{} `json:"input,omitempty"`
}

type ralphUsage struct {
	OutputTokens int `json:"output_tokens"`
}

type ralphResponse struct {
	Content    []ralphContentBlock `json:"content"`
	StopReason string              `json:"stop_reason"`
	Usage      ralphUsage          `json:"usage"`
}

// --- HTTP helper ---

func ralphCall(apiKey string, req ralphRequest) (ralphResponse, error) {
	body, _ := json.Marshal(req)
	r, _ := http.NewRequest("POST", envBaseURL(), bytes.NewReader(body))
	r.Header.Set("x-api-key", apiKey)
	r.Header.Set("anthropic-version", "2023-06-01")
	r.Header.Set("content-type", "application/json")

	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		return ralphResponse{}, err
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	var out ralphResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return ralphResponse{}, fmt.Errorf("parse: %w\n%s", err, raw)
	}
	return out, nil
}

// --- Token estimation ---

func estimateTokens(messages []ralphMsg) int {
	b, _ := json.Marshal(messages)
	return len(b) / 4
}

// --- Compaction cascade (4 layers) ---

func compactarCascada(messages []ralphMsg, apiKey string) []ralphMsg {
	// Layer 1: clear old tool_results beyond 6 cycles
	trCount := 0
	for i := len(messages) - 1; i >= 0; i-- {
		switch c := messages[i].Content.(type) {
		case []interface{}:
			for j, block := range c {
				if m, ok := block.(map[string]interface{}); ok && m["type"] == "tool_result" {
					trCount++
					if trCount > 6 {
						m["content"] = "[cleared]"
						c[j] = m
					}
				}
			}
		}
	}

	if estimateTokens(messages) <= historyBudget {
		return messages
	}

	// Layer 2: FIFO
	for estimateTokens(messages) > historyBudget && len(messages) > 2 {
		messages = messages[1:]
	}
	if estimateTokens(messages) <= historyBudget {
		return messages
	}

	// Layer 3: LLM summarization of the middle segment
	if len(messages) > 8 {
		head := messages[:2]
		tail := messages[len(messages)-4:]
		middle := messages[2 : len(messages)-4]
		middleJSON, _ := json.Marshal(middle)
		snippet := string(middleJSON)
		if len(snippet) > 8000 {
			snippet = snippet[:8000]
		}

		sumResp, err := ralphCall(apiKey, ralphRequest{
			Model:     envOr("SMALL_MODEL", "claude-haiku-4-5-20251001"),
			MaxTokens: 800,
			Messages: []ralphMsg{{
				Role:    "user",
				Content: "Resume este historial preservando decisiones y resultados clave:\n" + snippet,
			}},
		})
		if err == nil && len(sumResp.Content) > 0 {
			compressed := ralphMsg{
				Role:    "user",
				Content: "[HISTORIAL COMPRIMIDO]\n" + sumResp.Content[0].Text,
			}
			messages = append(append(head, compressed), tail...)
		}
	}

	// Layer 4: aggressive head+tail truncation
	if estimateTokens(messages) > historyBudget && len(messages) > 6 {
		messages = append(messages[:2], messages[len(messages)-4:]...)
	}

	return messages
}

// --- Permission check + execution ---

func ejecutarConPermisos(spec ralphToolSpec, args map[string]interface{}, level permissionLevel) (string, bool) {
	if level < spec.Permission {
		return fmt.Sprintf("[Denegado: requiere %s, actual %s]", spec.Permission, level), false
	}
	defer func() {
		if r := recover(); r != nil {
			// silently ignore panics from tool functions in demo
		}
	}()
	return spec.Fn(args), true
}

// --- Diminishing returns checker ---

type diminishingReturnsChecker struct {
	minTokens      int
	maxConsecutive int
	below          int
}

func (d *diminishingReturnsChecker) check(tokensOutput int) bool {
	if tokensOutput < d.minTokens {
		d.below++
	} else {
		d.below = 0
	}
	return d.below >= d.maxConsecutive
}

// --- Ralph loop ---

func ralphLoop(apiKey, userRequest, system string, specs []ralphToolSpec, level permissionLevel) string {
	messages := []ralphMsg{{Role: "user", Content: userRequest}}

	apiTools := make([]ralphAPITool, len(specs))
	for i, s := range specs {
		apiTools[i] = ralphAPITool{Name: s.Name, Description: s.Description, InputSchema: s.InputSchema}
	}

	toolMap := make(map[string]ralphToolSpec)
	for _, s := range specs {
		toolMap[s.Name] = s
	}

	dr := &diminishingReturnsChecker{
		minTokens:      diminishingReturnsTokens,
		maxConsecutive: diminishingReturnsIters,
	}

	for iteration := 1; ; iteration++ {
		if estimateTokens(messages) > int(float64(historyBudget)*compactionThreshold) {
			messages = compactarCascada(messages, apiKey)
			fmt.Printf("  [compactado → ~%dt]\n", estimateTokens(messages))
		}

		resp, err := ralphCall(apiKey, ralphRequest{
			Model:     ralphModel,
			MaxTokens: 2048,
			System:    system,
			Tools:     apiTools,
			Messages:  messages,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "error en iter %d: %v\n", iteration, err)
			return "[error de API]"
		}

		tokensOut := resp.Usage.OutputTokens
		fmt.Printf("[iter %d] stop=%s | output=%dt\n", iteration, resp.StopReason, tokensOut)

		if resp.StopReason == "end_turn" {
			for _, b := range resp.Content {
				if b.Type == "text" {
					return b.Text
				}
			}
			return ""
		}

		if dr.check(tokensOut) {
			fmt.Printf("  [ralph] %d iters no productivas → stop\n", diminishingReturnsIters)
			for _, b := range resp.Content {
				if b.Type == "text" {
					return b.Text
				}
			}
			return "[loop detenido]"
		}

		if resp.StopReason == "tool_use" {
			type toolResult struct {
				Type      string `json:"type"`
				ToolUseID string `json:"tool_use_id"`
				Content   string `json:"content"`
			}
			var results []toolResult

			for _, b := range resp.Content {
				if b.Type == "tool_use" {
					spec, ok := toolMap[b.Name]
					var r string
					if ok {
						r, _ = ejecutarConPermisos(spec, b.Input, level)
					} else {
						r = fmt.Sprintf("[tool '%s' no registrada]", b.Name)
					}
					trunc := r
					if len(trunc) > 60 {
						trunc = trunc[:60]
					}
					fmt.Printf("  %s(%v) → %s\n", b.Name, b.Input, trunc)
					results = append(results, toolResult{
						Type:      "tool_result",
						ToolUseID: b.ID,
						Content:   r,
					})
				}
			}

			// Convert content blocks to interface{} for JSON serialization
			assistantContent := make([]interface{}, len(resp.Content))
			for i, b := range resp.Content {
				assistantContent[i] = b
			}
			messages = append(messages, ralphMsg{Role: "assistant", Content: assistantContent})

			userContent := make([]interface{}, len(results))
			for i, r := range results {
				userContent[i] = r
			}
			messages = append(messages, ralphMsg{Role: "user", Content: userContent})
		}
	}
}

func main() {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "ANTHROPIC_API_KEY no está configurada")
		os.Exit(1)
	}

	specs := []ralphToolSpec{
		{
			Name:        "calcular",
			Description: "Evalúa una expresión matemática.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"expresion": map[string]interface{}{"type": "string"},
				},
				"required": []string{"expresion"},
			},
			Permission: readOnly,
			Fn: func(args map[string]interface{}) string {
				expr, _ := args["expresion"].(string)
				// Simple evaluator for demo
				parts := strings.Split(expr, "-")
				if len(parts) == 2 {
					// Parse "YYYY-MM-DD minus YYYY-MM-DD" style not handled here
					// Return expression for the model to reason about
					return fmt.Sprintf("[calcular: %s → usa diferencia de fechas]", expr)
				}
				return fmt.Sprintf("[expresión: %s]", expr)
			},
		},
		{
			Name:        "obtener_fecha",
			Description: "Devuelve la fecha actual en formato ISO.",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
			Permission: readOnly,
			Fn: func(args map[string]interface{}) string {
				// Return a fixed demo date
				return "2026-05-25"
			},
		},
	}

	resultado := ralphLoop(
		apiKey,
		"¿Cuántos días han pasado desde el 1 de enero de 2025 hasta hoy?",
		"Eres un asistente útil.",
		specs,
		readOnly,
	)
	fmt.Printf("\nRespuesta: %s\n", resultado)
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
