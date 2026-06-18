// Devolver el resultado al modelo — formatos de tool_result.
//
// Muestra el formato correcto de tool_result en cinco escenarios:
//   1. Texto simple
//   2. JSON estructurado
//   3. Imagen (content array con type: "image")
//   4. Error formativo (is_error: true)
//   5. Loop completo: request → tool_use → execute → tool_result → segunda response
//
// El contenido del campo 'content' cuando is_error=true determina
// si el modelo puede autocorregir — un error genérico produce retry
// idéntico; un error formativo produce recovery inteligente.

// Cómo ejecutar: make go FILE=go/05-herramientas/21-feedback-modelo.go

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
)

var (
	modelFeedback = envOr("MODEL", "claude-sonnet-4-6")
	feedbackAPIURL = envBaseURL()
)

// --- Tipos de tool_result ---

// toolResultSimple es el formato básico con texto plano.
type toolResultSimple struct {
	Type      string `json:"type"`
	ToolUseID string `json:"tool_use_id"`
	Content   string `json:"content"`
	IsError   bool   `json:"is_error,omitempty"`
}

// toolResultRich es el formato con content array (para imágenes o contenido mixto).
type toolResultRich struct {
	Type      string        `json:"type"`
	ToolUseID string        `json:"tool_use_id"`
	Content   []interface{} `json:"content"`
	IsError   bool          `json:"is_error,omitempty"`
}

// --- 1. Tool result con texto simple ---

func toolResultTextoFeedback(toolUseID string) toolResultSimple {
	return toolResultSimple{
		Type:      "tool_result",
		ToolUseID: toolUseID,
		Content:   "La temperatura en Madrid es 24°C, condición: soleado.",
	}
}

// --- 2. Tool result con JSON estructurado ---

func toolResultJSONFeedback(toolUseID string) toolResultSimple {
	datos := map[string]interface{}{
		"city":      "Madrid",
		"temperature": map[string]interface{}{
			"value": 24,
			"unit":  "celsius",
		},
		"condition": "sunny",
		"humidity":  45,
		"wind": map[string]interface{}{
			"speed":     12,
			"direction": "NW",
		},
		"forecast": []map[string]interface{}{
			{"day": "mañana", "high": 26, "low": 18},
			{"day": "pasado", "high": 23, "low": 16},
		},
	}
	jsonStr, _ := json.Marshal(datos)
	return toolResultSimple{
		Type:      "tool_result",
		ToolUseID: toolUseID,
		Content:   string(jsonStr),
	}
}

// --- 3. Tool result con imagen (content array) ---

func toolResultImagenFeedback(toolUseID string) toolResultRich {
	// PNG 1x1 rojo (base64) — en producción sería el gráfico real
	pngBase64 := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwADhQGAWjR9awAAAABJRU5ErkJggg=="

	return toolResultRich{
		Type:      "tool_result",
		ToolUseID: toolUseID,
		Content: []interface{}{
			map[string]interface{}{
				"type": "text",
				"text": "Gráfico de temperaturas de Madrid — últimas 24 horas:",
			},
			map[string]interface{}{
				"type": "image",
				"source": map[string]interface{}{
					"type":       "base64",
					"media_type": "image/png",
					"data":       pngBase64,
				},
			},
		},
	}
}

// --- 4. Tool result con error formativo ---

type errorTipo string

const (
	errorNotFound  errorTipo = "not_found"
	errorTimeout   errorTipo = "timeout"
	errorPermission errorTipo = "permission"
)

func toolResultErrorFormativoFeedback(toolUseID string, tipo errorTipo) toolResultSimple {
	mensajes := map[errorTipo]string{
		errorNotFound: "Archivo no encontrado: /tmp/report.md\n" +
			"Archivos disponibles en /tmp/: budget.md, analysis.md, notes.txt\n" +
			"Sugerencia: usa read_file con el path de uno de los archivos disponibles.",

		errorTimeout: "Timeout tras 10s buscando 'todos los documentos de 2024'.\n" +
			"Intenta filtrar por rango de fecha mas pequeno, e.g. '2024-Q1' o 'enero 2024'.",

		errorPermission: "Sin permisos para acceder a /etc/passwords.\n" +
			"No reintentes — usa un directorio dentro de /home/usuario/.",
	}

	return toolResultSimple{
		Type:      "tool_result",
		ToolUseID: toolUseID,
		Content:   mensajes[tipo],
		IsError:   true,
	}
}

// --- Mostrar los formatos ---

func mostrarFormatosFeedback() {
	fmt.Println("=== Formatos de tool_result ===\n")
	fakeID := "toolu_fake_id_001"

	fmt.Println("1. Texto simple:")
	data, _ := json.MarshalIndent(toolResultTextoFeedback(fakeID), "", "  ")
	fmt.Println(string(data))

	fmt.Println("\n2. JSON estructurado:")
	data, _ = json.MarshalIndent(toolResultJSONFeedback(fakeID), "", "  ")
	fmt.Println(string(data))

	fmt.Println("\n3. Imagen (content array):")
	img := toolResultImagenFeedback(fakeID)
	// Truncar el base64 para la demo
	imgDisplay := toolResultRich{
		Type:      img.Type,
		ToolUseID: img.ToolUseID,
		Content: []interface{}{
			img.Content[0],
			map[string]interface{}{
				"type": "image",
				"source": map[string]interface{}{
					"type":       "base64",
					"media_type": "image/png",
					"data":       "[base64 truncado para demo]",
				},
			},
		},
	}
	data, _ = json.MarshalIndent(imgDisplay, "", "  ")
	fmt.Println(string(data))

	fmt.Println("\n4. Errores formativos:")
	for _, tipo := range []errorTipo{errorNotFound, errorTimeout, errorPermission} {
		r := toolResultErrorFormativoFeedback(fakeID, tipo)
		fmt.Printf("\n  [%s]\n  is_error: %v\n  content: %s\n", tipo, r.IsError, r.Content)
	}
}

