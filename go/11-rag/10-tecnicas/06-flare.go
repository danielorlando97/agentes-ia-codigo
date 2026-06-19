// FLARE: Forward-Looking Active Retrieval Augmented Generation (Jiang et al., 2023).
//
// FLARE genera texto en segmentos y, cuando la probabilidad de un token cae bajo
// un umbral, usa el texto tentativo como query de búsqueda y regenera el segmento
// con el contexto recuperado. Sin fine-tuning — solo logprobs nativos de la API.
//
// IMPORTANTE: Claude y Gemini no exponen logprobs — este archivo usa OpenAI.
// Compatible con: OpenAI API, modelos locales vía Ollama, HuggingFace.
//
// Cómo ejecutar:
//
//	export OPENAI_API_KEY=sk-...
//	make go FILE=go/11-rag/10-tecnicas/06-flare.go
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
)

var (
	model      = getenv("MODEL", "gpt-4o-mini")
	umbral     = getenvFloat("FLARE_UMBRAL", 0.2)
	maxIter    = getenvInt("FLARE_MAX_ITER", 6)
	openaiKey  = os.Getenv("OPENAI_API_KEY")
	openaiBase = strings.TrimRight(getenv("OPENAI_BASE_URL", "https://api.openai.com"), "/")
)

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getenvFloat(key string, def float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
}

func getenvInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

// ── Corpus y retriever mock ────────────────────────────────────────────────

var corpus = []string{
	"RAG combina recuperación de documentos con generación del LLM para reducir alucinaciones.",
	"Self-RAG fine-tunea el modelo para emitir tokens especiales que controlan el retrieval.",
	"FLARE activa el retrieval cuando la probabilidad de un token cae bajo un umbral configurable.",
	"BM25 es una función de recuperación léxica basada en frecuencia de término e IDF.",
	"Advanced RAG usa BM25 + búsqueda semántica + RRF para mejorar el recall.",
	"La ventana de contexto de Claude 3 llega a 200 000 tokens.",
	"Los modelos de lenguaje tienden a alucinar hechos fuera de su distribución de entrenamiento.",
	"GraphRAG construye un grafo de entidades sobre el corpus antes de recuperar.",
	"Los logprobs permiten medir la incertidumbre del modelo token a token.",
	"FLARE-direct usa la tentativa de generación como query sin reformulación adicional.",
}

func tokenizar(texto string) []string {
	return strings.Fields(strings.ToLower(texto))
}

func bm25TopK(query string, k int) []string {
	tokenized := make([][]string, len(corpus))
	for i, doc := range corpus {
		tokenized[i] = tokenizar(doc)
	}
	n := float64(len(corpus))
	df := map[string]float64{}
	for _, tokens := range tokenized {
		seen := map[string]bool{}
		for _, t := range tokens {
			if !seen[t] {
				df[t]++
				seen[t] = true
			}
		}
	}
	var avgdl float64
	for _, t := range tokenized {
		avgdl += float64(len(t))
	}
	avgdl /= n
	const k1, b = 1.5, 0.75
	qTokens := tokenizar(query)
	type scored struct {
		doc   string
		score float64
	}
	scores := make([]scored, len(corpus))
	for i, tokens := range tokenized {
		tf := map[string]int{}
		for _, t := range tokens {
			tf[t]++
		}
		dl := float64(len(tokens))
		var total float64
		for _, term := range qTokens {
			dfTerm, ok := df[term]
			if !ok {
				continue
			}
			idf := math.Log((n-dfTerm+0.5)/(dfTerm+0.5) + 1)
			freq := float64(tf[term])
			total += idf * (freq * (k1 + 1)) / (freq + k1*(1-b+b*dl/avgdl))
		}
		scores[i] = scored{corpus[i], total}
	}
	// sort descending
	for i := 0; i < len(scores)-1; i++ {
		for j := i + 1; j < len(scores); j++ {
			if scores[j].score > scores[i].score {
				scores[i], scores[j] = scores[j], scores[i]
			}
		}
	}
	var out []string
	for _, s := range scores[:min(k, len(scores))] {
		if s.score > 0 {
			out = append(out, s.doc)
		}
	}
	return out
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ── Generación con logprobs (OpenAI API) ──────────────────────────────────

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type tokenLogprob struct {
	Token   string  `json:"token"`
	Logprob float64 `json:"logprob"`
}

type logprobsContent struct {
	Content []tokenLogprob `json:"content"`
}

type chatChoice struct {
	Message struct {
		Content string `json:"content"`
	} `json:"message"`
	Logprobs *logprobsContent `json:"logprobs"`
}

type chatResponse struct {
	Choices []chatChoice `json:"choices"`
}

func generarConLogprobs(prompt string, maxTokens int) (string, float64, error) {
	payload := map[string]interface{}{
		"model":      model,
		"messages":   []chatMessage{{Role: "user", Content: prompt}},
		"max_tokens": maxTokens,
		"logprobs":   true,
		"temperature": 0,
	}
	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", openaiBase+"/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+openaiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return "", 0, fmt.Errorf("OpenAI API error %d: %s", resp.StatusCode, data)
	}

	var cr chatResponse
	if err := json.Unmarshal(data, &cr); err != nil || len(cr.Choices) == 0 {
		return "", 0, fmt.Errorf("respuesta inválida: %s", data)
	}

	texto := cr.Choices[0].Message.Content
	minLP := 0.0
	if lp := cr.Choices[0].Logprobs; lp != nil && len(lp.Content) > 0 {
		minLP = lp.Content[0].Logprob
		for _, t := range lp.Content[1:] {
			if t.Logprob < minLP {
				minLP = t.Logprob
			}
		}
	}
	return texto, minLP, nil
}

