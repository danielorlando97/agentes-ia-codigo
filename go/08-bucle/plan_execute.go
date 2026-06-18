// Plan-and-Execute: separa planificación de ejecución.
//
// El planificador (modelo estándar) genera una lista de pasos con una sola llamada.
// El executor (modelo económico) implementa cada paso con tool_use nativo.
// Dynamic replanning: si un paso falla, el planificador se invoca de nuevo
// con el estado actual para regenerar el plan restante.
//
// Requiere: ANTHROPIC_API_KEY en el entorno.

// Cómo ejecutar: make go FILE=go/08-bucle/plan_execute.go

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
)

const (
	maxReplans    = 2
)

var (
	plannerModel = envOr("MODEL", "claude-sonnet-4-6")
	executorModel = envOr("SMALL_MODEL", "claude-haiku-4-5-20251001")
	apiURL = envBaseURL()
)

var stepRe = regexp.MustCompile(`(?m)^\d+[.)]\s+(.+)$`)

// --- API types ---

type peMsg struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"`
}

type peTool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"input_schema"`
}

type peRequest struct {
	Model     string  `json:"model"`
	MaxTokens int     `json:"max_tokens"`
	System    string  `json:"system,omitempty"`
	Messages  []peMsg `json:"messages"`
	Tools     []peTool `json:"tools,omitempty"`
}

type peContentBlock struct {
	Type  string                 `json:"type"`
	Text  string                 `json:"text,omitempty"`
	ID    string                 `json:"id,omitempty"`
	Name  string                 `json:"name,omitempty"`
	Input map[string]interface{} `json:"input,omitempty"`
}

type peResponse struct {
	Content    []peContentBlock `json:"content"`
	StopReason string           `json:"stop_reason"`
}

// --- HTTP helper ---

func peCall(apiKey string, req peRequest) (peResponse, error) {
	body, _ := json.Marshal(req)
	r, _ := http.NewRequest("POST", apiURL, bytes.NewReader(body))
	r.Header.Set("x-api-key", apiKey)
	r.Header.Set("anthropic-version", "2023-06-01")
	r.Header.Set("content-type", "application/json")

	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		return peResponse{}, err
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	var out peResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return peResponse{}, fmt.Errorf("parse: %w\n%s", err, raw)
	}
	return out, nil
}

// --- Planner ---

func planificar(apiKey, tarea, estado string) []string {
	prompt := fmt.Sprintf(
		"Genera una lista numerada de pasos atómicos para completar esta tarea.\n"+
			"Cada paso debe comenzar con un verbo de acción y ser ejecutable de forma independiente.\n\n"+
			"Tarea: %s\nEstado actual: %s\n\n"+
			"Responde solo con la lista numerada, sin explicaciones adicionales.",
		tarea, estado,
	)
	resp, err := peCall(apiKey, peRequest{
		Model:     plannerModel,
		MaxTokens: 600,
		Messages:  []peMsg{{Role: "user", Content: prompt}},
	})
	if err != nil || len(resp.Content) == 0 {
		return nil
	}
	matches := stepRe.FindAllStringSubmatch(resp.Content[0].Text, -1)
	pasos := make([]string, 0, len(matches))
	for _, m := range matches {
		if s := strings.TrimSpace(m[1]); s != "" {
			pasos = append(pasos, s)
		}
	}
	return pasos
}

// --- Executor (tool_use nativo) ---

type toolFn func(args map[string]interface{}) string

func ejecutarPaso(apiKey, paso, contexto string, tools []peTool, fns map[string]toolFn) (string, bool) {
	messages := []peMsg{{
		Role:    "user",
		Content: fmt.Sprintf("Contexto previo:\n%s\n\nEjecuta este paso: %s", contexto, paso),
	}}

	for range 8 {
		resp, err := peCall(apiKey, peRequest{
			Model:     executorModel,
			MaxTokens: 512,
			Tools:     tools,
			Messages:  messages,
		})
		if err != nil {
			return fmt.Sprintf("[error: %v]", err), false
		}

		if resp.StopReason == "end_turn" {
			for _, b := range resp.Content {
				if b.Type == "text" {
					return b.Text, true
				}
			}
			return "[sin texto]", false
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
					fn, ok := fns[b.Name]
					var r string
					if ok {
						r = fn(b.Input)
					} else {
						r = fmt.Sprintf("[tool '%s' no encontrada]", b.Name)
					}
					results = append(results, toolResult{
						Type:      "tool_result",
						ToolUseID: b.ID,
						Content:   r,
					})
				}
			}

			assistantContent := make([]interface{}, len(resp.Content))
			for i, b := range resp.Content {
				assistantContent[i] = b
			}
			messages = append(messages, peMsg{Role: "assistant", Content: assistantContent})

			userContent := make([]interface{}, len(results))
			for i, r := range results {
				userContent[i] = r
			}
			messages = append(messages, peMsg{Role: "user", Content: userContent})
			continue
		}

		return "[paso no completado]", false
	}
	return "[max iteraciones del executor]", false
}

// --- Agent loop ---

func runPlanExecute(apiKey, tarea string, tools []peTool, fns map[string]toolFn) string {
	plan := planificar(apiKey, tarea, "Sin ejecución previa")
	fmt.Printf("Plan (%d pasos):\n", len(plan))
	for j, p := range plan {
		fmt.Printf("  %d. %s\n", j+1, p)
	}

	var resultados []string
	replans := 0
	i := 0

	for i < len(plan) {
		var ctxParts []string
		for j, r := range resultados {
			ctxParts = append(ctxParts, fmt.Sprintf("Paso %d: %s", j+1, r))
		}
		contexto := strings.Join(ctxParts, "\n")

		resultado, exito := ejecutarPaso(apiKey, plan[i], contexto, tools, fns)

		mark := "✓"
		if !exito {
			mark = "✗"
		}
		trunc := plan[i]
		if len(trunc) > 60 {
			trunc = trunc[:60]
		}
		fmt.Printf("\n[paso %d/%d] %s %s\n", i+1, len(plan), mark, trunc)

		if !exito && replans < maxReplans {
			estado := fmt.Sprintf("Completados: %v\nFalló: %s", resultados, plan[i])
			nuevo := planificar(apiKey, tarea, estado)
			if len(nuevo) > 0 {
				replans++
				plan = append(plan[:i], nuevo...)
				fmt.Printf("  → replan #%d: %d pasos nuevos\n", replans, len(nuevo))
				continue
			}
		}

		resultados = append(resultados, resultado)
		i++
	}

	// Síntesis final
	sintesisPrompt := fmt.Sprintf(
		"Tarea: %s\n\nResultados de los pasos:\n%s\n\nResume qué se logró en 2-3 frases.",
		tarea, strings.Join(resultados, "\n"),
	)
	resp, err := peCall(apiKey, peRequest{
		Model:     plannerModel,
		MaxTokens: 400,
		Messages:  []peMsg{{Role: "user", Content: sintesisPrompt}},
	})
	if err != nil || len(resp.Content) == 0 {
		return strings.Join(resultados, "\n")
	}
	return resp.Content[0].Text
}

func main() {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "ANTHROPIC_API_KEY no está configurada")
		os.Exit(1)
	}

	tools := []peTool{{
		Name:        "calcular",
		Description: "Evalúa una expresión matemática simple.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"expresion": map[string]interface{}{
					"type":        "string",
					"description": "ej: '15 * 8'",
				},
			},
			"required": []string{"expresion"},
		},
	}}

	fns := map[string]toolFn{
		"calcular": func(args map[string]interface{}) string {
			expr, _ := args["expresion"].(string)
			// Evaluador numérico básico: solo suma y multiplicación para la demo
			parts := strings.Split(expr, "*")
			if len(parts) == 2 {
				a, errA := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
				b, errB := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
				if errA == nil && errB == nil {
					return fmt.Sprintf("%g", a*b)
				}
			}
			parts = strings.Split(expr, "+")
			if len(parts) == 2 {
				a, errA := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
				b, errB := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
				if errA == nil && errB == nil {
					return fmt.Sprintf("%g", a+b)
				}
			}
			return fmt.Sprintf("[no puedo evaluar: %s]", expr)
		},
	}

	resultado := runPlanExecute(
		apiKey,
		"Calcula el área de un rectángulo de 15 por 8 metros. "+
			"Luego calcula cuántas baldosas de 0.25 m² se necesitan para cubrirlo.",
		tools,
		fns,
	)
	fmt.Printf("\n=== Resultado final ===\n%s\n", resultado)
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
