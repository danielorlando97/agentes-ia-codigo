// ContextManager con conteo exacto de tokens y compactación LLM como fallback.
// - countTokensExact: llamada HTTP al endpoint /v1/messages/count_tokens
// - Compactación LLM: cuando FIFO no basta, un modelo barato resume el historial
// - ContextMetrics: fifoEvictions, llmCompactions, tokensSaved

// Cómo ejecutar: make go FILE=go/06-memoria/02-corto-plazo/nivel-4-completo.go

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

const (
	apiBase      = "https://api.anthropic.com/v1"
)

var (
	model = envOr("MODEL", "claude-sonnet-4-6")
	compactModel = envOr("SMALL_MODEL", "claude-haiku-4-5-20251001")
)

type Mensaje struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	Pinned  bool   `json:"-"`
}

type ContextBudget struct {
	Total     int
	System    int
	Retrieved int
	Tools     int
	Response  int
	Threshold float64
}

func NewContextBudget() ContextBudget {
	return ContextBudget{
		Total: 128_000, System: 4_000, Retrieved: 3_000,
		Tools: 2_000, Response: 8_000, Threshold: 0.75,
	}
}

func (b ContextBudget) History() int {
	return b.Total - b.System - b.Retrieved - b.Tools - b.Response
}

func (b ContextBudget) CompactTrigger() int {
	return int(float64(b.History()) * b.Threshold)
}

type ContextMetrics struct {
	FifoEvictions  int
	LlmCompactions int
	TokensSaved    int
}

type ContextManager struct {
	apiKey       string
	budget       ContextBudget
	systemPrompt string
	Metrics      ContextMetrics
}

func NewContextManager(apiKey string, budget ContextBudget, systemPrompt string) *ContextManager {
	return &ContextManager{apiKey: apiKey, budget: budget, systemPrompt: systemPrompt}
}

func (cm *ContextManager) doPost(endpoint string, payload interface{}) ([]byte, error) {
	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", apiBase+endpoint, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", cm.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

func (cm *ContextManager) countTokensExact(messages []Mensaje) int {
	payload := map[string]interface{}{
		"model":    model,
		"system":   cm.systemPrompt,
		"messages": messages,
	}
	data, err := cm.doPost("/messages/count_tokens", payload)
	if err != nil {
		return cm.estimate(messages)
	}
	var result struct {
		InputTokens int `json:"input_tokens"`
	}
	if err := json.Unmarshal(data, &result); err != nil || result.InputTokens == 0 {
		return cm.estimate(messages)
	}
	return result.InputTokens
}

func (cm *ContextManager) estimate(messages []Mensaje) int {
	total := 0
	for _, m := range messages {
		b, _ := json.Marshal(m)
		total += len(b)
	}
	return total / 4
}

func (cm *ContextManager) fifoReduce(messages []Mensaje, budget int) ([]Mensaje, int) {
	working := make([]Mensaje, len(messages))
	copy(working, messages)
	evicted := 0
	for cm.estimate(working) > budget {
		found := false
		for i, m := range working {
			if !m.Pinned {
				working = append(working[:i], working[i+1:]...)
				evicted++
				found = true
				break
			}
		}
		if !found {
			break
		}
	}
	return working, evicted
}

func (cm *ContextManager) llmCompact(messages []Mensaje) []Mensaje {
	if len(messages) <= 8 {
		return messages
	}
	head := messages[:2]
	tail := messages[len(messages)-6:]
	middle := messages[2 : len(messages)-6]
	if len(middle) == 0 {
		return messages
	}

	tokensBefore := cm.estimate(messages)
	fmt.Printf("[compactación LLM] resumiendo %d mensajes intermedios\n", len(middle))

	middleJSON, _ := json.Marshal(middle)
	snippet := string(middleJSON)
	if len(snippet) > 12_000 {
		snippet = snippet[:12_000]
	}

	payload := map[string]interface{}{
		"model":      compactModel,
		"max_tokens": 1_500,
		"messages": []map[string]string{{
			"role": "user",
			"content": "Resume este historial preservando exactamente: " +
				"cada herramienta llamada y su resultado, " +
				"cada decisión tomada y por qué, " +
				"el estado actual de la tarea.\n\nHistorial: " + snippet,
		}},
	}

	data, err := cm.doPost("/messages", payload)
	if err != nil {
		return messages
	}

	var resp struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(data, &resp); err != nil || len(resp.Content) == 0 {
		return messages
	}

	compressed := Mensaje{Role: "user", Content: "[HISTORIAL COMPRIMIDO]\n" + resp.Content[0].Text}
	result := append(append([]Mensaje{}, head...), compressed)
	result = append(result, tail...)

	tokensAfter := cm.estimate(result)
	cm.Metrics.LlmCompactions++
	saved := tokensBefore - tokensAfter
	if saved > 0 {
		cm.Metrics.TokensSaved += saved
	}
	fmt.Printf("[compactación LLM] ~%dt → ~%dt\n", tokensBefore, tokensAfter)
	return result
}

func (cm *ContextManager) Prepare(messages []Mensaje) []Mensaje {
	current := cm.countTokensExact(messages)
	if current <= cm.budget.CompactTrigger() {
		return messages
	}

	fmt.Printf("[contexto] %dt > threshold=%dt\n", current, cm.budget.CompactTrigger())

	reduced, evicted := cm.fifoReduce(messages, cm.budget.History())
	cm.Metrics.FifoEvictions += evicted

	if cm.estimate(reduced) <= cm.budget.History() {
		return reduced
	}
	return cm.llmCompact(reduced)
}

func main() {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	budget := ContextBudget{
		Total: 10_000, System: 500, Retrieved: 300, Tools: 200, Response: 500, Threshold: 0.75,
	}
	mgr := NewContextManager(apiKey, budget, "Eres un asistente de código.")

	history := []Mensaje{{Role: "user", Content: "Analiza este repositorio.", Pinned: true}}
	for i := 1; i < 30; i++ {
		role := "user"
		if i%2 != 0 {
			role = "assistant"
		}
		history = append(history, Mensaje{
			Role:    role,
			Content: fmt.Sprintf("Turno %d: ", i) + strings.Repeat("resultado de análisis. ", 40),
		})
	}

	prepared := mgr.Prepare(history)
	fmt.Printf("Historial final: %d mensajes\n", len(prepared))
	fmt.Printf("Métricas: fifoEvictions=%d llmCompactions=%d tokensSaved=%d\n",
		mgr.Metrics.FifoEvictions, mgr.Metrics.LlmCompactions, mgr.Metrics.TokensSaved)
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
