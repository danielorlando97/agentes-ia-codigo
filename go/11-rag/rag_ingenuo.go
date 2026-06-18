// Naive RAG con TF-IDF cosine similarity — solo stdlib + llamada HTTP directa a Anthropic API
// Indexa un corpus hardcodeado, recupera top-3 chunks, genera respuesta con Claude Haiku.

// Cómo ejecutar: make go FILE=go/11-rag/rag_ingenuo.go


package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"sort"
	"strings"
)

var corpus = []string{
	"Los modelos de lenguaje transformers usan mecanismo de atención para procesar texto.",
	"El contexto de un LLM es la ventana de tokens que puede procesar en una sola inferencia.",
	"RAG combina recuperación de documentos con generación del LLM para reducir alucinaciones.",
	"El chunking divide documentos largos en fragmentos manejables para el vector store.",
	"La similitud coseno mide el ángulo entre dos vectores en el espacio de embeddings.",
	"Los embeddings mapean texto a vectores numéricos en un espacio semántico continuo.",
	"El reranking reordena los candidatos recuperados usando un modelo más preciso.",
	"BM25 es una función de recuperación basada en TF-IDF mejorada para búsqueda exacta.",
}

type indexEntry struct {
	chunk string
	vec   map[string]float64
}

// tokenizar divide el texto en tokens en minúsculas.
func tokenizar(texto string) []string {
	return strings.Fields(strings.ToLower(texto))
}

// tfidfVector calcula el vector TF-IDF de un slice de tokens.
func tfidfVector(tokens []string, df map[string]int, nDocs int) map[string]float64 {
	tf := make(map[string]int)
	for _, t := range tokens {
		tf[t]++
	}
	total := len(tokens)
	if total == 0 {
		total = 1
	}
	vec := make(map[string]float64, len(tf))
	for term, count := range tf {
		tfScore := float64(count) / float64(total)
		idfScore := math.Log(float64(nDocs+1) / float64(df[term]+1))
		vec[term] = tfScore * idfScore
	}
	return vec
}

// cosineSim calcula la similitud coseno entre dos vectores TF-IDF.
func cosineSim(v1, v2 map[string]float64) float64 {
	var dot, norm1, norm2 float64
	for term, s2 := range v2 {
		dot += v1[term] * s2
	}
	for _, s := range v1 {
		norm1 += s * s
	}
	for _, s := range v2 {
		norm2 += s * s
	}
	if norm1 == 0 || norm2 == 0 {
		return 0
	}
	return dot / (math.Sqrt(norm1) * math.Sqrt(norm2))
}

// indexar construye el índice TF-IDF sobre el corpus.
func indexar(docs []string) ([]indexEntry, map[string]int) {
	tokenized := make([][]string, len(docs))
	for i, doc := range docs {
		tokenized[i] = tokenizar(doc)
	}

	df := make(map[string]int)
	for _, tokens := range tokenized {
		seen := make(map[string]bool)
		for _, t := range tokens {
			if !seen[t] {
				df[t]++
				seen[t] = true
			}
		}
	}

	nDocs := len(docs)
	index := make([]indexEntry, nDocs)
	for i, doc := range docs {
		index[i] = indexEntry{
			chunk: doc,
			vec:   tfidfVector(tokenized[i], df, nDocs),
		}
	}
	return index, df
}

// buscar devuelve los top-k chunks más relevantes para la query.
func buscar(query string, index []indexEntry, df map[string]int, k int) []string {
	nDocs := len(index)
	qTokens := tokenizar(query)
	qVec := tfidfVector(qTokens, df, nDocs)

	type scored struct {
		chunk string
		score float64
	}
	scores := make([]scored, len(index))
	for i, entry := range index {
		scores[i] = scored{chunk: entry.chunk, score: cosineSim(qVec, entry.vec)}
	}
	sort.Slice(scores, func(i, j int) bool { return scores[i].score > scores[j].score })

	result := make([]string, 0, k)
	for _, s := range scores[:k] {
		result = append(result, s.chunk)
	}
	return result
}

// --- Anthropic API types ---

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	System    string             `json:"system"`
	Messages  []anthropicMessage `json:"messages"`
}

type anthropicContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type anthropicResponse struct {
	Content []anthropicContent `json:"content"`
}

// ragIngenuo recupera contexto y consulta el LLM de Anthropic.
func ragIngenuo(query string, index []indexEntry, df map[string]int, apiKey string) (string, error) {
	topChunks := buscar(query, index, df, 3)

	var sb strings.Builder
	for _, c := range topChunks {
		sb.WriteString("- ")
		sb.WriteString(c)
		sb.WriteString("\n")
	}
	contexto := sb.String()

	reqBody := anthropicRequest{
		Model:     envOr("SMALL_MODEL", "claude-haiku-4-5-20251001"),
		MaxTokens: 300,
		System:    "Responde usando solo el contexto proporcionado. Si la respuesta no está en el contexto, dilo explícitamente.",
		Messages: []anthropicMessage{
			{
				Role:    "user",
				Content: fmt.Sprintf("Contexto:\n%s\nPregunta: %s", contexto, query),
			},
		},
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, envBaseURL(), bytes.NewReader(bodyBytes))
	if err != nil {
		return "", fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("http do: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("API error %d: %s", resp.StatusCode, string(respBytes))
	}

	var ar anthropicResponse
	if err := json.Unmarshal(respBytes, &ar); err != nil {
		return "", fmt.Errorf("unmarshal response: %w", err)
	}
	if len(ar.Content) == 0 {
		return "", fmt.Errorf("respuesta vacía del LLM")
	}
	return ar.Content[0].Text, nil
}

func main() {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		log.Fatal("ANTHROPIC_API_KEY no está definida")
	}

	index, df := indexar(corpus)

	query := "¿Qué es RAG y para qué sirve?"
	fmt.Printf("Query: %s\n\n", query)

	top := buscar(query, index, df, 3)
	fmt.Println("Chunks recuperados:")
	for i, chunk := range top {
		fmt.Printf("  %d. %s\n", i+1, chunk)
	}
	fmt.Println()

	respuesta, err := ragIngenuo(query, index, df, apiKey)
	if err != nil {
		log.Fatalf("Error: %v", err)
	}
	fmt.Printf("Respuesta:\n%s\n", respuesta)
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
