// Test de localizacion: clasifica un sistema en el espectro smolagents.

// Cómo ejecutar: make go FILE=go/01-que-es-un-agente/clasificador-nivel.go

package main

import "fmt"

type features struct {
	multiAgente               bool
	codeAgent                 bool
	loopNoAcotadoYDecideTools bool
	loopAcotadoConTools       bool
	llmEligeRutaSinLoop       bool
}

func classify(f features) string {
	switch {
	case f.multiAgente:
		return "★★★ multi-agente"
	case f.codeAgent:
		return "★★★ code agent"
	case f.loopNoAcotadoYDecideTools:
		return "★★☆ multi-step"
	case f.loopAcotadoConTools:
		return "★★☆ tool caller"
	case f.llmEligeRutaSinLoop:
		return "★☆☆ router"
	default:
		return "☆☆☆ procesador"
	}
}

func main() {
	cases := []struct {
		name string
		f    features
	}{
		{"traduccion sin tools", features{}},
		{"router por intent", features{llmEligeRutaSinLoop: true}},
		{"RAG simple (1 retrieve + 1 generate)", features{loopAcotadoConTools: true}},
		{"ReAct hasta end_turn", features{loopNoAcotadoYDecideTools: true}},
		{"supervisor + workers", features{multiAgente: true}},
		{"agente que escribe codigo Python", features{codeAgent: true}},
	}
	for _, c := range cases {
		fmt.Printf("%-25s  %s\n", classify(c.f), c.name)
	}
}
