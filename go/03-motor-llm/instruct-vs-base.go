// Comparación entre modelo instruct y modelo base (simulada).
//
// Muestra:
//  1. Diferencia de formato: instrucción directa vs completar texto
//  2. Diferencia de output: seguimiento de instrucciones vs continuación libre
//  3. Tokens consumidos y tasa de seguimiento de instrucciones
//
// NOTA: Anthropic no expone modelos base en su API pública.
// Este script simula el contraste enviando dos tipos de prompt al mismo modelo
// instruct y midiendo el seguimiento de instrucciones en cada caso.
//
// Sin SDK: HTTP directo contra la API de Anthropic.

// Cómo ejecutar: make go FILE=go/03-motor-llm/instruct-vs-base.go

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
)

const (
	promptInstruct = "Lista los tres pasos principales para preparar una taza de té. " +
	"Sé conciso. Usa formato de lista numerada."
	promptBase = "Para preparar una taza de té, primero"
	systemBaseSim = "Continúa el texto que se te da. No añadas saludos ni despedidas. " +
	"No uses formato de lista a menos que el texto de entrada lo sugiera. " +
	"Escribe en el mismo registro y tono del texto de entrada."
)

var (
	instructMainModel = envOr("MODEL", "claude-sonnet-4-6")
	instructSmallModel = envOr("SMALL_MODEL", "claude-haiku-4-5-20251001")
	instructAPIURL = envBaseURL()
)

// ─── API helpers ──────────────────────────────────────────────────────────

type instructMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type instructResp struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Usage struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func instructCallAPI(system, userContent string) (*instructResp, error) {
	payload := map[string]any{
		"model":      instructSmallModel,
		"max_tokens": 200,
		"messages":   []instructMsg{{Role: "user", Content: userContent}},
	}
	if system != "" {
		payload["system"] = system
	}

	body, _ := json.Marshal(payload)
	req, _ := http.NewRequestWithContext(context.Background(), "POST", instructAPIURL, bytes.NewReader(body))
	req.Header.Set("x-api-key", os.Getenv("ANTHROPIC_API_KEY"))
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("content-type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)
	var r instructResp
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("parse %s: %w", string(data), err)
	}
	if r.Error != nil {
		return nil, fmt.Errorf("API error: %s", r.Error.Message)
	}
	return &r, nil
}

func instructExtractText(r *instructResp) string {
	var sb strings.Builder
	for _, b := range r.Content {
		if b.Type == "text" {
			sb.WriteString(b.Text)
		}
	}
	return sb.String()
}

// ─── Métricas ─────────────────────────────────────────────────────────────

var reLista = regexp.MustCompile(`(?m)^\s*[123]\.`)

func detectarListaNumerada(texto string) bool {
	return reLista.MatchString(texto)
}

func detectarTresPasos(texto string) bool {
	matches := regexp.MustCompile(`(?m)^\s*\d+\.`).FindAllString(texto, -1)
	return len(matches) == 3
}

// ─── 2. Medir seguimiento de instrucciones ────────────────────────────────

type configPrompt struct {
	label       string
	prompt      string
	system      string
	descripcion string
}

func medirSeguimientoInstrucciones(repeticiones int) {
	fmt.Println("\n[comparación: prompt instruct vs prompt base]")
	fmt.Printf("  Repeticiones: %d\n\n", repeticiones)

	configuraciones := []configPrompt{
		{
			label:       "instruct-prompt",
			prompt:      promptInstruct,
			descripcion: "Instrucción directa (formato imperativo con requisitos explícitos)",
		},
		{
			label:       "base-sim-prompt",
			prompt:      promptBase,
			system:      systemBaseSim,
			descripcion: "Continuación de texto (simulación del estilo base model)",
		},
	}

	for _, cfg := range configuraciones {
		fmt.Printf("  --- %s ---\n", cfg.label)
		fmt.Printf("  %s\n", cfg.descripcion)
		fmt.Printf("  Prompt: %q\n\n", cfg.prompt)

		var tasaLista, tasa3Pasos, totalTokens int
		var outputs []string

		for rep := 0; rep < repeticiones; rep++ {
			r, err := instructCallAPI(cfg.system, cfg.prompt)
			if err != nil {
				fmt.Printf("  rep%d: error — %v\n", rep+1, err)
				continue
			}
			texto := instructExtractText(r)
			outputs = append(outputs, texto)

			if detectarListaNumerada(texto) {
				tasaLista++
			}
			if detectarTresPasos(texto) {
				tasa3Pasos++
			}
			totalTokens += r.Usage.InputTokens + r.Usage.OutputTokens
		}

		if len(outputs) == 0 {
			fmt.Println("  Sin resultados\n")
			continue
		}

		avgTokens := float64(totalTokens) / float64(len(outputs))
		fmt.Printf("  Tasa lista numerada:  %d/%d (%.0f%%)\n",
			tasaLista, repeticiones, float64(tasaLista)/float64(repeticiones)*100)
		fmt.Printf("  Tasa 3 ítems exactos: %d/%d (%.0f%%)\n",
			tasa3Pasos, repeticiones, float64(tasa3Pasos)/float64(repeticiones)*100)
		fmt.Printf("  Tokens promedio/call: %.0f\n\n", avgTokens)
		fmt.Println("  Outputs:")
		for i, out := range outputs {
			truncated := out
			if len(truncated) > 120 {
				truncated = truncated[:120]
			}
			fmt.Printf("    rep%d: %q\n", i+1, truncated)
		}
		fmt.Println()
	}
}

// ─── 3. Tabla de diferencias ──────────────────────────────────────────────

func tablaDiferencias() {
	fmt.Println("\n[tabla: diferencias documentadas base vs instruct]")
	type fila struct {
		dim, instruct, base string
	}
	filas := []fila{
		{"Formato de prompt",  "Instrucción imperativa directa", "Texto a completar"},
		{"Output esperado",    "Sigue instrucciones explícitas", "Continúa el texto dado"},
		{"Saludos/formato",    "Sí (conversacional por defecto)", "No (texto plano)"},
		{"Seguimiento reglas", "Alto (RLHF/SFT orientado)",      "Bajo (no fine-tuneado)"},
		{"Uso en agentes",     "Siempre (tool calling, system)", "Nunca directamente"},
		{"Acceso API",         "Público (claude-haiku, sonnet)", "No expuesto por Anthropic"},
		{"Temperatura típica", "0.0–1.0 según tarea",            "0.7–1.0 para completar"},
	}
	fmt.Printf("  %-30s  %-35s  %s\n", "Dimensión", "Instruct model", "Base model")
	fmt.Println("  " + strings.Repeat("-", 100))
	for _, f := range filas {
		fmt.Printf("  %-30s  %-35s  %s\n", f.dim, f.instruct, f.base)
	}
	fmt.Println()
}

func main() {
	fmt.Println("=== Instruct vs Base: diferencias de comportamiento ===")
	medirSeguimientoInstrucciones(3)
	tablaDiferencias()
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