// ── FLARE-direct ──────────────────────────────────────────────────────────

func flare(query string) (string, error) {
	var segmentosAceptados []string
	var contextoActual []string

	for i := 1; i <= maxIter; i++ {
		contextoStr := strings.Join(contextoActual, "\n")
		previoStr   := strings.Join(segmentosAceptados, " ")

		var sb strings.Builder
		sb.WriteString("Responde en español de forma factual y concisa. ")
		if contextoStr != "" {
			sb.WriteString("Contexto recuperado: " + contextoStr + "\n")
		}
		if previoStr != "" {
			sb.WriteString("Respuesta parcial hasta ahora: " + previoStr + "\n")
		}
		sb.WriteString("Pregunta: " + query + "\n")
		sb.WriteString("Continúa la respuesta (máximo 2 oraciones):")

		tentativa, minLP, err := generarConLogprobs(sb.String(), 80)
		if err != nil {
			return "", err
		}

		preview := tentativa
		if len(preview) > 60 {
			preview = preview[:60]
		}
		fmt.Printf("  [%d] confianza mín: %.3f  |  tentativa: %q\n", i, minLP, preview)

		segmento := tentativa

		if minLP < umbral {
			chunks := bm25TopK(tentativa, 2)
			if len(chunks) > 0 {
				contextoActual = chunks
				fmt.Printf("       → retrieval activado (%d chunks). Regenerando...\n", len(chunks))
				var sb2 strings.Builder
				sb2.WriteString("Responde en español de forma factual y concisa. ")
				sb2.WriteString("Contexto: " + strings.Join(chunks, "\n") + "\n")
				if previoStr != "" {
					sb2.WriteString("Respuesta parcial: " + previoStr + "\n")
				}
				sb2.WriteString("Pregunta: " + query + "\n")
				sb2.WriteString("Continúa la respuesta (máximo 2 oraciones):")
				regen, _, err := generarConLogprobs(sb2.String(), 80)
				if err != nil {
					return "", err
				}
				segmento = regen
			}
		}

		segmentosAceptados = append(segmentosAceptados, strings.TrimSpace(segmento))

		termina := strings.ContainsAny(segmento, ".!?")
		total := strings.Fields(strings.Join(segmentosAceptados, " "))
		if termina && len(total) > 20 {
			break
		}
	}
	return strings.Join(segmentosAceptados, " "), nil
}

// ── Main ──────────────────────────────────────────────────────────────────

func main() {
	if openaiKey == "" {
		fmt.Fprintln(os.Stderr, "OPENAI_API_KEY no está definido en el entorno.")
		os.Exit(1)
	}

	preguntas := []string{
		"¿Qué es FLARE y cómo se diferencia de Self-RAG?",
		"¿Cuándo se activa el retrieval en FLARE y qué hace el modelo con los chunks?",
	}

	for _, pregunta := range preguntas {
		fmt.Printf("\nPregunta: %s\n", pregunta)
		fmt.Printf("Umbral logprob: %.2f | Modelo: %s\n\n", umbral, model)
		respuesta, err := flare(pregunta)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			continue
		}
		fmt.Printf("\nRespuesta final: %s\n\n", respuesta)
		fmt.Println(strings.Repeat("-", 70))
	}
}
