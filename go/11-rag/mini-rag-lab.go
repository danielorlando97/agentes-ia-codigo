// Mini-proyecto: El RAG lab (Go).
//
// Pipeline RAG con TF-IDF / BM25 / reranking simulado — sin API key.
//
// Uso:
//
//	go run mini-rag-lab.go
//	go run mini-rag-lab.go -tecnica bm25
//	go run mini-rag-lab.go -tecnica todos -query "¿qué es un agente?"
//	go run mini-rag-lab.go -top-k 5

// Cómo ejecutar: make go FILE=go/11-rag/mini-rag-lab.go

package main

import (
	"flag"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// ── corpus de ejemplo ─────────────────────────────────────────────────────────

var corpusEjemplo = strings.TrimSpace(`
Un agente de IA es un sistema que percibe su entorno mediante herramientas y actúa para alcanzar objetivos. A diferencia de un chatbot simple, un agente puede planificar, ejecutar acciones y adaptarse a resultados inesperados.

Los agentes modernos se construyen sobre modelos de lenguaje (LLM). El LLM actúa como motor de razonamiento: decide qué hacer a continuación basándose en el contexto acumulado y las herramientas disponibles.

Las herramientas son funciones que el agente puede invocar: buscar en internet, leer archivos, ejecutar código, consultar bases de datos. Cada herramienta tiene un nombre, una descripción y un esquema de parámetros en JSON.

El loop ReAct (Reason-Act-Observe) es el patrón más común: el modelo razona sobre la situación, decide una acción, ejecuta la herramienta, observa el resultado, y repite hasta completar la tarea.

La memoria de un agente tiene varias capas: la ventana de contexto inmediata, el historial de la sesión episódica, y una base de conocimiento recuperable semántica. Cada capa tiene características distintas de capacidad, latencia y costo.

RAG (Retrieval-Augmented Generation) combina recuperación de documentos relevantes con generación del modelo. En lugar de depender solo del conocimiento interno del LLM, RAG busca fragmentos pertinentes en un corpus y los incluye en el contexto.

El chunking divide el corpus en fragmentos manejables antes de indexarlos. La estrategia afecta la calidad del retrieval: chunks muy pequeños pierden contexto; chunks muy grandes diluyen la señal de relevancia.

TF-IDF (Term Frequency - Inverse Document Frequency) es una técnica clásica de recuperación de información. Pondera los términos según su frecuencia en el documento y su rareza en el corpus.

BM25 es una mejora sobre TF-IDF que normaliza por longitud de documento y aplica saturación a la frecuencia de término. En benchmarks supera a TF-IDF vanilla en 5-15% de precisión para queries cortas.

El reranking es un segundo paso de ordenación sobre los candidatos del primer retrieval. Un reranker evalúa la relevancia entre query y fragmento juntos, en lugar de comparar embeddings por separado.

Los embeddings vectoriales representan texto como vectores de alta dimensión. La similitud coseno mide qué tan semánticamente cercanos son sus textos originales.

Los índices vectoriales como FAISS, Chroma o Pinecone permiten búsqueda de k-vecinos más cercanos en millones de vectores en milisegundos usando algoritmos aproximados.

El naive RAG falla en cuatro escenarios: queries ambiguas, fragmentos sin contexto, ranking por similitud diferente a ranking por utilidad, y alucinación a pesar del contexto recuperado.

Graph RAG construye un grafo de entidades y relaciones sobre el corpus. Permite responder preguntas de síntesis global. El costo de indexado puede superar los 100 dólares para corpus grandes.

El agente puede usar retrieval como herramienta en lugar de como pipeline fijo. Esto le permite decidir si buscar, qué buscar, y refinar la query. El costo en tokens es 3-4 veces mayor que el pipeline fijo.
`)

// ── tipos ─────────────────────────────────────────────────────────────────────

type Chunk struct {
	id     int
	texto  string
	tokens int
}

type ChunkScore struct {
	Chunk
	score float64
}

type Indice struct {
	corpusTokens [][]string
	idf          map[string]float64
	avgLen       float64
}

// ── chunking ──────────────────────────────────────────────────────────────────

func chunkingParrafos(texto string) []Chunk {
	var chunks []Chunk
	for i, p := range strings.Split(texto, "\n\n") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		chunks = append(chunks, Chunk{id: i, texto: p, tokens: len([]rune(p)) / 4})
	}
	return chunks
}

var reNoAlpha = regexp.MustCompile(`[^a-záéíóúüñ\s]`)

func tokenizarTexto(texto string) []string {
	texto = strings.ToLower(texto)
	// normalizar caracteres especiales manualmente para compatibilidad
	texto = reNoAlpha.ReplaceAllString(texto, " ")
	var tokens []string
	for _, t := range strings.Fields(texto) {
		runes := []rune(t)
		if len(runes) > 2 {
			tokens = append(tokens, t)
		}
	}
	return tokens
}

