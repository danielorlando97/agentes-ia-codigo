// Buffer conversacional con evicción por tokens, mutex y paridad tool_use/tool_result.
//
// Cómo ejecutar: make go FILE=go/06-memoria/10-tecnicas/01-buffer.go

package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

type Mensaje struct {
	Role      string `json:"role,omitempty"`
	Type      string `json:"type,omitempty"`
	ID        string `json:"id,omitempty"`
	ToolUseID string `json:"tool_use_id,omitempty"`
	Name      string `json:"name,omitempty"`
	Content   string `json:"content,omitempty"`
	Pinned    bool   `json:"-"`
}

func estimarTokens(m Mensaje) int {
	b, _ := json.Marshal(m)
	return len(b) / 4
}

type BufferConversacional struct {
	mensajes         []Mensaje
	budget           int
	mu               sync.Mutex
}

func NuevoBuffer(maxTokens, reservaRespuesta int) *BufferConversacional {
	return &BufferConversacional{budget: maxTokens - reservaRespuesta}
}

func (b *BufferConversacional) Agregar(m Mensaje, pinned bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	m.Pinned = pinned
	b.mensajes = append(b.mensajes, m)
	b.evictar()
}

func (b *BufferConversacional) Snapshot() []Mensaje {
	b.mu.Lock()
	defer b.mu.Unlock()
	copia := make([]Mensaje, len(b.mensajes))
	copy(copia, b.mensajes)
	for i := range copia {
		copia[i].Pinned = false
	}
	return copia
}

func (b *BufferConversacional) TokensActuales() int {
	total := 0
	for _, m := range b.mensajes {
		total += estimarTokens(m)
	}
	return total
}

func (b *BufferConversacional) evictar() {
	for b.TokensActuales() > b.budget {
		idx := b.primerEviccionable()
		if idx < 0 {
			break
		}
		b.mensajes = append(b.mensajes[:idx], b.mensajes[idx+1:]...)
	}
}

func (b *BufferConversacional) primerEviccionable() int {
	toolUseIDs := map[string]bool{}
	toolResultIDs := map[string]bool{}
	for _, m := range b.mensajes {
		if m.Type == "tool_use" && m.ID != "" {
			toolUseIDs[m.ID] = true
		}
		if m.Type == "tool_result" && m.ToolUseID != "" {
			toolResultIDs[m.ToolUseID] = true
		}
	}
	paresActivos := map[string]bool{}
	for id := range toolUseIDs {
		if toolResultIDs[id] {
			paresActivos[id] = true
		}
	}

	for i, m := range b.mensajes {
		if m.Pinned {
			continue
		}
		if m.Type == "tool_use" && paresActivos[m.ID] {
			continue
		}
		if m.Type == "tool_result" && paresActivos[m.ToolUseID] {
			continue
		}
		return i
	}
	return -1
}

func main() {
	buf := NuevoBuffer(600, 200)

	buf.Agregar(Mensaje{Role: "user", Content: "Analiza el módulo de pagos"}, true)
	buf.Agregar(Mensaje{Role: "assistant", Content: "Voy a revisar los archivos."}, false)

	for i := 0; i < 4; i++ {
		useID := fmt.Sprintf("tu_%d", i)
		buf.Agregar(Mensaje{
			Role:    "assistant",
			Type:    "tool_use",
			ID:      useID,
			Name:    "read_file",
			Content: fmt.Sprintf("src/pagos/modulo_%d.go", i),
		}, false)
		buf.Agregar(Mensaje{
			Role:      "user",
			Type:      "tool_result",
			ToolUseID: useID,
			Content:   fmt.Sprintf("Contenido módulo %d: %s", i, strings.Repeat("x", 80)),
		}, false)
	}

	buf.Agregar(Mensaje{Role: "assistant", Content: "Análisis completo. El módulo 2 tiene el problema."}, false)
	buf.Agregar(Mensaje{Role: "user", Content: "¿Qué tipo de problema?"}, false)

	snap := buf.Snapshot()
	fmt.Printf("Mensajes en buffer: %d\n", len(snap))
	fmt.Printf("Tokens estimados: %d\n", buf.TokensActuales())
	fmt.Println()

	for _, m := range snap {
		tipo := m.Type
		if tipo == "" {
			tipo = m.Role
		}
		contenido := m.Content
		if len(contenido) > 60 {
			contenido = contenido[:60]
		}
		fmt.Printf("  %s: %s\n", tipo, contenido)
	}
}
