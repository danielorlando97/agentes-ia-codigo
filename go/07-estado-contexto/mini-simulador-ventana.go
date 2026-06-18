// Mini-proyecto: El simulador de ventana de contexto (Go).
//
// Uso:
//
//	go run mini-simulador-ventana.go
//	go run mini-simulador-ventana.go -ventana 4096
//	go run mini-simulador-ventana.go -turnos 30 -tecnica clearing
//	go run mini-simulador-ventana.go -tecnica todos

// Cómo ejecutar: make go FILE=go/07-estado-contexto/mini-simulador-ventana.go

package main

import (
	"flag"
	"fmt"
	"math/rand"
	"strings"
)

// Snapshot precios mayo 2026 — verificar en docs del proveedor
const precioSonnetInput = 3.00 // USD / millón tokens
const ventanaDefault = 8_192

// ── tipos ─────────────────────────────────────────────────────────────────────

type Mensaje struct {
	role    string
	content string
	bloques []Bloque // nil para mensajes de texto plano
}

type Bloque struct {
	tipo       string // "tool_use" | "tool_result"
	id         string
	contenido  string
	clareado   bool
}

func (m *Mensaje) texto() string {
	if len(m.bloques) > 0 {
		var parts []string
		for _, b := range m.bloques {
			parts = append(parts, b.contenido)
		}
		return strings.Join(parts, " ")
	}
	return m.content
}

func estimarTokens(texto string) int {
	n := len([]rune(texto)) / 4
	if n < 1 {
		return 1
	}
	return n
}

func tokensMensaje(m *Mensaje) int {
	return estimarTokens(m.texto()) + 4
}

func tokensHistorial(mensajes []*Mensaje) int {
	total := 0
	for _, m := range mensajes {
		total += tokensMensaje(m)
	}
	return total
}

// ── generador ─────────────────────────────────────────────────────────────────

var palabras = []string{
	"agente", "contexto", "herramienta", "respuesta", "análisis",
	"código", "función", "resultado", "iteración", "plan",
	"decisión", "estado", "memoria", "búsqueda", "resumen",
}

func lorem(nTokens int, rng *rand.Rand) string {
	var parts []string
	for len(strings.Join(parts, " "))/4 < nTokens {
		parts = append(parts, palabras[rng.Intn(len(palabras))])
	}
	return strings.Join(parts, " ")
}

var tiposPeso = []struct {
	tipo string
	peso int
}{
	{"user_simple", 30},
	{"user_largo", 120},
	{"assistant_texto", 80},
	{"tool_call", 40},
	{"tool_result_corto", 60},
	{"tool_result_largo", 400},
}

func elegirPonderado(rng *rand.Rand) string {
	total := 0
	for _, tp := range tiposPeso {
		total += tp.peso
	}
	r := rng.Intn(total)
	for _, tp := range tiposPeso {
		r -= tp.peso
		if r < 0 {
			return tp.tipo
		}
	}
	return tiposPeso[len(tiposPeso)-1].tipo
}

func generarTurno(tipo string, rng *rand.Rand) *Mensaje {
	switch tipo {
	case "user_simple":
		return &Mensaje{role: "user", content: lorem(30, rng)}
	case "user_largo":
		return &Mensaje{role: "user", content: lorem(120, rng)}
	case "assistant_texto":
		return &Mensaje{role: "assistant", content: lorem(80, rng)}
	case "tool_call":
		id := fmt.Sprintf("t%d", rng.Intn(9000)+1000)
		return &Mensaje{
			role: "assistant",
			bloques: []Bloque{{tipo: "tool_use", id: id, contenido: lorem(10, rng)}},
		}
	case "tool_result_corto":
		id := fmt.Sprintf("t%d", rng.Intn(9000)+1000)
		return &Mensaje{
			role: "user",
			bloques: []Bloque{{tipo: "tool_result", id: id, contenido: lorem(60, rng)}},
		}
	case "tool_result_largo":
		id := fmt.Sprintf("t%d", rng.Intn(9000)+1000)
		return &Mensaje{
			role: "user",
			bloques: []Bloque{{tipo: "tool_result", id: id, contenido: lorem(400, rng)}},
		}
	}
	return &Mensaje{role: "user", content: lorem(30, rng)}
}

func generarHistorial(nTurnos int) []*Mensaje {
	rng := rand.New(rand.NewSource(42))
	msgs := make([]*Mensaje, nTurnos)
	for i := range msgs {
		msgs[i] = generarTurno(elegirPonderado(rng), rng)
	}
	return msgs
}

