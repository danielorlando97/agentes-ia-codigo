// Ensamblador de contexto con presupuesto explícito por región.
// - ContextBudget: modelo de presupuesto con 5 regiones
// - Umbral configurable que activa la evicción antes del límite
// - clipText para recortar system prompt y memoria recuperada

// Cómo ejecutar: make go FILE=go/06-memoria/02-corto-plazo/nivel-3-produccion.go

package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

type Mensaje struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	Pinned  bool   `json:"pinned,omitempty"`
}

type ContextBudget struct {
	Total     int
	System    int
	Retrieved int
	Tools     int
	Response  int
	Threshold float64
}

func NewContextBudget() ContextBudget {
	return ContextBudget{
		Total: 128_000, System: 4_000, Retrieved: 3_000,
		Tools: 2_000, Response: 8_000, Threshold: 0.75,
	}
}

func (b ContextBudget) History() int {
	return b.Total - b.System - b.Retrieved - b.Tools - b.Response
}

func (b ContextBudget) CompactTrigger() int {
	return int(float64(b.History()) * b.Threshold)
}

func estimateTokens(obj interface{}) int {
	data, _ := json.Marshal(obj)
	return len(data) / 4
}

func clipText(text string, maxTokens int) string {
	maxChars := maxTokens * 4
	if len(text) > maxChars {
		return text[:maxChars]
	}
	return text
}

func reduceHistory(messages []Mensaje, budget int) []Mensaje {
	working := make([]Mensaje, len(messages))
	copy(working, messages)
	for estimateTokens(working) > budget {
		evicted := false
		for i, m := range working {
			if !m.Pinned {
				working = append(working[:i], working[i+1:]...)
				evicted = true
				break
			}
		}
		if !evicted {
			break
		}
	}
	return working
}

type ContextResult struct {
	System    string
	Retrieved string
	Tools     []interface{}
	Messages  []Mensaje
}

func buildContext(history []Mensaje, systemPrompt, retrieved string, budget ContextBudget) ContextResult {
	historyTokens := estimateTokens(history)
	if historyTokens > budget.CompactTrigger() {
		fmt.Printf("[contexto] historial=%dt > threshold=%dt → reduciendo\n",
			historyTokens, budget.CompactTrigger())
		history = reduceHistory(history, budget.History())
		fmt.Printf("[contexto] historial reducido a ~%dt\n", estimateTokens(history))
	}
	return ContextResult{
		System:    clipText(systemPrompt, budget.System),
		Retrieved: clipText(retrieved, budget.Retrieved),
		Tools:     []interface{}{},
		Messages:  history,
	}
}

func main() {
	budget := ContextBudget{
		Total: 10_000, System: 500, Retrieved: 300, Tools: 200, Response: 500, Threshold: 0.75,
	}

	history := []Mensaje{{Role: "user", Content: "Analiza este repositorio.", Pinned: true}}
	for i := 1; i <= 24; i++ {
		role := "user"
		if i%2 != 0 {
			role = "assistant"
		}
		history = append(history, Mensaje{
			Role:    role,
			Content: "contenido: " + strings.Repeat("x", 300),
		})
	}

	ctx := buildContext(
		history,
		"Eres un asistente de análisis de código experto en Python.",
		"Sesión anterior: el usuario analizó auth.py y encontró un bug en validate_token().",
		budget,
	)

	fmt.Printf("Historial final: %d mensajes\n", len(ctx.Messages))
	fmt.Printf("Anclado preservado: '%s'\n", ctx.Messages[0].Content)
	fmt.Printf("System clipeado a: %d chars\n", len(ctx.System))
}
