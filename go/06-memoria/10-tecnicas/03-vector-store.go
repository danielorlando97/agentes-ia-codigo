// Vector store: SQL como fuente de verdad, embeddings como ranking semántico.
// Orquestación con degradación: si el índice falla, busca por texto.
//
// Cómo ejecutar: make go FILE=go/06-memoria/10-tecnicas/03-vector-store.go

package main

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

type Memoria struct {
	ID     string
	Texto  string
	Tipo   string
	Fuente string
	Creado int64
}

type ResultadoBusqueda struct {
	Memoria
	Score *float64
}

func mockEmbedding(texto string, dim int) []float64 {
	seed := uint64(0)
	for _, c := range texto {
		seed = seed*31 + uint64(c)
	}
	vec := make([]float64, dim)
	for i := range vec {
		seed = seed*1664525 + 1013904223
		vec[i] = float64(seed)/float64(math.MaxUint64)*2 - 1
	}
	norma := 0.0
	for _, v := range vec {
		norma += v * v
	}
	norma = math.Sqrt(norma)
	for i := range vec {
		vec[i] /= norma
	}
	return vec
}

func cosineSim(a, b []float64) float64 {
	sum := 0.0
	for i := range a {
		sum += a[i] * b[i]
	}
	return sum
}

type VectorStore struct {
	store        map[string]Memoria
	indice       map[string][]float64
	indiceActivo bool
	embDim       int
}

func NuevoVectorStore(dim int) *VectorStore {
	return &VectorStore{
		store:        make(map[string]Memoria),
		indice:       make(map[string][]float64),
		indiceActivo: true,
		embDim:       dim,
	}
}

func (vs *VectorStore) Insertar(id, texto, tipo, fuente string) {
	vs.store[id] = Memoria{ID: id, Texto: texto, Tipo: tipo, Fuente: fuente, Creado: time.Now().Unix()}
	vs.indice[id] = mockEmbedding(texto, vs.embDim)
}

func (vs *VectorStore) Buscar(query string, k int) []ResultadoBusqueda {
	if vs.indiceActivo && len(vs.indice) > 0 {
		return vs.buscarSemantico(query, k)
	}
	return vs.buscarTexto(query, k)
}

func (vs *VectorStore) buscarSemantico(query string, k int) []ResultadoBusqueda {
	qEmb := mockEmbedding(query, vs.embDim)
	type par struct {
		id    string
		score float64
	}
	pares := make([]par, 0, len(vs.indice))
	for id, emb := range vs.indice {
		pares = append(pares, par{id, cosineSim(qEmb, emb)})
	}
	sort.Slice(pares, func(i, j int) bool { return pares[i].score > pares[j].score })
	if k > len(pares) {
		k = len(pares)
	}
	resultados := make([]ResultadoBusqueda, k)
	for i, p := range pares[:k] {
		s := p.score
		resultados[i] = ResultadoBusqueda{Memoria: vs.store[p.id], Score: &s}
	}
	return resultados
}

func (vs *VectorStore) buscarTexto(query string, k int) []ResultadoBusqueda {
	tokens := strings.Fields(strings.ToLower(query))
	var resultados []ResultadoBusqueda
	for _, m := range vs.store {
		texto := strings.ToLower(m.Texto)
		for _, t := range tokens {
			if strings.Contains(texto, t) {
				resultados = append(resultados, ResultadoBusqueda{Memoria: m})
				break
			}
		}
		if len(resultados) >= k {
			break
		}
	}
	return resultados
}

func (vs *VectorStore) SimularFalloIndice() {
	vs.indiceActivo = false
	fmt.Println("  [simulación] índice vectorial desactivado")
}

func main() {
	store := NuevoVectorStore(64)

	recuerdos := [][3]string{
		{"m1", "Ana es la directora de producto de Acme Corp", "hecho"},
		{"m2", "El proyecto Pegasus tiene deadline en junio", "hecho"},
		{"m3", "El presupuesto del Q3 fue aprobado por Ana", "decision"},
		{"m4", "La integración con Stripe es parte de Pegasus", "hecho"},
		{"m5", "El equipo usa Python y Go como lenguajes principales", "hecho"},
		{"m6", "La reunión de lanzamiento fue el 15 de marzo", "evento"},
		{"m7", "El bug en auth afecta a usuarios admin", "hallazgo"},
		{"m8", "Ana aprobó el roadmap del Q4 en la reunión del viernes", "decision"},
	}

	for _, r := range recuerdos {
		store.Insertar(r[0], r[1], r[2], "")
	}
	fmt.Printf("Total recuerdos insertados: %d\n\n", len(store.store))

	fmt.Println("Búsqueda semántica: 'proyectos con presupuesto aprobado'")
	resultados := store.Buscar("proyectos con presupuesto aprobado", 3)
	for _, r := range resultados {
		score := "nil"
		if r.Score != nil {
			score = fmt.Sprintf("%.3f", *r.Score)
		}
		fmt.Printf("  [%s] %s\n", score, r.Texto)
	}

	fmt.Println()
	store.SimularFalloIndice()
	fmt.Println("Búsqueda con degradación: 'Ana proyecto'")
	resultadosFTS := store.Buscar("Ana proyecto", 3)
	for _, r := range resultadosFTS {
		fmt.Printf("  [texto] %s\n", r.Texto)
	}
}
