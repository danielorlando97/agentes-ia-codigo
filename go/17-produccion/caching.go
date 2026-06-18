// Tres capas de caching: prompt caching (Anthropic), response caching, embedding caching

// Cómo ejecutar: make go FILE=go/17-produccion/caching.go

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

var cachingModel = envOr("MODEL", "claude-sonnet-4-6")

var guiaEstilo = strings.Repeat("Regla 1: usa nombres descriptivos.\nRegla 2: máximo 80 chars por línea.\n", 50)

// ─── Estructuras de respuesta ─────────────────────────────────────────────────

type cachingRequest struct {
	Model     string        `json:"model"`
	MaxTokens int           `json:"max_tokens"`
	System    interface{}   `json:"system,omitempty"`
	Messages  []cachingMsg  `json:"messages"`
}

type cachingMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type cachingResponse struct {
	Content []struct {
		Text string `json:"text"`
	} `json:"content"`
	Usage struct {
		InputTokens               int `json:"input_tokens"`
		OutputTokens              int `json:"output_tokens"`
		CacheCreationInputTokens  int `json:"cache_creation_input_tokens"`
		CacheReadInputTokens      int `json:"cache_read_input_tokens"`
	} `json:"usage"`
}

func llamarAnthropicCaching(payload interface{}) (cachingResponse, error) {
	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", envBaseURL(), bytes.NewReader(body))
	req.Header.Set("x-api-key", os.Getenv("ANTHROPIC_API_KEY"))
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("anthropic-beta", "prompt-caching-2024-07-31")
	req.Header.Set("content-type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return cachingResponse{}, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	var ar cachingResponse
	return ar, json.Unmarshal(data, &ar)
}

// ─── Capa 1: Prompt caching ───────────────────────────────────────────────────

func revisarConPromptCache(codigo string) error {
	systemConCache := []map[string]interface{}{
		{
			"type":          "text",
			"text":          "Eres un revisor de código experto.\n\n" + guiaEstilo,
			"cache_control": map[string]string{"type": "ephemeral"},
		},
	}
	payload := map[string]interface{}{
		"model":      cachingModel,
		"max_tokens": 512,
		"system":     systemConCache,
		"messages":   []cachingMsg{{Role: "user", Content: "Revisa este código:\n" + codigo}},
	}

	ar, err := llamarAnthropicCaching(payload)
	if err != nil {
		return err
	}
	cacheHit := ar.Usage.CacheReadInputTokens > 0
	fmt.Printf("[cache_prompt] hit=%v | creation=%d | read=%d\n",
		cacheHit, ar.Usage.CacheCreationInputTokens, ar.Usage.CacheReadInputTokens)
	return nil
}

// ─── Capa 2: Response caching ─────────────────────────────────────────────────

type cacheEntry struct {
	value string
	ts    time.Time
}

var (
	responseCache = map[string]cacheEntry{}
	responseMu    sync.Mutex
)

func responderFaq(pregunta string) (string, error) {
	h := sha256.Sum256([]byte(pregunta))
	clave := fmt.Sprintf("%x", h)

	responseMu.Lock()
	if entry, ok := responseCache[clave]; ok && time.Since(entry.ts) < 300*time.Second {
		responseMu.Unlock()
		fmt.Println("[cache_response] hit")
		return entry.value, nil
	}
	responseMu.Unlock()

	ar, err := llamarAnthropicCaching(map[string]interface{}{
		"model":      cachingModel,
		"max_tokens": 256,
		"messages":   []cachingMsg{{Role: "user", Content: pregunta}},
	})
	if err != nil {
		return "", err
	}
	resultado := ar.Content[0].Text

	responseMu.Lock()
	responseCache[clave] = cacheEntry{value: resultado, ts: time.Now()}
	responseMu.Unlock()

	fmt.Println("[cache_response] miss — respuesta guardada")
	return resultado, nil
}

// ─── Capa 3: Semantic caching ─────────────────────────────────────────────────

func embeddingStub(texto string) []float64 {
	var h int64
	for _, c := range texto {
		h = h*31 + int64(c)
	}
	val := float64(abs64(h)%100) / 100.0
	result := make([]float64, 10)
	for i := range result {
		result[i] = val
	}
	return result
}

func abs64(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}

func similitudCoseno(a, b []float64) float64 {
	var dot, normA, normB float64
	for i := range a {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}

type semanticEntry struct {
	emb      []float64
	query    string
	respuesta string
}

var semanticCache []semanticEntry

const umbralSimilitud = 0.95

func responderSemantico(pregunta string) (string, error) {
	emb := embeddingStub(pregunta)

	for _, entry := range semanticCache {
		sim := similitudCoseno(emb, entry.emb)
		if sim >= umbralSimilitud {
			fmt.Printf("[cache_semantic] hit (similitud=%.3f, query original='%s')\n", sim, entry.query)
			return entry.respuesta, nil
		}
	}

	ar, err := llamarAnthropicCaching(map[string]interface{}{
		"model":      cachingModel,
		"max_tokens": 256,
		"messages":   []cachingMsg{{Role: "user", Content: pregunta}},
	})
	if err != nil {
		return "", err
	}
	texto := ar.Content[0].Text
	semanticCache = append(semanticCache, semanticEntry{emb: emb, query: pregunta, respuesta: texto})
	fmt.Println("[cache_semantic] miss — respuesta guardada")
	return texto, nil
}

func main() {
	fmt.Println("=== Prompt caching ===")
	codigo := "def f(x):\n    return x*2"
	if err := revisarConPromptCache(codigo); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
	}
	if err := revisarConPromptCache(codigo); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
	}

	fmt.Println("\n=== Response caching ===")
	responderFaq("¿Cuál es la política de devoluciones?")
	responderFaq("¿Cuál es la política de devoluciones?")

	fmt.Println("\n=== Semantic caching ===")
	responderSemantico("¿Qué hace filter_context?")
	responderSemantico("¿Qué hace filter_context?")
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