// ── técnicas ──────────────────────────────────────────────────────────────────

func esToolResult(m *Mensaje) bool {
	for _, b := range m.bloques {
		if b.tipo == "tool_result" {
			return true
		}
	}
	return false
}

func clonarMensaje(m *Mensaje) *Mensaje {
	nuevo := &Mensaje{role: m.role, content: m.content}
	if len(m.bloques) > 0 {
		nuevo.bloques = make([]Bloque, len(m.bloques))
		copy(nuevo.bloques, m.bloques)
	}
	return nuevo
}

func clonarHistorial(msgs []*Mensaje) []*Mensaje {
	clon := make([]*Mensaje, len(msgs))
	for i, m := range msgs {
		clon[i] = clonarMensaje(m)
	}
	return clon
}

func aplicarClearing(msgs []*Mensaje) []*Mensaje {
	resultado := make([]*Mensaje, len(msgs))
	for i, m := range msgs {
		if esToolResult(m) {
			nuevo := clonarMensaje(m)
			for j := range nuevo.bloques {
				if nuevo.bloques[j].tipo == "tool_result" {
					nuevo.bloques[j].contenido = "[cleared]"
				}
			}
			resultado[i] = nuevo
		} else {
			resultado[i] = m
		}
	}
	return resultado
}

func aplicarHeadTail(msgs []*Mensaje, maxTokens int) []*Mensaje {
	if tokensHistorial(msgs) <= maxTokens {
		return msgs
	}
	var head, tail []*Mensaje
	presupuesto := maxTokens
	for _, m := range msgs {
		tok := tokensMensaje(m)
		if presupuesto >= tok {
			head = append(head, m)
			presupuesto -= tok
		} else {
			break
		}
	}
	for i := len(msgs) - 1; i >= len(head); i-- {
		tok := tokensMensaje(msgs[i])
		if presupuesto >= tok {
			tail = append([]*Mensaje{msgs[i]}, tail...)
			presupuesto -= tok
		} else {
			break
		}
	}
	omitidos := len(msgs) - len(head) - len(tail)
	sep := &Mensaje{role: "user", content: fmt.Sprintf("[... %d mensajes omitidos ...]", omitidos)}
	return append(append(head, sep), tail...)
}

func aplicarSumarizacion(msgs []*Mensaje, maxTokens int, rng *rand.Rand) []*Mensaje {
	if tokensHistorial(msgs) <= maxTokens {
		return msgs
	}
	nConservar := len(msgs) / 3
	if nConservar < 2 {
		nConservar = 2
	}
	aResumir := msgs[:len(msgs)-nConservar]
	recientes := msgs[len(msgs)-nConservar:]
	tokResumidos := tokensHistorial(aResumir)
	resumenTok := tokResumidos / 5
	if resumenTok < 20 {
		resumenTok = 20
	}
	resumen := &Mensaje{
		role: "user",
		content: fmt.Sprintf("[RESUMEN de %d mensajes / ~%d tokens → %d tokens]: %s",
			len(aResumir), tokResumidos, resumenTok, lorem(resumenTok, rng)),
	}
	return append([]*Mensaje{resumen}, recientes...)
}

// ── simulación ────────────────────────────────────────────────────────────────

type Resultado struct {
	tecnica         string
	tokensEnviados  int
	compactaciones  int
	tokensAhorrados int
	desbordamientos int
	tokensFinal     int
	costoUSD        float64
}

func simular(original []*Mensaje, tecnica string, ventana int) Resultado {
	umbral := int(float64(ventana) * 0.85)
	rng := rand.New(rand.NewSource(99))

	var compactaciones, tokensEnviados, tokensAhorrados, desbordamientos int
	var historial []*Mensaje

	for _, msg := range original {
		historial = append(historial, msg)
		tokActual := tokensHistorial(historial)

		if tokActual > umbral {
			tokAntes := tokActual
			switch tecnica {
			case "clearing":
				historial = aplicarClearing(historial)
			case "head_tail":
				historial = aplicarHeadTail(historial, umbral)
			case "sumarizacion":
				historial = aplicarSumarizacion(historial, umbral, rng)
			}
			compactaciones++
			diff := tokAntes - tokensHistorial(historial)
			if diff > 0 {
				tokensAhorrados += diff
			}
		}

		tokFinal := tokensHistorial(historial)
		tokensEnviados += tokFinal
		if tokFinal > ventana {
			desbordamientos++
		}
	}

	return Resultado{
		tecnica:         tecnica,
		tokensEnviados:  tokensEnviados,
		compactaciones:  compactaciones,
		tokensAhorrados: tokensAhorrados,
		desbordamientos: desbordamientos,
		tokensFinal:     tokensHistorial(historial),
		costoUSD:        float64(tokensEnviados) * precioSonnetInput / 1_000_000,
	}
}

