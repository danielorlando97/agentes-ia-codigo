// Medir métricas clave de una sesión multi-turn con tool calls.
//
// Muestra:
//  1. TTFT (time to first token) por turno
//  2. TPOT (time per output token) en ms por turno
//  3. Tokens de input/output acumulados por turno
//  4. Costo total de la sesión y costo por tarea vs costo por token
//  5. Tabla resumen de la sesión completa
//
// Sin SDK: HTTP directo contra la API de Anthropic.

// Cómo ejecutar: make go FILE=go/03-motor-llm/costo-latencia.go

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	// Precios Haiku 4.5 (USD por millón de tokens, Mayo 2025)
	costoPrecioInput  = 0.80
	costoPrecioOutput = 4.00
	tarea = "Necesito resolver un problema en tres pasos:\n" +
	"1. Calcula 347 × 89\n" +
	"2. Al resultado anterior, réstale 5000\n" +
	"3. Eleva el resultado al cuadrado\n" +
	"Muéstrame los tres resultados."
)

var (
	costoMainModel = envOr("MODEL", "claude-sonnet-4-6")
	costoSmallModel = envOr("SMALL_MODEL", "claude-haiku-4-5-20251001")
	costoAPIURL = envBaseURL()
)

var costoHerramientas = []map[string]any{
	{
		"name":        "calcular",
		"description": "Realiza operaciones matemáticas. Operaciones: suma, resta, multiplicacion, division, potencia.",
		"input_schema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"operacion": map[string]any{
					"type": "string",
					"enum": []string{"suma", "resta", "multiplicacion", "division", "potencia"},
				},
				"a": map[string]string{"type": "number", "description": "Primer operando"},
				"b": map[string]string{"type": "number", "description": "Segundo operando"},
			},
			"required": []string{"operacion", "a", "b"},
		},
	},
}

// ─── Estructuras de mensajes ─────────────────────────────────────────────

type costoBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   string          `json:"content,omitempty"`
}

type costoMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type costoAPIResp struct {
	Content    []costoBlock `json:"content"`
	StopReason string       `json:"stop_reason"`
	Usage      struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// ─── Métricas por turno ──────────────────────────────────────────────────

type metricasTurno struct {
	turno         int
	ttftS         float64
	latenciaS     float64
	tokensInput   int
	tokensOutput  int
	tpotMs        float64
	toolCalls     int
	costoUSD      float64
}

// ─── Cliente HTTP con streaming SSE ──────────────────────────────────────

type sseMessage struct {
	Type    string `json:"type"`
	Delta   *struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"delta,omitempty"`
	Usage   *struct {
		OutputTokens int `json:"output_tokens"`
	} `json:"usage,omitempty"`
	Message *struct {
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	} `json:"message,omitempty"`
	ContentBlock *costoBlock `json:"content_block,omitempty"`
	Index        int         `json:"index,omitempty"`
}

type fullResponse struct {
	Content    []costoBlock
	StopReason string
	InTokens   int
	OutTokens  int
}

func llamarConMetricasGo(mensajes []costoMessage, turno int) (*costoAPIResp, metricasTurno, error) {
	payload := map[string]any{
		"model":      costoSmallModel,
		"max_tokens": 512,
		"tools":      costoHerramientas,
		"stream":     true,
		"messages":   mensajes,
	}
	body, _ := json.Marshal(payload)
	req, _ := http.NewRequestWithContext(context.Background(), "POST", costoAPIURL, bytes.NewReader(body))
	req.Header.Set("x-api-key", os.Getenv("ANTHROPIC_API_KEY"))
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("content-type", "application/json")
	req.Header.Set("accept", "text/event-stream")

	tInicio := time.Now()
	var ttft float64
	ttftCap := false

	httpResp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, metricasTurno{}, err
	}
	defer httpResp.Body.Close()

	// Acumular bloques por índice
	blocksByIndex := make(map[int]*costoBlock)
	var stopReason string
	var inTokens, outTokens int

	scanner := bufio.NewScanner(httpResp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}
		var ev sseMessage
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			continue
		}

		switch ev.Type {
		case "message_start":
			if ev.Message != nil {
				inTokens = ev.Message.Usage.InputTokens
			}
		case "content_block_start":
			if ev.ContentBlock != nil {
				b := *ev.ContentBlock
				blocksByIndex[ev.Index] = &b
			}
		case "content_block_delta":
			if !ttftCap && ev.Delta != nil && ev.Delta.Text != "" {
				ttft = time.Since(tInicio).Seconds()
				ttftCap = true
			}
			if ev.Delta != nil && ev.Delta.Type == "text_delta" {
				if b, ok := blocksByIndex[ev.Index]; ok {
					b.Text += ev.Delta.Text
				}
			}
		case "message_delta":
			if ev.Usage != nil {
				outTokens = ev.Usage.OutputTokens
			}
		case "message_stop":
			// nada
		}

		// stop_reason puede estar en message_delta; parseamos de otra forma
		var raw map[string]any
		if err := json.Unmarshal([]byte(data), &raw); err == nil {
			if sr, ok := raw["delta"].(map[string]any); ok {
				if s, ok := sr["stop_reason"].(string); ok {
					stopReason = s
				}
			}
		}
	}

	latencia := time.Since(tInicio).Seconds()
	if !ttftCap {
		ttft = latencia
	}

	// Reconstruir content
	var content []costoBlock
	for i := 0; ; i++ {
		b, ok := blocksByIndex[i]
		if !ok {
			break
		}
		content = append(content, *b)
	}

	tpotMs := 0.0
	if outTokens > 0 && latencia > 0 {
		tpotMs = latencia * 1000 / float64(outTokens)
	}
	toolCalls := 0
	for _, b := range content {
		if b.Type == "tool_use" {
			toolCalls++
		}
	}
	costoUSD := float64(inTokens)/1_000_000*costoPrecioInput +
		float64(outTokens)/1_000_000*costoPrecioOutput

	m := metricasTurno{
		turno: turno, ttftS: ttft, latenciaS: latencia,
		tokensInput: inTokens, tokensOutput: outTokens,
		tpotMs: tpotMs, toolCalls: toolCalls, costoUSD: costoUSD,
	}

	// Construir respuesta sintética para el llamador
	synth := &costoAPIResp{
		Content:    content,
		StopReason: stopReason,
		Usage: struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		}{InputTokens: inTokens, OutputTokens: outTokens},
	}
	return synth, m, nil
}

// ─── Herramienta calculadora mock ────────────────────────────────────────

func ejecutarCalculadoraGo(operacion string, a, b float64) string {
	var resultado float64
	switch operacion {
	case "suma":            resultado = a + b
	case "resta":           resultado = a - b
	case "multiplicacion":  resultado = a * b
	case "division":
		if b != 0 {
			resultado = a / b
		}
	case "potencia":
		resultado = 1
		for i := 0; i < int(b); i++ {
			resultado *= a
		}
	}
	out, _ := json.Marshal(map[string]any{"resultado": resultado, "operacion": operacion, "a": a, "b": b})
	return string(out)
}

// ─── Sesión multi-turn ────────────────────────────────────────────────────

func ejecutarSesionMultiturn() []metricasTurno {
	tareaRaw, _ := json.Marshal(tarea)
	mensajes := []costoMessage{{Role: "user", Content: tareaRaw}}
	var metricasSesion []metricasTurno
	turno := 1

	fmt.Println("\n[sesión multi-turn con tool calls]")
	fmt.Printf("  Tarea: %q...\n\n", tarea[:80])

	for turno <= 10 {
		fmt.Printf("  --- Turno %d ---\n", turno)
		resp, m, err := llamarConMetricasGo(mensajes, turno)
		if err != nil {
			fmt.Printf("  error: %v\n", err)
			break
		}
		metricasSesion = append(metricasSesion, m)

		fmt.Printf("  TTFT=%.3fs  total=%.3fs  TPOT=%.1fms/tok  in=%d  out=%d  tool_calls=%d  costo=$%.6f\n",
			m.ttftS, m.latenciaS, m.tpotMs, m.tokensInput, m.tokensOutput, m.toolCalls, m.costoUSD)

		// Añadir respuesta del asistente
		asstContent, _ := json.Marshal(resp.Content)
		mensajes = append(mensajes, costoMessage{Role: "assistant", Content: asstContent})

		// Buscar tool_uses
		var toolUses []costoBlock
		for _, b := range resp.Content {
			if b.Type == "tool_use" {
				toolUses = append(toolUses, b)
			}
		}

		if len(toolUses) == 0 {
			// Fin de la sesión
			var sb strings.Builder
			for _, b := range resp.Content {
				if b.Type == "text" {
					sb.WriteString(b.Text)
				}
			}
			textoFinal := sb.String()
			if len(textoFinal) > 200 {
				textoFinal = textoFinal[:200]
			}
			fmt.Printf("\n  Respuesta final: %q\n", textoFinal)
			break
		}

		// Ejecutar herramientas
		var toolResults []costoBlock
		for _, tu := range toolUses {
			var args map[string]float64
			_ = json.Unmarshal(tu.Input, &args)
			resultado := ejecutarCalculadoraGo(
				func() string {
					var m map[string]any
					_ = json.Unmarshal(tu.Input, &m)
					if op, ok := m["operacion"].(string); ok {
						return op
					}
					return ""
				}(),
				args["a"], args["b"],
			)
			fmt.Printf("  Tool: calcular(a=%.0f, b=%.0f) → %s\n", args["a"], args["b"], resultado)
			toolResults = append(toolResults, costoBlock{
				Type:      "tool_result",
				ToolUseID: tu.ID,
				Content:   resultado,
			})
		}

		userContent, _ := json.Marshal(toolResults)
		mensajes = append(mensajes, costoMessage{Role: "user", Content: userContent})
		turno++
	}

	return metricasSesion
}

// ─── Tabla resumen ────────────────────────────────────────────────────────

func tablaResumenSesionGo(sesion []metricasTurno) {
	fmt.Println("\n[tabla resumen de la sesión]")

	header := fmt.Sprintf("  %6s  %8s  %9s  %9s  %7s  %8s  %11s  %10s",
		"Turno", "TTFT(s)", "Total(s)", "TPOT(ms)", "In tok", "Out tok", "Tool calls", "Costo ($)")
	sep := "  " + strings.Repeat("-", len([]rune(header))-2)
	fmt.Println(header)
	fmt.Println(sep)

	var sumIn, sumOut int
	var sumCosto, sumLat float64

	for _, m := range sesion {
		fmt.Printf("  %6d  %8.3f  %9.3f  %9.1f  %7d  %8d  %11d  %10.6f\n",
			m.turno, m.ttftS, m.latenciaS, m.tpotMs,
			m.tokensInput, m.tokensOutput, m.toolCalls, m.costoUSD)
		sumIn  += m.tokensInput
		sumOut += m.tokensOutput
		sumCosto += m.costoUSD
		sumLat   += m.latenciaS
	}

	fmt.Println(sep)
	fmt.Printf("  %6s  %8s  %9.3f  %9s  %7d  %8d  %11s  %10.6f\n",
		"TOTAL", "", sumLat, "", sumIn, sumOut, "", sumCosto)

	fmt.Printf("\n  Costo por tarea completa:   $%.6f\n", sumCosto)
	if sumOut > 0 {
		fmt.Printf("  Costo por token de output:  $%.4f/millón\n", sumCosto/float64(sumOut)*1_000_000)
	}
	if sumIn > 0 {
		fmt.Printf("  Costo por token de input:   $%.4f/millón\n", sumCosto/float64(sumIn)*1_000_000)
	}

	overhead := 0
	if len(sesion) > 0 {
		overhead = sumIn - sesion[0].tokensInput
	}
	fmt.Printf("\n  Overhead de historial acumulado: %d tokens extra de input\n", overhead)
	fmt.Println("  (Los tool schemas se cuentan en cada turno)")
}

// ─── Suprimir warning de io import ───────────────────────────────────────

var _ = io.Discard

func main() {
	fmt.Println("=== Costo y latencia: sesión multi-turn con tool calls ===")
	sesion := ejecutarSesionMultiturn()
	tablaResumenSesionGo(sesion)
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