// ── TF-IDF ────────────────────────────────────────────────────────────────────

func calcularTF(tokens []string) map[string]float64 {
	conteo := make(map[string]int)
	for _, t := range tokens {
		conteo[t]++
	}
	tf := make(map[string]float64)
	total := len(tokens)
	for t, c := range conteo {
		tf[t] = float64(c) / float64(total)
	}
	return tf
}

func calcularIDF(corpusTokens [][]string) map[string]float64 {
	n := len(corpusTokens)
	df := make(map[string]int)
	for _, tokens := range corpusTokens {
		seen := make(map[string]bool)
		for _, t := range tokens {
			if !seen[t] {
				df[t]++
				seen[t] = true
			}
		}
	}
	idf := make(map[string]float64)
	for t, d := range df {
		idf[t] = math.Log(float64(n+1)/float64(d+1)) + 1
	}
	return idf
}

func tfidfScore(queryTokens, chunkTokens []string, idf map[string]float64) float64 {
	tf := calcularTF(chunkTokens)
	score := 0.0
	for _, qt := range queryTokens {
		score += tf[qt] * idf[qt]
	}
	return score
}

// ── BM25 ──────────────────────────────────────────────────────────────────────

func bm25Score(queryTokens, chunkTokens []string, idf map[string]float64, avgLen, k1, b float64) float64 {
	conteo := make(map[string]int)
	for _, t := range chunkTokens {
		conteo[t]++
	}
	dl := float64(len(chunkTokens))
	score := 0.0
	for _, qt := range queryTokens {
		tf := float64(conteo[qt])
		idfVal := idf[qt]
		num := tf * (k1 + 1)
		den := tf + k1*(1-b+b*dl/avgLen)
		if den > 0 {
			score += idfVal * (num / den)
		}
	}
	return score
}

// ── reranker simulado ─────────────────────────────────────────────────────────

func rerankScore(queryTokens []string, chunkTexto string) float64 {
	chunkTokens := tokenizarTexto(chunkTexto)
	chunkSet := make(map[string]bool)
	for _, t := range chunkTokens {
		chunkSet[t] = true
	}
	matches := 0
	for _, qt := range queryTokens {
		if chunkSet[qt] {
			matches++
		}
	}
	cobertura := float64(matches) / math.Max(float64(len(queryTokens)), 1)

	densMatches := 0
	for _, ct := range chunkTokens {
		for _, qt := range queryTokens {
			if ct == qt {
				densMatches++
			}
		}
	}
	densidad := float64(densMatches) / math.Max(float64(len(chunkTokens)), 1)

	n := len(chunkTokens)
	var longScore float64
	switch {
	case n < 10:
		longScore = 0.3
	case n < 50:
		longScore = 0.7
	case n <= 200:
		longScore = 1.0
	default:
		longScore = math.Max(0.5, 1.0-float64(n-200)/400)
	}

	return 0.5*cobertura + 0.3*densidad + 0.2*longScore
}

// ── pipeline ──────────────────────────────────────────────────────────────────

func construirIndice(chunks []Chunk) Indice {
	corpusTokens := make([][]string, len(chunks))
	for i, c := range chunks {
		corpusTokens[i] = tokenizarTexto(c.texto)
	}
	idf := calcularIDF(corpusTokens)
	total := 0
	for _, t := range corpusTokens {
		total += len(t)
	}
	avgLen := float64(total) / math.Max(float64(len(corpusTokens)), 1)
	return Indice{corpusTokens: corpusTokens, idf: idf, avgLen: avgLen}
}

func recuperar(query string, chunks []Chunk, indice Indice, tecnica string, topK int) []ChunkScore {
	queryTokens := tokenizarTexto(query)

	type pair struct {
		score float64
		idx   int
	}
	scores := make([]pair, len(chunks))
	for i := range chunks {
		ct := indice.corpusTokens[i]
		var score float64
		switch tecnica {
		case "tfidf":
			score = tfidfScore(queryTokens, ct, indice.idf)
		case "bm25", "rerank":
			score = bm25Score(queryTokens, ct, indice.idf, indice.avgLen, 1.5, 0.75)
		}
		scores[i] = pair{score, i}
	}
	sort.Slice(scores, func(i, j int) bool { return scores[i].score > scores[j].score })

	if tecnica == "rerank" {
		limit := topK * 3
		if limit > len(scores) {
			limit = len(scores)
		}
		candidatos := scores[:limit]
		rerankPairs := make([]pair, len(candidatos))
		for k, p := range candidatos {
			rerankPairs[k] = pair{rerankScore(queryTokens, chunks[p.idx].texto), p.idx}
		}
		sort.Slice(rerankPairs, func(i, j int) bool { return rerankPairs[i].score > rerankPairs[j].score })
		if topK > len(rerankPairs) {
			topK = len(rerankPairs)
		}
		result := make([]ChunkScore, topK)
		for i := range result {
			result[i] = ChunkScore{chunks[rerankPairs[i].idx], rerankPairs[i].score}
		}
		return result
	}

	if topK > len(scores) {
		topK = len(scores)
	}
	result := make([]ChunkScore, topK)
	for i := range result {
		result[i] = ChunkScore{chunks[scores[i].idx], scores[i].score}
	}
	return result
}