// ── presentación ──────────────────────────────────────────────────────────────

func barra(valor, maximo, ancho int) string {
	if maximo == 0 {
		return strings.Repeat("░", ancho)
	}
	lleno := valor * ancho / maximo
	return strings.Repeat("█", lleno) + strings.Repeat("░", ancho-lleno)
}

func imprimirResultados(resultados []Resultado, nTurnos, ventana int) {
	fmt.Printf("\n%s\n", strings.Repeat("=", 66))
	fmt.Println("  SIMULADOR DE VENTANA DE CONTEXTO")
	fmt.Printf("  %d turnos  |  ventana %d tokens  |  precios sonnet mayo 2026\n", nTurnos, ventana)
	fmt.Printf("%s\n", strings.Repeat("=", 66))

	maxTok := 1
	maxCost := 0.0001
	for _, r := range resultados {
		if r.tokensEnviados > maxTok {
			maxTok = r.tokensEnviados
		}
		if r.costoUSD > maxCost {
			maxCost = r.costoUSD
		}
	}

	fmt.Printf("\n%-16s %11s %9s %11s %9s %8s\n",
		"Técnica", "Tokens env.", "Compact.", "Ahorr. tok", "Desbord.", "USD")
	fmt.Println(strings.Repeat("-", 66))
	for _, r := range resultados {
		fmt.Printf("%-16s %11d %9d %11d %9d $%7.4f\n",
			r.tecnica, r.tokensEnviados, r.compactaciones,
			r.tokensAhorrados, r.desbordamientos, r.costoUSD)
	}

	fmt.Printf("\n%s\n", strings.Repeat("─", 66))
	fmt.Println("  Tokens enviados (barra relativa al máximo)")
	fmt.Println(strings.Repeat("─", 66))
	for _, r := range resultados {
		fmt.Printf("  %-14s %s  %d\n", r.tecnica, barra(r.tokensEnviados, maxTok, 30), r.tokensEnviados)
	}

	fmt.Printf("\n%s\n", strings.Repeat("─", 66))
	fmt.Println("  Costo USD (barra relativa al máximo)")
	fmt.Println(strings.Repeat("─", 66))
	var base *Resultado
	for i := range resultados {
		if resultados[i].tecnica == "ninguna" {
			base = &resultados[i]
			break
		}
	}
	for _, r := range resultados {
		ahorroStr := ""
		if base != nil && r.tecnica != "ninguna" {
			ahorro := (1 - r.costoUSD/base.costoUSD) * 100
			ahorroStr = fmt.Sprintf("  (%+.1f%% vs sin compactación)", ahorro)
		}
		fmt.Printf("  %-14s %s  $%.4f%s\n", r.tecnica, barra(int(r.costoUSD*1e6), int(maxCost*1e6), 30), r.costoUSD, ahorroStr)
	}

	fmt.Println("\n[Estimación ±10% — conteo exacto con tiktoken (Python)]")
	fmt.Println("[Snapshot precios mayo 2026 — verificar en docs del proveedor]")
}

// ── main ──────────────────────────────────────────────────────────────────────

func main() {
	ventana := flag.Int("ventana", ventanaDefault, "Tokens de ventana")
	turnos := flag.Int("turnos", 40, "Número de turnos a simular")
	tecnicaFlag := flag.String("tecnica", "todos", "Técnica: clearing|head_tail|sumarizacion|ninguna|todos")
	flag.Parse()

	historial := generarHistorial(*turnos)
	tokensBruto := tokensHistorial(historial)

	fmt.Printf("\n[Historial generado: %d turnos, %d tokens bruto]\n", *turnos, tokensBruto)
	fmt.Printf("[Ventana configurada: %d tokens  |  umbral: %d]\n", *ventana, int(float64(*ventana)*0.85))

	var tecnicas []string
	if *tecnicaFlag == "todos" {
		tecnicas = []string{"ninguna", "clearing", "head_tail", "sumarizacion"}
	} else {
		tecnicas = []string{*tecnicaFlag}
	}

	resultados := make([]Resultado, len(tecnicas))
	for i, t := range tecnicas {
		resultados[i] = simular(historial, t, *ventana)
	}
	imprimirResultados(resultados, *turnos, *ventana)
}
