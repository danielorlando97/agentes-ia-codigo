// Sumarización lazy del historial conversacional.
// Comprime el intermedio cuando supera el umbral de tokens.
// Preserva cabeza (primeros 2) + cola (últimos 6 turnos) intactos.
//
// Cómo ejecutar: make go FILE=go/06-memoria/10-tecnicas/02-sumarizacion.go

package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	umbralTokens    = 2_000
	turnosPreservar = 6
	cabezaPreservar = 2
)

type Mensaje struct {
	Role      string `json:"role,omitempty"`
	Type      string `json:"type,omitempty"`
	ID        string `json:"id,omitempty"`
	ToolUseID string `json:"tool_use_id,omitempty"`
	Name      string `json:"name,omitempty"`
	Content   string `json:"content,omitempty"`
}

func estimarTokens(msgs []Mensaje) int {
	total := 0
	for _, m := range msgs {
		b, _ := json.Marshal(m)
		total += len(b) / 4
	}
	return total
}

func resumirMock(msgs []Mensaje) string {
	herramientas := map[string]bool{}
	nErrores := 0
	for _, m := range msgs {
		if m.Type == "tool_use" && m.Name != "" {
			herramientas[m.Name] = true
		}
		if strings.Contains(strings.ToLower(m.Content), "error") {
			nErrores++
		}
	}
	resumen := fmt.Sprintf("[%d turnos comprimidos] ", len(msgs))
	if len(herramientas) > 0 {
		nombres := []string{}
		for h := range herramientas {
			nombres = append(nombres, h)
		}
		resumen += "Herramientas: " + strings.Join(nombres, ", ") + ". "
	}
	if nErrores > 0 {
		resumen += fmt.Sprintf("%d errores encontrados. ", nErrores)
	}
	return resumen + "El agente continuó investigando."
}

func sanitizarPares(msgs []Mensaje) []Mensaje {
	resultIDs := map[string]bool{}
	for _, m := range msgs {
		if m.Type == "tool_result" && m.ToolUseID != "" {
			resultIDs[m.ToolUseID] = true
		}
	}
	salida := []Mensaje{}
	for _, m := range msgs {
		if m.Type == "tool_use" && !resultIDs[m.ID] {
			continue
		}
		salida = append(salida, m)
	}
	return salida
}

func buildContext(msgs []Mensaje) []Mensaje {
	if estimarTokens(msgs) <= umbralTokens {
		return msgs
	}
	if len(msgs) <= cabezaPreservar+turnosPreservar {
		return msgs
	}
	cabeza := msgs[:cabezaPreservar]
	cola := msgs[len(msgs)-turnosPreservar:]
	middle := msgs[cabezaPreservar : len(msgs)-turnosPreservar]

	middleLimpio := sanitizarPares(middle)
	resumenTexto := resumirMock(middleLimpio)

	bloqueResumen := Mensaje{
		Role: "user",
		Content: fmt.Sprintf("[HISTORIAL COMPRIMIDO — %d turnos]\n%s\n[FIN COMPRIMIDO]",
			len(middleLimpio), resumenTexto),
	}
	resultado := []Mensaje{}
	resultado = append(resultado, cabeza...)
	resultado = append(resultado, bloqueResumen)
	resultado = append(resultado, cola...)
	return resultado
}

func simularHistorial(n int) []Mensaje {
	msgs := []Mensaje{{Role: "user", Content: "Analiza el repositorio completo."}}
	for i := 1; i < n; i++ {
		switch i % 4 {
		case 1:
			msgs = append(msgs, Mensaje{
				Role: "assistant", Type: "tool_use",
				ID: fmt.Sprintf("tool_%d", i), Name: "read_file",
			})
		case 2:
			msgs = append(msgs, Mensaje{
				Role: "user", Type: "tool_result",
				ToolUseID: fmt.Sprintf("tool_%d", i-1),
				Content:   fmt.Sprintf("Contenido módulo %d: %s", i/4, strings.Repeat("código ", 15)),
			})
		default:
			msgs = append(msgs, Mensaje{
				Role:    "assistant",
				Content: fmt.Sprintf("Análisis parcial #%d: continuando.", i/4),
			})
		}
	}
	return msgs
}

func main() {
	historial := simularHistorial(30)
	tokensOrig := estimarTokens(historial)
	fmt.Printf("Historial original: %d mensajes, ~%d tokens\n", len(historial), tokensOrig)

	contexto := buildContext(historial)
	tokensComp := estimarTokens(contexto)
	reduccion := 100 * (1 - float64(tokensComp)/float64(tokensOrig))
	fmt.Printf("Contexto comprimido: %d mensajes, ~%d tokens\n", len(contexto), tokensComp)
	fmt.Printf("Reducción: %.0f%%\n\n", reduccion)

	for _, m := range contexto {
		tipo := m.Type
		if tipo == "" {
			tipo = m.Role
		}
		contenido := m.Content
		if contenido == "" {
			contenido = m.Name
		}
		if len(contenido) > 80 {
			contenido = contenido[:80]
		}
		fmt.Printf("  [%s] %s\n", tipo, contenido)
	}
}
