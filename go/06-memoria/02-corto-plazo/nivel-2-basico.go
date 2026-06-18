// Evicción FIFO por presupuesto de tokens con mensajes anclados.
// - Estimación de tokens (len(json) / 4)
// - Evicción por tokens en lugar de conteo de turns
// - Mensajes con Pinned=true nunca se evictan

// Cómo ejecutar: make go FILE=go/06-memoria/02-corto-plazo/nivel-2-basico.go

package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

const historyBudget = 110_000

type Mensaje struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	Pinned  bool   `json:"pinned,omitempty"`
}

func estimateTokens(messages []Mensaje) int {
	total := 0
	for _, m := range messages {
		b, _ := json.Marshal(m)
		total += len(b)
	}
	return total / 4
}

func buildContext(messages []Mensaje, budget int) []Mensaje {
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

func main() {
	msgs := []Mensaje{{Role: "user", Content: "Analiza este repositorio.", Pinned: true}}
	for i := 1; i < 20; i++ {
		role := "assistant"
		if i%2 == 0 {
			role = "user"
		}
		msgs = append(msgs, Mensaje{
			Role:    role,
			Content: "resultado de herramienta: " + strings.Repeat("x", 800),
		})
	}

	budget := 4_000
	result := buildContext(msgs, budget)

	fmt.Printf("Entrada: %d msgs, ~%d tokens\n", len(msgs), estimateTokens(msgs))
	fmt.Printf("Salida:  %d msgs, ~%d tokens (budget=%d)\n", len(result), estimateTokens(result), budget)
	fmt.Printf("Anclado preservado: %v → '%s'\n", result[0].Pinned, result[0].Content[:min(40, len(result[0].Content))])
}
