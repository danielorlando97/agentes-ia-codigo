// Cómo ejecutar: make go FILE=go/07-estado-contexto/truncacion.go
package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

type Message map[string]interface{}

func estimateTokens(messages []Message) int {
	total := 0
	for _, m := range messages {
		b, _ := json.Marshal(m)
		total += len(b)
	}
	return total / 4
}

func validarParidad(messages []Message) []string {
	uses    := map[string]bool{}
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

func hasToolUse(msg Message) bool {
	content, ok := msg["content"].([]interface{})
	if !ok {
		return false
	}
	for _, block := range content {
		b, ok := block.(map[string]interface{})
		if ok && b["type"] == "tool_use" {
			return true
		}
	}
	return false
}

func truncarFifo(messages []Message, maxTokens int) []Message {
	working := make([]Message, len(messages))
	copy(working, messages)

	for estimateTokens(working) > maxTokens && len(working) > 1 {
		removed := working[0]
		working = working[1:]
		if len(working) > 0 &&
			removed["role"] == "assistant" &&
			hasToolUse(removed) &&
			working[0]["role"] == "user" {
			working = working[1:]
		}
	}
	return working
}

func truncarHeadTail(messages []Message, maxTokens, head, tail int) []Message {
	if len(messages) <= head+tail {
		return messages
	}
	result := make([]Message, 0, head+tail)
	result = append(result, messages[:head]...)
	result = append(result, messages[len(messages)-tail:]...)

	if estimateTokens(result) > maxTokens {
		result = truncarFifo(result, maxTokens)
	}
	return result
}

func limpiarToolResults(messages []Message, minAge int) []Message {
	toolResultCount := 0
	resultMsgs := make([]Message, len(messages))
	copy(resultMsgs, messages)

	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		content, ok := msg["content"].([]interface{})
		if !ok {
			resultMsgs[i] = msg
			continue
		}

		newBlocks := make([]interface{}, len(content))
		copy(newBlocks, content)

		for j, block := range content {
			b, ok := block.(map[string]interface{})
			if !ok {
				continue
			}
			if b["type"] == "tool_result" {
				toolResultCount++
				if toolResultCount > minAge {
					newBlock := map[string]interface{}{}
					for k, v := range b {
						newBlock[k] = v
					}
					newBlock["content"] = []interface{}{map[string]interface{}{"type": "text", "text": "[cleared]"}}
					newBlocks[j] = newBlock
				}
			}
		}

		newMsg := Message{}
		for k, v := range msg {
			newMsg[k] = v
		}
		newMsg["content"] = newBlocks
		resultMsgs[i] = newMsg
	}

	return resultMsgs
}

type AlmacenHistorial struct {
	MaxTokens int
	messages  []Message
}

func NewAlmacenHistorial(maxTokens int) *AlmacenHistorial {
	if maxTokens == 0 {
		maxTokens = 110_000
	}
	return &AlmacenHistorial{MaxTokens: maxTokens, messages: []Message{}}
}

func (a *AlmacenHistorial) Add(message Message) {
	a.messages = append(a.messages, message)
}

func (a *AlmacenHistorial) Get() []Message {
	result := make([]Message, len(a.messages))
	copy(result, a.messages)
	return result
}

func (a *AlmacenHistorial) Tokens() int {
	return estimateTokens(a.messages)
}

func (a *AlmacenHistorial) Len() int {
	return len(a.messages)
}

func (a *AlmacenHistorial) ApplyFifo() {
	a.messages = truncarFifo(a.messages, a.MaxTokens)
}

func (a *AlmacenHistorial) ApplyHeadTail(head, tail int) {
	a.messages = truncarHeadTail(a.messages, a.MaxTokens, head, tail)
}

func (a *AlmacenHistorial) ClearToolResults(minAge int) {
	a.messages = limpiarToolResults(a.messages, minAge)
}

func (a *AlmacenHistorial) CheckParity() []string {
	return validarParidad(a.messages)
}

func main() {
	historial := NewAlmacenHistorial(2_000)

	historial.Add(Message{"role": "user", "content": "Analiza el repo."})
	for i := 0; i < 8; i++ {
		id := fmt.Sprintf("tu_%02d", i)
		historial.Add(Message{
			"role": "assistant",
			"content": []interface{}{
				map[string]interface{}{
					"type":  "tool_use",
					"id":    id,
					"name":  "read_file",
					"input": map[string]interface{}{"path": fmt.Sprintf("file_%d.py", i)},
				},
			},
		})
		historial.Add(Message{
			"role": "user",
			"content": []interface{}{
				map[string]interface{}{
					"type":        "tool_result",
					"tool_use_id": id,
					"content":     []interface{}{map[string]interface{}{"type": "text", "text": strings.Repeat("x", 200)}},
				},
			},
		})
	}

	fmt.Printf("Antes: %d msgs, ~%d tokens\n", historial.Len(), historial.Tokens())
	parity := historial.CheckParity()
	if len(parity) == 0 {
		fmt.Println("Paridad: OK")
	} else {
		fmt.Printf("Paridad: %v\n", parity)
	}

	historial.ClearToolResults(4)
	fmt.Printf("\nTras clear_tool_results(min_age=4): ~%d tokens\n", historial.Tokens())
	parity = historial.CheckParity()
	if len(parity) == 0 {
		fmt.Println("Paridad tras limpiar: OK")
	} else {
		fmt.Printf("Paridad tras limpiar: %v\n", parity)
	}

	historial.ApplyHeadTail(1, 4)
	fmt.Printf("\nTras head_tail(1,4): %d msgs, ~%d tokens\n", historial.Len(), historial.Tokens())
	parity = historial.CheckParity()
	if len(parity) == 0 {
		fmt.Println("Paridad final: OK")
	} else {
		fmt.Printf("Paridad final: %v\n", parity)
	}
}
