// Benchmark de latencia y costo: API cloud vs modelo local (Ollama).
//
// Muestra:
//  1. TTFT y latencia total via streaming de Anthropic
//  2. El mismo benchmark con Ollama local (graceful si no está disponible)
//  3. Break-even: cuántos requests/mes para que el local sea más barato
//  4. Tabla de costos mensuales parametrizable
//
// Sin SDK: HTTP directo contra la API de Anthropic.

// Cómo ejecutar: make go FILE=go/03-motor-llm/local-vs-api.go

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	// Precios Haiku 4.5 (USD por millón de tokens, Mayo 2025)
	localPrecioInput  = 0.80
	localPrecioOutput = 4.00
	costoGPUHoraUSD    = 0.50
	horasDiaActivas    = 24.0
	requestsHoraLocal  = 120
	ollamaURL   = "http://localhost:11434"
	ollamaModel = "llama3.2:1b"
	promptBenchmark = "Explica en exactamente dos oraciones qué es el attention mechanism en transformers."
)

var (
	localMainModel = envOr("MODEL", "claude-sonnet-4-6")
	localSmallModel = envOr("SMALL_MODEL", "claude-haiku-4-5-20251001")
	localAPIURL = envBaseURL()
)

// ─── Resultado de benchmark ───────────────────────────────────────────────

type resultadoBenchmark struct {
	tipo             string
	modelo           string
	avgTTFT          float64
	avgLatencia      float64
	avgTokensInput   float64
	avgTokensOutput  float64
	costoPerCallUSD  float64
}

// ─── 1. Benchmark API Cloud con streaming ─────────────────────────────────

type sseEvent struct {
	Type  string          `json:"type"`
	Delta *struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"delta,omitempty"`
	Usage *struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage,omitempty"`
	Message *struct {
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	} `json:"message,omitempty"`
}

func benchmarkAPICloud(repeticiones int) resultadoBenchmark {
	fmt.Printf("\n[benchmark API cloud — %s]\n", localSmallModel)
	fmt.Printf("  Prompt: %q...\n\n", promptBenchmark[:60])

	var sumTTFT, sumLatencia float64
	var sumIn, sumOut int

	for i := 0; i < repeticiones; i++ {
		payload := map[string]any{
			"model":      localSmallModel,
			"max_tokens": 128,
			"stream":     true,
			"messages": []map[string]string{
				{"role": "user", "content": promptBenchmark},
			},
		}
		body, _ := json.Marshal(payload)
		req, _ := http.NewRequestWithContext(context.Background(), "POST", localAPIURL, bytes.NewReader(body))
		req.Header.Set("x-api-key", os.Getenv("ANTHROPIC_API_KEY"))
		req.Header.Set("anthropic-version", "2023-06-01")
		req.Header.Set("content-type", "application/json")
		req.Header.Set("accept", "text/event-stream")

		tInicio := time.Now()
		var ttft float64
		ttftCapturado := false
		var tokensIn, tokensOut int

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			fmt.Printf("  rep%d: error — %v\n", i+1, err)
			continue
		}

		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := strings.TrimPrefix(line, "data: ")
			if data == "[DONE]" {
				break
			}
			var event sseEvent
			if err := json.Unmarshal([]byte(data), &event); err != nil {
				continue
			}
			if !ttftCapturado && event.Type == "content_block_delta" &&
				event.Delta != nil && event.Delta.Text != "" {
				ttft = time.Since(tInicio).Seconds()
				ttftCapturado = true
			}
			if event.Type == "message_delta" && event.Usage != nil {
				tokensOut = event.Usage.OutputTokens
			}
			if event.Type == "message_start" && event.Message != nil {
				tokensIn = event.Message.Usage.InputTokens
			}
		}
		resp.Body.Close()

		latencia := time.Since(tInicio).Seconds()
		if !ttftCapturado {
			ttft = latencia
		}

		sumTTFT += ttft
		sumLatencia += latencia
		sumIn += tokensIn
		sumOut += tokensOut

		fmt.Printf("  rep%d: TTFT=%.3fs  total=%.3fs  in=%dtok  out=%dtok\n",
			i+1, ttft, latencia, tokensIn, tokensOut)
	}

	avgIn  := float64(sumIn) / float64(repeticiones)
	avgOut := float64(sumOut) / float64(repeticiones)
	avgTTFT := sumTTFT / float64(repeticiones)
	avgLat  := sumLatencia / float64(repeticiones)
	costo := avgIn/1_000_000*localPrecioInput + avgOut/1_000_000*localPrecioOutput

	fmt.Printf("\n  Promedio: TTFT=%.3fs  total=%.3fs\n", avgTTFT, avgLat)
	fmt.Printf("  Costo por call: $%.6f\n", costo)

	return resultadoBenchmark{
		tipo: "api_cloud", modelo: localSmallModel,
		avgTTFT: avgTTFT, avgLatencia: avgLat,
		avgTokensInput: avgIn, avgTokensOutput: avgOut,
		costoPerCallUSD: costo,
	}
}

