// Demostración del efecto 'lost in the middle' y coste de estrategias de contexto.
//
// Muestra:
//  1. Construcción de contexto con hechos en inicio, medio y final
//  2. Accuracy de recuperación por posición
//  3. Costo en tokens: full-context vs RAG selectivo
//  4. Savings estimados y proyección a escala
//
// Sin SDK: HTTP directo contra la API de Anthropic.

// Cómo ejecutar: make go FILE=go/03-motor-llm/ventana-contexto.go

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

const (
	// Precios Haiku 4.5 (USD por millón de tokens, Mayo 2025)
	precioInputPorMillon  = 0.80
	precioOutputPorMillon = 4.00
	hechoInicio = "CÓDIGO_INICIO: el código de seguridad del servidor es ALFA-7742."
	hechoMedio  = "CÓDIGO_MEDIO: el código de seguridad del servidor es BETA-3319."
	hechoFinal  = "CÓDIGO_FINAL: el código de seguridad del servidor es GAMMA-8851."
	rellenoParagrafo = "El equipo de infraestructura realizó mantenimiento preventivo en todos los nodos " +
	"del clúster. Se actualizaron las dependencias de seguridad y se realizaron pruebas " +
	"de carga con resultados satisfactorios. El tiempo de respuesta promedio se mantuvo " +
	"por debajo de los 200ms durante toda la ventana de mantenimiento programada. " +
	"Los registros de auditoría no mostraron anomalías y el sistema quedó estable. "
)

var (
	ventanaMainModel = envOr("MODEL", "claude-sonnet-4-6")
	ventanaSmallModel = envOr("SMALL_MODEL", "claude-haiku-4-5-20251001")
	ventanaAPIURL = envBaseURL()
)

// ─── API helpers ──────────────────────────────────────────────────────────

type ventanaMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ventanaResp struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func ventanaCallAPI(system, userContent string) (string, error) {
	payload := map[string]any{
		"model":      ventanaSmallModel,
		"max_tokens": 64,
		"system":     system,
		"messages":   []ventanaMsg{{Role: "user", Content: userContent}},
	}
	body, _ := json.Marshal(payload)
	req, _ := http.NewRequestWithContext(context.Background(), "POST", ventanaAPIURL, bytes.NewReader(body))
	req.Header.Set("x-api-key", os.Getenv("ANTHROPIC_API_KEY"))
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("content-type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)
	var r ventanaResp
	if err := json.Unmarshal(data, &r); err != nil {
		return "", fmt.Errorf("parse %s: %w", string(data), err)
	}
	if r.Error != nil {
		return "", fmt.Errorf("API error: %s", r.Error.Message)
	}
	var sb strings.Builder
	for _, b := range r.Content {
		if b.Type == "text" {
			sb.WriteString(b.Text)
		}
	}
	return strings.TrimSpace(sb.String()), nil
}

// ─── 1. Construir contexto ────────────────────────────────────────────────

func construirContexto(rellenoBloques int) string {
	bloqueRelleno := strings.Repeat(rellenoParagrafo, 5) + "\n\n"
	mitad := rellenoBloques / 2

	var sb strings.Builder
	sb.WriteString("=== INFORME DE INFRAESTRUCTURA ===\n\n")
	sb.WriteString(hechoInicio + "\n\n")
	for i := 0; i < mitad; i++ {
		sb.WriteString(bloqueRelleno)
	}
	sb.WriteString(hechoMedio + "\n\n")
	for i := 0; i < mitad; i++ {
		sb.WriteString(bloqueRelleno)
	}
	sb.WriteString(hechoFinal + "\n\n")
	sb.WriteString("=== FIN DEL INFORME ===\n")
	return sb.String()
}

// ─── 2. Medir accuracy por posición ──────────────────────────────────────

type preguntaConfig struct {
	pregunta string
	esperado string
}

var preguntas = map[string]preguntaConfig{
	"inicio": {"¿Cuál es el CÓDIGO_INICIO mencionado en el informe?", "ALFA-7742"},
	"medio":  {"¿Cuál es el CÓDIGO_MEDIO mencionado en el informe?", "BETA-3319"},
	"final":  {"¿Cuál es el CÓDIGO_FINAL mencionado en el informe?", "GAMMA-8851"},
}

func medirAccuracyPorPosicion(contexto string, repeticiones int) {
	system := "Responde SOLO con el código pedido, sin explicaciones. " +
		"Si no lo encuentras, responde 'NO_ENCONTRADO'."

	fmt.Println("\n[accuracy de recuperación por posición del hecho]")
	fmt.Printf("  Repeticiones por posición: %d\n\n", repeticiones)

	for _, posicion := range []string{"inicio", "medio", "final"} {
		cfg := preguntas[posicion]
		aciertos := 0

		for rep := 0; rep < repeticiones; rep++ {
			userContent := fmt.Sprintf("%s\n\n---\n%s", contexto, cfg.pregunta)
			texto, err := ventanaCallAPI(system, userContent)
			if err != nil {
				fmt.Printf("  %s rep%d: error — %v\n", posicion, rep+1, err)
				continue
			}
			if strings.Contains(texto, cfg.esperado) {
				aciertos++
			}
		}

		tasa := float64(aciertos) / float64(repeticiones) * 100
		fmt.Printf("  %-6s  accuracy=%.0f%%  (esperado: %s)\n",
			posicion, tasa, cfg.esperado)
	}
}

// ─── 3. Comparar estrategias de contexto ──────────────────────────────────

// estimarTokens aproxima len(texto) / 4 (heurística: ~4 chars por token).
func estimarTokens(texto string) int {
	return (len(texto) + 3) / 4
}

func calcularCostoUSD(tokensInput, tokensOutput int) float64 {
	return float64(tokensInput)/1_000_000*precioInputPorMillon +
		float64(tokensOutput)/1_000_000*precioOutputPorMillon
}

func compararEstrategiasContexto(contextoFull string) {
	fmt.Println("\n[comparación de estrategias de contexto]")

	tokensFull := estimarTokens(contextoFull)

	chunks := []struct {
		nombre string
		chunk  string
	}{
		{"inicio (RAG)", fmt.Sprintf("Sección de inicio del informe:\n%s", hechoInicio)},
		{"medio (RAG)",  fmt.Sprintf("Sección intermedia del informe:\n%s", hechoMedio)},
		{"final (RAG)",  fmt.Sprintf("Sección final del informe:\n%s", hechoFinal)},
	}

	for _, c := range chunks {
		tokensChunk := estimarTokens(c.chunk)
		savingTokens := tokensFull - tokensChunk
		savingPct := float64(savingTokens) / float64(tokensFull) * 100
		costoFull := calcularCostoUSD(tokensFull, 50)
		costoChunk := calcularCostoUSD(tokensChunk, 50)
		ahorroUSD := costoFull - costoChunk

		fmt.Printf("\n  Recuperar hecho de %s:\n", c.nombre)
		fmt.Printf("    Full-context:  %6d tokens  $%.6f\n", tokensFull, costoFull)
		fmt.Printf("    Solo chunk:    %6d tokens  $%.6f\n", tokensChunk, costoChunk)
		fmt.Printf("    Ahorro:        %6d tokens  (%.1f%%)  $%.6f\n",
			savingTokens, savingPct, ahorroUSD)
	}

	// Proyección a escala
	requestsDia := 10_000
	costoFullDia := calcularCostoUSD(tokensFull, 50) * float64(requestsDia)
	costoRAGDia  := calcularCostoUSD(estimarTokens(chunks[0].chunk), 50) * float64(requestsDia)
	ratio := costoFullDia / costoRAGDia

	fmt.Printf("\n  Proyección a %d requests/día:\n", requestsDia)
	fmt.Printf("    Full-context:  $%.2f/día  ≈ $%.0f/mes\n",
		costoFullDia, costoFullDia*30)
	fmt.Printf("    RAG selectivo: $%.2f/día  ≈ $%.0f/mes\n",
		costoRAGDia, costoRAGDia*30)
	fmt.Printf("    Full-context cuesta ~%.0fx más que RAG selectivo\n", ratio)
}

func main() {
	fmt.Println("=== Ventana de contexto: lost-in-the-middle y coste de estrategias ===")

	contexto := construirContexto(30)
	tokensTotales := estimarTokens(contexto)
	palabras := len(strings.Fields(contexto))
	fmt.Printf("\nContexto construido: ~%d tokens (%d palabras)\n",
		tokensTotales, palabras)

	medirAccuracyPorPosicion(contexto, 3)
	compararEstrategiasContexto(contexto)
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
