// Retrieval como herramienta — el LLM decide cuándo y qué buscar con tool_use.
//
// En lugar de recuperar siempre antes de generar (RAG ingenuo), aquí el LLM
// recibe buscar_documentos como herramienta y decide si llamarla, cuántas veces,
// y con qué query. El agente itera hasta que produce texto final (end_turn)
// o alcanza el límite de seguridad de 5 iteraciones.
//
// TF-IDF cosine idéntico a rag_ingenuo — stdlib únicamente + HTTP directo a
// la API de Anthropic (sin SDK externo).

// Cómo ejecutar: make go FILE=go/11-rag/retrieval_herramienta.go

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"sort"
	"strings"
)

const (
	ragMaxIter = 5
)

var (
	ragModel = envOr("SMALL_MODEL", "claude-haiku-4-5-20251001")
	ragAPIURL = envBaseURL()
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
	"RAG-Anything extiende RAG a corpus multimodal con tablas, imágenes y ecuaciones.",
	"LightRAG construye un grafo de conocimiento con retrieval dual-level para multi-hop.",
}

// ── TF-IDF cosine ─────────────────────────────────────────────────────────────

func tokenizar(texto string) []string {
	return strings.Fields(strings.ToLower(texto))
}

func tfidfVector(tokens []string, df map[string]int, nDocs int) map[string]float64 {
	tf := make(map[string]int)
	for _, t := range tokens {
		tf[t]++
	}
	total := len(tokens)
	if total == 0 {
		total = 1
	}
	vec := make(map[string]float64)
	for term, count := range tf {
		tfScore := float64(count) / float64(total)
		idfScore := math.Log(float64(nDocs+1) / float64(df[term]+1))
		vec[term] = tfScore * idfScore
	}
	return vec
}

func cosineSim(v1, v2 map[string]float64) float64 {
	dot := 0.0
	for t, w := range v2 {
		dot += v1[t] * w
	}
	norm1, norm2 := 0.0, 0.0
	for _, x := range v1 {
		norm1 += x * x
	}
	for _, x := range v2 {
		norm2 += x * x
	}
	if norm1 == 0 || norm2 == 0 {
		return 0
	}
	return dot / (math.Sqrt(norm1) * math.Sqrt(norm2))
}

type indexEntry struct {
	chunk string
	vec   map[string]float64
}

func indexar(docs []string) ([]indexEntry, map[string]int) {
	tokenized := make([][]string, len(docs))
	for i, doc := range docs {
		tokenized[i] = tokenizar(doc)
	}
	nDocs := len(docs)

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

	index := make([]indexEntry, len(docs))
	for i, doc := range docs {
		index[i] = indexEntry{
			chunk: doc,
			vec:   tfidfVector(tokenized[i], df, nDocs),
		}
	}
	return index, df
}

func buscarDocumentos(query string, k int, index []indexEntry, df map[string]int) string {
	nDocs := len(index)
	qTokens := tokenizar(query)
	qVec := tfidfVector(qTokens, df, nDocs)

	type scored struct {
		chunk string
		score float64
	}
	scores := make([]scored, len(index))
	for i, entry := range index {
		scores[i] = scored{entry.chunk, cosineSim(qVec, entry.vec)}
	}
	sort.Slice(scores, func(i, j int) bool { return scores[i].score > scores[j].score })

	if k > len(scores) {
		k = len(scores)
	}
	lines := make([]string, k)
	for i, s := range scores[:k] {
		lines[i] = fmt.Sprintf("%d. %s (score=%.4f)", i+1, s.chunk, s.score)
	}
	return strings.Join(lines, "\n")
}

// ── Tipos para la API de Anthropic ────────────────────────────────────────────

// Los mensajes pueden contener texto, arrays de bloques o tool_results.
// Usamos json.RawMessage para poder serializar cualquier forma.
type ragMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// Bloque genérico de la respuesta del modelo
type ragBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   string          `json:"content,omitempty"`
}

type ragAPIResponse struct {
	Content    []ragBlock `json:"content"`
	StopReason string     `json:"stop_reason"`
	Error      *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// Herramienta registrada en la API
var ragTools = []map[string]interface{}{
	{
		"name":        "buscar_documentos",
		"description": "Busca en la base de conocimiento interna y devuelve los fragmentos más relevantes.",
		"input_schema": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"query": map[string]string{
					"type":        "string",
					"description": "Texto a buscar en la base de conocimiento.",
				},
				"k": map[string]interface{}{
					"type":        "integer",
					"description": "Número de fragmentos a recuperar (por defecto 3).",
				},
			},
			"required": []string{"query"},
		},
	},
}