// ─── 2. Benchmark Ollama local ────────────────────────────────────────────

func verificarOllama() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", ollamaURL+"/api/tags", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == 200
}

func benchmarkOllamaLocal(repeticiones int) *resultadoBenchmark {
	if !verificarOllama() {
		fmt.Printf("\n[benchmark local — Ollama no disponible en %s]\n", ollamaURL)
		fmt.Println("  Para ejecutar el benchmark local:")
		fmt.Println("    1. Instala Ollama: https://ollama.com")
		fmt.Printf("    2. Descarga el modelo: ollama pull %s\n", ollamaModel)
		fmt.Println("    3. Vuelve a ejecutar este script")
		return nil
	}

	fmt.Printf("\n[benchmark local — Ollama %s]\n", ollamaModel)
	fmt.Printf("  Prompt: %q...\n\n", promptBenchmark[:60])

	var sumTTFT, sumLatencia float64
	var count int

	for i := 0; i < repeticiones; i++ {
		payload := map[string]any{
			"model":  ollamaModel,
			"prompt": promptBenchmark,
			"stream": true,
		}
		body, _ := json.Marshal(payload)
		req, _ := http.NewRequestWithContext(context.Background(), "POST",
			ollamaURL+"/api/generate", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")

		tInicio := time.Now()
		var ttft float64
		ttftCapturado := false
		var tokensGen int

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			fmt.Printf("  rep%d: error — %v\n", i+1, err)
			continue
		}

		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			var chunk struct {
				Response  string `json:"response"`
				EvalCount int    `json:"eval_count"`
			}
			if err := json.Unmarshal(scanner.Bytes(), &chunk); err != nil {
				continue
			}
			if !ttftCapturado && chunk.Response != "" {
				ttft = time.Since(tInicio).Seconds()
				ttftCapturado = true
			}
			if chunk.EvalCount > 0 {
				tokensGen = chunk.EvalCount
			}
		}
		resp.Body.Close()

		latencia := time.Since(tInicio).Seconds()
		if !ttftCapturado {
			ttft = latencia
		}
		tps := 0.0
		if latencia > 0 {
			tps = float64(tokensGen) / latencia
		}

		sumTTFT += ttft
		sumLatencia += latencia
		count++

		fmt.Printf("  rep%d: TTFT=%.3fs  total=%.3fs  tokens_gen=%d  TPS=%.1f\n",
			i+1, ttft, latencia, tokensGen, tps)
	}

	if count == 0 {
		return nil
	}

	avgTTFT := sumTTFT / float64(count)
	avgLat  := sumLatencia / float64(count)
	fmt.Printf("\n  Promedio: TTFT=%.3fs  total=%.3fs\n", avgTTFT, avgLat)

	r := &resultadoBenchmark{
		tipo: "local_ollama", modelo: ollamaModel,
		avgTTFT: avgTTFT, avgLatencia: avgLat,
	}
	return r
}

