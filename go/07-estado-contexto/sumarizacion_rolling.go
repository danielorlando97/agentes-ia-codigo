// Cómo ejecutar: make go FILE=go/07-estado-contexto/sumarizacion_rolling.go
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
)

func estimateTokens(messages []map[string]interface{}) int {
	total := 0
	for _, m := range messages {
		b, _ := json.Marshal(m)
		total += len(b)
	}
	return total / 4
}

func validarParidad(messages []map[string]interface{}) []string {
	uses := map[string]bool{}
	results := map[string]bool{}

	for _, msg := range messages {
		content, ok := msg["content"].([]interface{})
		if !ok {
			continue
		}
		for _, block := range content {
			b, ok := block.(map[string]interface{})
			if !ok {
				continue
			}
			if b["type"] == "tool_use" {
				if id, ok := b["id"].(string); ok {
					uses[id] = true
				}
			} else if b["type"] == "tool_result" {
				if id, ok := b["tool_use_id"].(string); ok {
					results[id] = true
				}
			}
		}
	}

	var orphans []string
	for id := range uses {
		if !results[id] {
			orphans = append(orphans, id)
		}
	}
	for id := range results {
		if !uses[id] {
			orphans = append(orphans, id)
		}
	}
	return orphans
}

type SummarizationConfig struct {
	Head             int
	Tail             int
	MaxTokens        int
	Threshold        float64
	Model            string
	SummaryMaxTokens int
}

func defaultSummarizationConfig() SummarizationConfig {
	return SummarizationConfig{
		Head:             2,
		Tail:             6,
		MaxTokens:        110_000,
		Threshold:        0.75,
		Model:            envOr("SMALL_MODEL", "claude-haiku-4-5-20251001"),
		SummaryMaxTokens: 1_500,
	}
}

type SummarizationBuffer struct {
	apiKey          string
	cfg             SummarizationConfig
	messages        []map[string]interface{}
	CompactionCount int
}

func NewSummarizationBuffer(apiKey string, cfg SummarizationConfig) *SummarizationBuffer {
	return &SummarizationBuffer{
		apiKey:   apiKey,
		cfg:      cfg,
		messages: []map[string]interface{}{},
	}
}

func (b *SummarizationBuffer) Add(message map[string]interface{}) {
	b.messages = append(b.messages, message)
}

func (b *SummarizationBuffer) Get() []map[string]interface{} {
	result := make([]map[string]interface{}, len(b.messages))
	copy(result, b.messages)
	return result
}

func (b *SummarizationBuffer) Tokens() int {
	return estimateTokens(b.messages)
}

func (b *SummarizationBuffer) Len() int {
	return len(b.messages)
}

func (b *SummarizationBuffer) shouldSummarize() bool {
	trigger := int(float64(b.cfg.MaxTokens) * b.cfg.Threshold)
	return b.Tokens() > trigger
}

func (b *SummarizationBuffer) buildCompactionPrompt(messages []map[string]interface{}) string {
	messagesJSON, _ := json.Marshal(messages)
	truncated := string(messagesJSON)
	if len(truncated) > 14_000 {
		truncated = truncated[:14_000]
	}
	return "Resume este historial de un agente. Preserva exactamente:\n" +
		"- Cada herramienta llamada, sus parámetros y su resultado (números, IDs, rutas)\n" +
		"- Cada decisión tomada y su justificación\n" +
		"- Restricciones y constraints del usuario\n" +
		"- El estado actual de la tarea y el progreso\n" +
		"No parafrasees valores numéricos ni identificadores — cópialos literalmente.\n\n" +
		"Historial: " + truncated
}

func (b *SummarizationBuffer) callLLM(prompt string) (string, error) {
	payload := map[string]interface{}{
		"model":      b.cfg.Model,
		"max_tokens": b.cfg.SummaryMaxTokens,
		"messages":   []map[string]interface{}{{"role": "user", "content": prompt}},
	}
	body, _ := json.Marshal(payload)

	req, _ := http.NewRequest("POST", envBaseURL(), bytes.NewReader(body))
	req.Header.Set("x-api-key", b.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("content-type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)
	var ar struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(data, &ar); err != nil || len(ar.Content) == 0 {
		return "", fmt.Errorf("respuesta inesperada: %s", data)
	}
	return ar.Content[0].Text, nil
}

func (b *SummarizationBuffer) Compact() (bool, error) {
	if !b.shouldSummarize() {
		return false, nil
	}
	msgs := b.messages
	if len(msgs) <= b.cfg.Head+b.cfg.Tail {
		return false, nil
	}

	head   := msgs[:b.cfg.Head]
	tail   := msgs[len(msgs)-b.cfg.Tail:]
	middle := msgs[b.cfg.Head : len(msgs)-b.cfg.Tail]

	if len(middle) == 0 {
		return false, nil
	}

	summary, err := b.callLLM(b.buildCompactionPrompt(middle))
	if err != nil {
		return false, err
	}

	compressed := map[string]interface{}{
		"role":    "user",
		"content": "[HISTORIAL COMPRIMIDO]\n" + summary,
	}

	newMessages := make([]map[string]interface{}, 0, len(head)+1+len(tail))
	newMessages = append(newMessages, head...)
	newMessages = append(newMessages, compressed)
	newMessages = append(newMessages, tail...)
	b.messages = newMessages
	b.CompactionCount++

	boundary := append(head, tail...)
	orphans := validarParidad(boundary)
	if len(orphans) > 0 {
		fmt.Printf("  [aviso] paridad en boundary: %v\n", orphans)
	}

	return true, nil
}

func main() {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")

	cfg := defaultSummarizationConfig()
	cfg.Head             = 1
	cfg.Tail             = 2
	cfg.MaxTokens        = 3_000
	cfg.Threshold        = 0.6
	cfg.SummaryMaxTokens = 300

	buf := NewSummarizationBuffer(apiKey, cfg)

	buf.Add(map[string]interface{}{"role": "user", "content": "Analiza el repo y encuentra bugs de seguridad."})
	for i := 0; i < 10; i++ {
		buf.Add(map[string]interface{}{"role": "assistant", "content": fmt.Sprintf("Analicé auth_%d.py: sin vulnerabilidades evidentes.", i)})
		buf.Add(map[string]interface{}{"role": "user", "content": fmt.Sprintf("Continúa con el módulo %d.", i+1)})
	}

	fmt.Printf("Antes de compact: %d msgs, ~%d tokens\n", buf.Len(), buf.Tokens())
	compactado, err := buf.Compact()
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}
	fmt.Printf("Compactó: %v | Tras compact: %d msgs, ~%d tokens\n", compactado, buf.Len(), buf.Tokens())
	fmt.Printf("Compacciones totales: %d\n", buf.CompactionCount)

	if compactado {
		msgs := buf.Get()
		if len(msgs) > 1 {
			content, _ := msgs[1]["content"].(string)
			if len(content) > 200 {
				content = content[:200]
			}
			fmt.Printf("\nMensaje comprimido (primeros 200 chars):\n  %s\n", content)
		}
	}
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
