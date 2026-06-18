// Memoria procedural como few-shot dinámico con scoring Jaccard.

// Cómo ejecutar: make go FILE=go/06-memoria/05-procedural/procedural_fewshot.go

package main

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

type Ejemplo struct {
	Contexto string
	Salida   string
	Score    float64
}

func jaccard(a, b string) float64 {
	wordsA := strings.Fields(strings.ToLower(a))
	wordsB := strings.Fields(strings.ToLower(b))
	if len(wordsA) == 0 || len(wordsB) == 0 {
		return 0
	}
	setA := make(map[string]bool)
	for _, w := range wordsA {
		setA[w] = true
	}
	setB := make(map[string]bool)
	for _, w := range wordsB {
		setB[w] = true
	}
	var interseccion int
	for w := range setA {
		if setB[w] {
			interseccion++
		}
	}
	union := len(setA) + len(setB) - interseccion
	return float64(interseccion) / float64(union)
}

type BufferFewShot struct {
	ejemplos    []Ejemplo
	maxEjemplos int
}

func NewBufferFewShot(maxEjemplos int) *BufferFewShot {
	return &BufferFewShot{maxEjemplos: maxEjemplos}
}

func (b *BufferFewShot) Add(contexto, salida string) {
	if len(b.ejemplos) >= b.maxEjemplos {
		sort.Slice(b.ejemplos, func(i, j int) bool {
			return b.ejemplos[i].Score < b.ejemplos[j].Score
		})
		b.ejemplos = b.ejemplos[1:]
	}
	b.ejemplos = append(b.ejemplos, Ejemplo{Contexto: contexto, Salida: salida, Score: 1.0})
}

func (b *BufferFewShot) Reinforce(contexto string, delta float64) {
	for i := range b.recuperarSimilaresIdx(contexto, 3) {
		b.ejemplos[i].Score = math.Min(2.0, b.ejemplos[i].Score+delta)
	}
}

func (b *BufferFewShot) Penalize(contexto string, delta float64) {
	idxs := b.recuperarSimilaresIdx(contexto, 3)
	for _, idx := range idxs {
		b.ejemplos[idx].Score = math.Max(0.0, b.ejemplos[idx].Score-delta)
	}
	var filtrados []Ejemplo
	for _, e := range b.ejemplos {
		if e.Score >= 0.1 {
			filtrados = append(filtrados, e)
		}
	}
	b.ejemplos = filtrados
}

func (b *BufferFewShot) recuperarSimilaresIdx(query string, topK int) []int {
	type scored struct {
		idx int
		s   float64
	}
	var lista []scored
	for i, e := range b.ejemplos {
		lista = append(lista, scored{i, jaccard(query, e.Contexto) * e.Score})
	}
	sort.Slice(lista, func(i, j int) bool { return lista[i].s > lista[j].s })
	var idxs []int
	for i := 0; i < topK && i < len(lista); i++ {
		idxs = append(idxs, lista[i].idx)
	}
	return idxs
}

func (b *BufferFewShot) recuperarSimilares(query string, topK int) []Ejemplo {
	idxs := b.recuperarSimilaresIdx(query, topK)
	var result []Ejemplo
	for _, idx := range idxs {
		result = append(result, b.ejemplos[idx])
	}
	return result
}

func (b *BufferFewShot) BuildFewShotBlock(query string, topK int) string {
	ejemplos := b.recuperarSimilares(query, topK)
	if len(ejemplos) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("## Ejemplos de comportamiento esperado\n\n")
	for i, e := range ejemplos {
		sb.WriteString(fmt.Sprintf("Ejemplo %d:\nEntrada: %s\nSalida: %s\n\n", i+1, e.Contexto, e.Salida))
	}
	return sb.String()
}

func main() {
	buf := NewBufferFewShot(20)

	buf.Add(
		"Explica qué es un decorador en Python",
		"Un decorador es una función que envuelve a otra función para modificar su comportamiento sin cambiar su código.",
	)
	buf.Add(
		"Escribe un ejemplo de función recursiva",
		"func factorial(n int) int {\n  if n <= 1 { return 1 }\n  return n * factorial(n-1)\n}",
	)
	buf.Add(
		"Corrige el manejo de errores en este código",
		"Añade manejo específico de errores, loggea con contexto, y re-lanza si no puedes manejar.",
	)
	buf.Add("Resume este texto en 3 puntos", "• Punto 1: ...\n• Punto 2: ...\n• Punto 3: ...")

	query := "Dame un ejemplo de recursión en Go"
	fmt.Printf("Buffer: %d ejemplos\n\n", len(buf.ejemplos))
	fmt.Print(buf.BuildFewShotBlock(query, 2))

	buf.Reinforce("función recursiva", 0.1)
	fmt.Printf("Score del ejemplo de recursión actualizado.\n")
}