// ─── 3. Break-even ────────────────────────────────────────────────────────

func calcularBreakeven(costoPerCallAPI float64) {
	fmt.Println("\n[break-even: ¿cuándo conviene el modelo local?]")

	costoInfraMes    := costoGPUHoraUSD * horasDiaActivas * 30
	requestsMesMax   := requestsHoraLocal * int(horasDiaActivas) * 30
	var breakevenReqs float64
	if costoPerCallAPI > 0 {
		breakevenReqs = costoInfraMes / costoPerCallAPI
	}

	fmt.Printf("\n  Supuestos infraestructura local:\n")
	fmt.Printf("    GPU alquilada:        $%.2f/hora\n", costoGPUHoraUSD)
	fmt.Printf("    Horas activas/día:    %.0fh\n", horasDiaActivas)
	fmt.Printf("    Costo infra/mes:      $%.2f\n", costoInfraMes)
	fmt.Printf("    Capacidad máx/mes:    %d requests\n", requestsMesMax)
	fmt.Printf("\n  API cloud (%s):\n", localSmallModel)
	fmt.Printf("    Costo por call:       $%.6f\n", costoPerCallAPI)
	fmt.Printf("    Break-even:           %.0f requests/mes\n", breakevenReqs)
	if breakevenReqs < float64(requestsMesMax) {
		pct := breakevenReqs / float64(requestsMesMax) * 100
		fmt.Printf("    ↳ Equivale al %.1f%% de la capacidad del hardware\n", pct)
		fmt.Printf("    ↳ Si superas %.0f req/mes, el local es MÁS barato\n", breakevenReqs)
	} else {
		fmt.Println("    ↳ La API es más barata incluso al 100% de capacidad del hardware")
	}
}

func tablaCostoMensual(scenarios []int, costoPerCallAPI float64) {
	costoInfraMes := costoGPUHoraUSD * horasDiaActivas * 30

	fmt.Println("\n[tabla de costo mensual: API cloud vs local]")
	fmt.Printf("  %15s  %14s  %12s  %12s  Ventaja\n",
		"Requests/mes", "API cloud ($)", "Local ($)", "Diferencia")
	sep := "  " + strings.Repeat("-", 80)
	fmt.Println(sep)

	for _, req := range scenarios {
		costoAPI   := float64(req) * costoPerCallAPI
		costoLocal := costoInfraMes
		diff        := costoAPI - costoLocal
		ventaja := "API"
		if costoLocal < costoAPI {
			ventaja = "local"
		}
		fmt.Printf("  %15d  %14.2f  %12.2f  %+12.2f  %s\n",
			req, costoAPI, costoLocal, diff, ventaja)
	}
}

func main() {
	fmt.Println("=== Local vs API: latencia y break-even de costos ===")

	resAPI   := benchmarkAPICloud(3)
	resLocal := benchmarkOllamaLocal(3)

	if resLocal != nil {
		fmt.Println("\n[comparación directa]")
		ratioTTFT := resAPI.avgTTFT / resLocal.avgTTFT
		ratioLat  := resAPI.avgLatencia / resLocal.avgLatencia
		label := func(r float64) string {
			if r < 1 {
				return "API más rápida"
			}
			return "Local más rápida"
		}
		fmt.Printf("  API / Local TTFT ratio:     %.2fx  (%s)\n", ratioTTFT, label(ratioTTFT))
		fmt.Printf("  API / Local latencia ratio: %.2fx  (%s)\n", ratioLat, label(ratioLat))
	}

	calcularBreakeven(resAPI.costoPerCallUSD)
	tablaCostoMensual([]int{1_000, 10_000, 100_000, 500_000, 1_000_000}, resAPI.costoPerCallUSD)
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
