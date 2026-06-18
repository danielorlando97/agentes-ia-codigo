// Control de costos: presupuesto por tarea, routing de modelos, alertas de gasto

// Cómo ejecutar: make go FILE=go/17-produccion/costos.go

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

// Precios en USD por millón de tokens (mayo 2025)
var precios = map[string]map[string]float64{
	"claude-haiku-4-5-20251001":  {"input": 0.80, "output": 4.00},
	"claude-sonnet-4-6-20250219": {"input": 3.00, "output": 15.00},
	"claude-opus-4-7-20250219":   {"input": 15.00, "output": 75.00},
}

var modeloPorTarea = map[string]string{
	"clasificar":        "claude-haiku-4-5-20251001",
	"extraer_campo":     "claude-haiku-4-5-20251001",
	"verificar_bool":    "claude-haiku-4-5-20251001",
	"resumir_breve":     "claude-haiku-4-5-20251001",
	"revisar_codigo":    "claude-sonnet-4-6-20250219",
	"analizar_doc":      "claude-sonnet-4-6-20250219",
	"generar_codigo":    "claude-sonnet-4-6-20250219",
	"arquitectura":      "claude-opus-4-7-20250219",
	"analisis_profundo": "claude-opus-4-7-20250219",
}

func seleccionarModelo(tipoTarea string) string {
	if m, ok := modeloPorTarea[tipoTarea]; ok {
		return m
	}
	return "claude-sonnet-4-6-20250219"
}

func costeLlamada(modelo string, tokensInput, tokensOutput int) float64 {
	p, ok := precios[modelo]
	if !ok {
		p = map[string]float64{"input": 3.00, "output": 15.00}
	}
	return (float64(tokensInput)*p["input"] + float64(tokensOutput)*p["output"]) / 1_000_000
}

type presupuestoTarea struct {
	maxPasos        int
	maxTokensInput  int
	maxTokensOutput int
	maxCosteUsd     float64
	tokensInput     int
	tokensOutput    int
	pasos           int
	coste           float64
}

func nuevoPresupuesto() *presupuestoTarea {
	return &presupuestoTarea{
		maxPasos:        15,
		maxTokensInput:  50_000,
		maxTokensOutput: 10_000,
		maxCosteUsd:     0.50,
	}
}

func (p *presupuestoTarea) registrar(modelo string, tokensInput, tokensOutput int) {
	p.tokensInput += tokensInput
	p.tokensOutput += tokensOutput
	p.pasos++
	p.coste += costeLlamada(modelo, tokensInput, tokensOutput)
}

func (p *presupuestoTarea) verificar() (bool, string) {
	if p.pasos >= p.maxPasos {
		return false, fmt.Sprintf("pasos=%d >= max=%d", p.pasos, p.maxPasos)
	}
	if p.tokensInput >= p.maxTokensInput {
		return false, fmt.Sprintf("tokens_input=%d >= max=%d", p.tokensInput, p.maxTokensInput)
	}
	if p.coste >= p.maxCosteUsd {
		return false, fmt.Sprintf("coste=$%.4f >= max=$%.2f", p.coste, p.maxCosteUsd)
	}
	return true, ""
}

func (p *presupuestoTarea) resumen() map[string]interface{} {
	return map[string]interface{}{
		"pasos":         p.pasos,
		"tokens_input":  p.tokensInput,
		"tokens_output": p.tokensOutput,
		"coste_usd":     math64Round(p.coste, 6),
	}
}

func math64Round(x float64, decimals int) float64 {
	factor := 1.0
	for i := 0; i < decimals; i++ {
		factor *= 10
	}
	return float64(int(x*factor+0.5)) / factor
}

type costosMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type costosRequest struct {
	Model     string      `json:"model"`
	MaxTokens int         `json:"max_tokens"`
	Messages  []costosMsg `json:"messages"`
}

type costosResponse struct {
	StopReason string `json:"stop_reason"`
	Content    []struct {
		Text string `json:"text"`
	} `json:"content"`
	Usage struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

func llamarAnthropicCostos(modelo string, mensajes []costosMsg) (costosResponse, error) {
	payload := costosRequest{Model: modelo, MaxTokens: 512, Messages: mensajes}
	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", envBaseURL(), bytes.NewReader(body))
	req.Header.Set("x-api-key", os.Getenv("ANTHROPIC_API_KEY"))
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("content-type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return costosResponse{}, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	var ar costosResponse
	return ar, json.Unmarshal(data, &ar)
}

func loopConPresupuesto(pregunta, tipoTarea string) (map[string]interface{}, error) {
	presupuesto := nuevoPresupuesto()
	modelo := seleccionarModelo(tipoTarea)
	mensajes := []costosMsg{{Role: "user", Content: pregunta}}

	for {
		ok, motivo := presupuesto.verificar()
		if !ok {
			fmt.Printf("[WARN] Presupuesto agotado: %s\n", motivo)
			return map[string]interface{}{
				"error":  motivo,
				"parcial": true,
				"uso":    presupuesto.resumen(),
			}, nil
		}

		respuesta, err := llamarAnthropicCostos(modelo, mensajes)
		if err != nil {
			return nil, err
		}

		presupuesto.registrar(modelo, respuesta.Usage.InputTokens, respuesta.Usage.OutputTokens)

		if respuesta.StopReason == "end_turn" {
			resumenJSON, _ := json.Marshal(presupuesto.resumen())
			fmt.Printf("[INFO] Tarea completada: %s\n", resumenJSON)
			return map[string]interface{}{
				"resultado": respuesta.Content[0].Text,
				"uso":       presupuesto.resumen(),
			}, nil
		}

		mensajes = append(mensajes, costosMsg{Role: "assistant", Content: respuesta.Content[0].Text})
	}
}

func demostrarRouting() {
	tareas := [][2]string{
		{"clasificar", "¿Este texto es spam? 'Gana dinero fácil'"},
		{"revisar_codigo", "def fib(n): return fib(n-1)+fib(n-2)"},
		{"analisis_profundo", "Propón una arquitectura de microservicios para pagos"},
	}
	for _, t := range tareas {
		modelo := seleccionarModelo(t[0])
		costeEstimado := costeLlamada(modelo, 500, 200)
		partes := strings.Split(modelo, "-")
		fmt.Printf("[routing] tipo=%s → modelo=%s | coste_estimado=$%.6f\n", t[0], partes[1], costeEstimado)
	}
}

func main() {
	fmt.Println("=== Routing de modelos ===")
	demostrarRouting()

	fmt.Println("\n=== Loop con presupuesto ===")
	resultado, err := loopConPresupuesto(
		"Analiza brevemente los tradeoffs de usar Redis vs SQLite para caché.",
		"analizar_doc",
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	texto := ""
	if r, ok := resultado["resultado"].(string); ok {
		texto = r
	} else if e, ok := resultado["error"].(string); ok {
		texto = e
	}
	if len(texto) > 200 {
		texto = texto[:200]
	}
	fmt.Printf("Resultado: %s\n", texto)
	usoJSON, _ := json.Marshal(resultado["uso"])
	fmt.Printf("Uso: %s\n", usoJSON)
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