// ── presentación ──────────────────────────────────────────────────────────────

func truncar(texto string, n int) string {
	runes := []rune(texto)
	if len(runes) <= n {
		return texto
	}
	return string(runes[:n-1]) + "…"
}

func imprimirResultados(query, tecnica string, fragmentos []ChunkScore) {
	fmt.Printf("\n  Técnica: %s   |   query: %q\n", strings.ToUpper(tecnica), query)
	fmt.Printf("  %s\n", strings.Repeat("-", 56))
	for i, f := range fragmentos {
		fmt.Printf("  [%d] score=%.4f  tokens=%d\n", i+1, f.score, f.tokens)
		fmt.Printf("      %s\n", truncar(f.texto, 70))
	}
	fmt.Println()
}

func compararTecnicas(query string, chunks []Chunk, indice Indice, topK int) {
	fmt.Printf("\n%s\n", strings.Repeat("=", 64))
	fmt.Println("  RAG LAB — Comparativa de técnicas de retrieval")
	fmt.Printf("  Query: %q\n", query)
	fmt.Printf("  Corpus: %d fragmentos  |  top-k: %d\n", len(chunks), topK)
	fmt.Println(strings.Repeat("=", 64))

	tecnicas := []string{"tfidf", "bm25", "rerank"}
	todosResultados := make(map[string][]ChunkScore)
	for _, t := range tecnicas {
		res := recuperar(query, chunks, indice, t, topK)
		todosResultados[t] = res
		imprimirResultados(query, t, res)
	}

	fmt.Printf("  %s\n", strings.Repeat("─", 56))
	fmt.Println("  Acuerdo entre técnicas (fragmentos recuperados por múltiples)")
	fmt.Printf("  %s\n", strings.Repeat("─", 56))
	conteo := make(map[int]int)
	for _, res := range todosResultados {
		for _, f := range res {
			conteo[f.id]++
		}
	}
	type kv struct{ id, count int }
	var sorted []kv
	for id, c := range conteo {
		if c > 1 {
			sorted = append(sorted, kv{id, c})
		}
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].count > sorted[j].count })
	if len(sorted) == 0 {
		fmt.Println("  (No hay acuerdo entre técnicas para esta query)")
	} else {
		for _, kv := range sorted {
			fmt.Printf("  [%d] ×%d técnicas: %s\n", kv.id, kv.count, truncar(chunks[kv.id].texto, 55))
		}
	}
}

func modoUnico(query string, chunks []Chunk, indice Indice, tecnica string, topK int) {
	fragmentos := recuperar(query, chunks, indice, tecnica, topK)
	fmt.Printf("\n%s\n", strings.Repeat("=", 64))
	fmt.Printf("  RAG LAB — %s\n", strings.ToUpper(tecnica))
	fmt.Printf("  Query: %q\n", query)
	fmt.Printf("  Corpus: %d fragmentos  |  top-k: %d\n", len(chunks), topK)
	fmt.Println(strings.Repeat("=", 64))
	imprimirResultados(query, tecnica, fragmentos)
	nTok := 0
	for _, f := range fragmentos {
		if f.id < 3 {
			nTok += f.tokens
		}
	}
	fmt.Printf("  Respuesta: [Simulada — contexto de %d fragmentos, ~%d tokens]\n", len(fragmentos), nTok)
}

// ── main ──────────────────────────────────────────────────────────────────────

func main() {
	tecnica := flag.String("tecnica", "todos", "Técnica: tfidf|bm25|rerank|todos")
	query := flag.String("query", "¿qué es un agente de IA?", "Query de búsqueda")
	topK := flag.Int("top-k", 3, "Número de fragmentos a recuperar")
	flag.Parse()

	_ = unicode.IsLetter // evitar unused import
	fmt.Println("[Usando corpus de ejemplo sobre agentes IA]\n")

	chunks := chunkingParrafos(corpusEjemplo)
	indice := construirIndice(chunks)

	totalTokens := 0
	for _, c := range chunks {
		totalTokens += c.tokens
	}
	fmt.Printf("[Corpus: %d fragmentos, %d tokens totales]\n", len(chunks), totalTokens)
	fmt.Printf("[Vocabulario: %d términos únicos]\n", len(indice.idf))

	if *tecnica == "todos" {
		compararTecnicas(*query, chunks, indice, *topK)
	} else {
		modoUnico(*query, chunks, indice, *tecnica, *topK)
	}
}