// --- 5. Loop completo con autocorrección por error formativo ---

type feedbackBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   interface{}     `json:"content,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
}

type feedbackMsg struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type feedbackAPIResp struct {
	Content    []feedbackBlock `json:"content"`
	StopReason string          `json:"stop_reason"`
}

func callAnthropicFeedback(payload map[string]interface{}) (*feedbackAPIResp, error) {
	body, _ := json.Marshal(payload)
	req, _ := http.NewRequestWithContext(context.Background(), "POST", feedbackAPIURL, bytes.NewReader(body))
	req.Header.Set("x-api-key", os.Getenv("ANTHROPIC_API_KEY"))
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("content-type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	var r feedbackAPIResp
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("parse: %s — %w", string(data), err)
	}
	return &r, nil
}

var toolsFeedback = []map[string]interface{}{
	{
		"name":        "read_file",
		"description": "Lee el contenido de un archivo.",
		"input_schema": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"path": map[string]string{"type": "string", "description": "Path absoluto del archivo"},
			},
			"required": []string{"path"},
		},
	},
	{
		"name":        "list_files",
		"description": "Lista los archivos en un directorio.",
		"input_schema": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"directory": map[string]string{"type": "string"},
			},
			"required": []string{"directory"},
		},
	},
}

func loopCompletoFeedback() {
	fmt.Println("\n\n=== Loop completo con autocorrección por error formativo ===\n")

	readFileIntento := 0

	ejecutarToolFeedback := func(toolUseID, name string, input json.RawMessage) feedbackBlock {
		var args map[string]string
		_ = json.Unmarshal(input, &args)

		if name == "read_file" {
			readFileIntento++
			if readFileIntento == 1 && args["path"] == "/tmp/report.md" {
				// Primera llamada falla con error formativo
				r := toolResultErrorFormativoFeedback(toolUseID, errorNotFound)
				return feedbackBlock{
					Type:      "tool_result",
					ToolUseID: toolUseID,
					Content:   r.Content,
					IsError:   r.IsError,
				}
			}
			// Llamadas posteriores (path correcto) tienen éxito
			return feedbackBlock{
				Type:      "tool_result",
				ToolUseID: toolUseID,
				Content:   fmt.Sprintf("Contenido de %s:\n# Presupuesto 2024\nTotal: $1,234,567", args["path"]),
			}
		}

		if name == "list_files" {
			data, _ := json.Marshal(map[string]interface{}{
				"directory": args["directory"],
				"files":     []string{"budget.md", "analysis.md", "notes.txt"},
			})
			return feedbackBlock{
				Type:      "tool_result",
				ToolUseID: toolUseID,
				Content:   string(data),
			}
		}

		return feedbackBlock{
			Type:      "tool_result",
			ToolUseID: toolUseID,
			Content:   fmt.Sprintf("Herramienta '%s' no existe", name),
			IsError:   true,
		}
	}

	taskJSON, _ := json.Marshal("Lee el archivo /tmp/report.md y dime el presupuesto total.")
	messages := []feedbackMsg{{Role: "user", Content: taskJSON}}

	for iter := 0; iter < 5; iter++ {
		resp, err := callAnthropicFeedback(map[string]interface{}{
			"model":      modelFeedback,
			"max_tokens": 1024,
			"tools":      toolsFeedback,
			"messages":   messages,
		})
		if err != nil {
			panic(err)
		}

		fmt.Printf("[iter=%d] stop_reason=%s\n", iter+1, resp.StopReason)

		if resp.StopReason == "end_turn" {
			for _, b := range resp.Content {
				if b.Type == "text" {
					fmt.Printf("\nRespuesta final:\n%s\n", b.Text)
				}
			}
			break
		}

		if resp.StopReason == "tool_use" {
			var toolResults []feedbackBlock
			for _, b := range resp.Content {
				if b.Type != "tool_use" {
					continue
				}
				var args map[string]string
				_ = json.Unmarshal(b.Input, &args)
				fmt.Printf("  → %s(%v)\n", b.Name, args)

				res := ejecutarToolFeedback(b.ID, b.Name, b.Input)
				status := "OK"
				if res.IsError {
					status = "ERROR"
				}
				content := fmt.Sprint(res.Content)
				if len(content) > 100 {
					content = content[:100]
				}
				fmt.Printf("  ← [%s] %s\n", status, content)
				toolResults = append(toolResults, res)
			}

			asstJSON, _ := json.Marshal(resp.Content)
			userJSON, _ := json.Marshal(toolResults)
			messages = append(messages,
				feedbackMsg{Role: "assistant", Content: asstJSON},
				feedbackMsg{Role: "user", Content: userJSON},
			)
		}
	}
}

func main() {
	mostrarFormatosFeedback()
	loopCompletoFeedback()
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