const ragSystem = "Eres un asistente con acceso a una base de conocimiento. " +
	"Usa buscar_documentos cuando necesites información específica. " +
	"Responde directamente si ya tienes suficiente información."

// ── Llamada HTTP a la API ──────────────────────────────────────────────────────

func callAnthropic(messages []ragMessage) (*ragAPIResponse, error) {
	payload := map[string]interface{}{
		"model":      ragModel,
		"max_tokens": 1024,
		"system":     ragSystem,
		"tools":      ragTools,
		"messages":   messages,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(context.Background(), "POST", ragAPIURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-api-key", os.Getenv("ANTHROPIC_API_KEY"))
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("content-type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var r ragAPIResponse
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("parse error: %s — %w", string(data), err)
	}
	if r.Error != nil {
		return nil, fmt.Errorf("API error %s: %s", r.Error.Type, r.Error.Message)
	}
	return &r, nil
}

// ── Tool result block (para serializar hacia la API) ──────────────────────────

type toolResultBlock struct {
	Type      string `json:"type"`
	ToolUseID string `json:"tool_use_id"`
	Content   string `json:"content"`
}

// ── Agent loop ─────────────────────────────────────────────────────────────────

func agenteRag(pregunta string, index []indexEntry, df map[string]int) string {
	preguntaJSON, _ := json.Marshal(pregunta)
	messages := []ragMessage{{Role: "user", Content: preguntaJSON}}

	for iteracion := 0; iteracion < ragMaxIter; iteracion++ {
		resp, err := callAnthropic(messages)
		if err != nil {
			return err.Error()
		}

		fmt.Printf("\n[iter=%d] stop_reason=%s\n", iteracion+1, resp.StopReason)

		switch resp.StopReason {
		case "end_turn":
			for _, b := range resp.Content {
				if b.Type == "text" {
					return b.Text
				}
			}
			return "[sin texto en la respuesta]"

		case "tool_use":
			// Añadir la respuesta del asistente (con los tool_use blocks) al historial
			asstJSON, _ := json.Marshal(resp.Content)
			messages = append(messages, ragMessage{Role: "assistant", Content: asstJSON})

			// Ejecutar todas las tool calls y acumular resultados
			var toolResults []toolResultBlock
			for _, block := range resp.Content {
				if block.Type != "tool_use" {
					continue
				}

				// Parsear los argumentos de la herramienta
				var args struct {
					Query string `json:"query"`
					K     int    `json:"k"`
				}
				args.K = 3 // valor por defecto
				_ = json.Unmarshal(block.Input, &args)

				fmt.Printf("  → buscar_documentos(query=%q, k=%d)\n", args.Query, args.K)
				resultado := buscarDocumentos(args.Query, args.K, index, df)
				preview := resultado
				if len(preview) > 120 {
					preview = preview[:120]
				}
				fmt.Printf("  ← %s\n", strings.ReplaceAll(preview, "\n", " | "))

				toolResults = append(toolResults, toolResultBlock{
					Type:      "tool_result",
					ToolUseID: block.ID,
					Content:   resultado,
				})
			}

			// CRÍTICO: todos los tool_results en un único mensaje user
			userJSON, _ := json.Marshal(toolResults)
			messages = append(messages, ragMessage{Role: "user", Content: userJSON})

		default:
			fmt.Printf("  [warn] stop_reason inesperado: %s\n", resp.StopReason)
			return fmt.Sprintf("[stop_reason inesperado: %s]", resp.StopReason)
		}
	}

	return "[límite de iteraciones alcanzado]"
}

// ── Demo ───────────────────────────────────────────────────────────────────────

func main() {
	index, df := indexar(corpus)

	pregunta := "¿Qué diferencia hay entre RAG-Anything y LightRAG?"
	fmt.Printf("Pregunta: %s\n", pregunta)

	respuesta := agenteRag(pregunta, index, df)
	fmt.Printf("\n=== Respuesta final ===\n%s\n", respuesta)
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
