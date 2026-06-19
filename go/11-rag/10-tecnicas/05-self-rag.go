// Self-RAG: generación por segmentos con tokens de reflexión.
//
// Self-RAG (Asai et al., 2023) enseña al modelo a evaluar en tiempo de inferencia
// si necesita recuperar y si lo que recuperó es útil — sin pipeline externo.
// Los cuatro tokens de reflexión:
//
//	Retrieve={yes/no/continue}  — ¿necesito buscar para generar este segmento?
//	ISREL={relevant/irrelevant} — ¿el pasaje recuperado es relevante para la query?
//	ISSUP={fully/partially/no}  — ¿el pasaje apoya la afirmación generada?
//	ISUSE={1..5}                — ¿es útil esta respuesta para el usuario?
//
// Esta implementación simula los tokens de reflexión via prompting con Claude.
//
// Cómo ejecutar:
//
//	export ANTHROPIC_API_KEY=sk-ant-...
//	make go FILE=go/11-rag/10-tecnicas/05-self-rag.go
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
)

const anthropicVersion = "2023-06-01"

func anthropicEndpoint() string {
	if base := os.Getenv("ANTHROPIC_BASE_URL"); base != "" {
		return base + "/v1/messages"
	}
	return "https://api.anthropic.com/v1/messages"
}

var selfRagModel = func() string {
	if m := os.Getenv("MODEL"); m != "" {
		return m
	}
	return "claude-haiku-4-5-20251001"
}()

// ── Corpus y retriever mock ────────────────────────────────────────────────

var selfRagCorpus = []string{
	"RAG combina recuperación de documentos con generación del LLM para reducir alucinaciones.",
	"Self-RAG fine-tunea el modelo para generar tokens especiales que evalúan el retrieval.",
	"El token Retrieve indica si el segmento actual necesita información externa.",
	"ISREL evalúa si el pasaje recuperado es relevante para la query original.",
	"ISSUP evalúa si el pasaje apoya la afirmación que el modelo está generando.",
	"ISUSE evalúa la utilidad global de la respuesta en una escala del 1 al 5.",
	"Los modelos de lenguaje large tienden a alucinar hechos no presentes en su preentrenamiento.",
	"BM25 es una función de recuperación léxica que supera a TF-IDF en la mayoría de benchmarks.",
	"El fine-tuning de Self-RAG requiere un corpus de reflexión generado por un critic model.",
	"Advanced RAG usa BM25 + semántico para mejorar el recall en búsqueda híbrida.",
}

func selfRagTokenizar(texto string) []string {
	return strings.Fields(strings.ToLower(texto))
}

func selfRagBM25(query string, k int) []string {
	tokenized := make([][]string, len(selfRagCorpus))
	for i, d := range selfRagCorpus {
		tokenized[i] = selfRagTokenizar(d)
	}
	n := float64(len(selfRagCorpus))
	df := map[string]float64{}
	for _, tokens := range tokenized {
		seen := map[string]bool{}
		for _, t := range tokens {
			if !seen[t] {
				df[t]++
				seen[t] = true
			}
		}
	}
	var avgdl float64
	for _, t := range tokenized {
		avgdl += float64(len(t))
	}
	avgdl /= n
	const k1, b = 1.5, 0.75
	qTokens := selfRagTokenizar(query)
	type sc struct{ doc string; score float64 }
	scores := make([]sc, len(selfRagCorpus))
	for i, tokens := range tokenized {
		tf := map[string]int{}
		for _, t := range tokens {
			tf[t]++
		}
		dl := float64(len(tokens))
		var total float64
		for _, term := range qTokens {
			dfTerm := df[term]
			if dfTerm == 0 {
				continue
			}
			idf := math.Log((n-dfTerm+0.5)/(dfTerm+0.5) + 1)
			freq := float64(tf[term])
			total += idf * (freq * (k1 + 1)) / (freq + k1*(1-b+b*dl/avgdl))
		}
		scores[i] = sc{selfRagCorpus[i], total}
	}
	sort.Slice(scores, func(i, j int) bool { return scores[i].score > scores[j].score })
	var out []string
	for _, s := range scores {
		if len(out) >= k {
			break
		}
		out = append(out, s.doc)
	}
	return out
}

// ── Anthropic API ──────────────────────────────────────────────────────────

type srMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type srReq struct {
	Model     string  `json:"model"`
	MaxTokens int     `json:"max_tokens"`
	System    string  `json:"system,omitempty"`
	Messages  []srMsg `json:"messages"`
}

type srResp struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

func callClaude(system string, msgs []srMsg, maxTokens int) (string, error) {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	payload := srReq{
		Model:     selfRagModel,
		MaxTokens: maxTokens,
		System:    system,
		Messages:  msgs,
	}
	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", anthropicEndpoint(), bytes.NewReader(body))
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", anthropicVersion)
	req.Header.Set("content-type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	var ar srResp
	if err := json.Unmarshal(data, &ar); err != nil || len(ar.Content) == 0 {
		return "", fmt.Errorf("respuesta inválida: %s", data)
	}
	return ar.Content[0].Text, nil
}

// ── Tokens de reflexión via prompting ─────────────────────────────────────

func simRetrieve(query, contextoPrevio string) (string, error) {
	txt, err := callClaude(
		"Responde únicamente con una de estas opciones: yes | no | continue",
		[]srMsg{{Role: "user", Content: fmt.Sprintf(
			"Query: %s\nContexto generado hasta ahora: %q\n"+
				"¿El siguiente segmento necesita recuperar documentos externos? "+
				"(yes=sí necesita; no=no necesita; continue=ya hay suficiente)",
			query, contextoPrevio,
		)}},
		10,
	)
	if err != nil {
		return "no", err
	}
	token := strings.ToLower(strings.TrimSpace(txt))
	for _, v := range []string{"yes", "no", "continue"} {
		if v == token {
			return token, nil
		}
	}
	return "no", nil
}

func simIsrel(query, pasaje string) (string, error) {
	txt, err := callClaude(
		"Responde únicamente con: relevant | irrelevant",
		[]srMsg{{Role: "user", Content: fmt.Sprintf(
			"Query: %s\nPasaje: %s\n¿Es relevante el pasaje para la query?",
			query, pasaje,
		)}},
		10,
	)
	if err != nil {
		return "irrelevant", err
	}
	if strings.Contains(strings.ToLower(txt), "relevant") {
		return "relevant", nil
	}
	return "irrelevant", nil
}

func simSegmento(query, pasaje, contextoPrevio string) (string, error) {
	return callClaude(
		"Genera un segmento conciso (1-2 frases) apoyado en el pasaje proporcionado.",
		[]srMsg{{Role: "user", Content: fmt.Sprintf(
			"Query: %s\nPasaje de referencia: %s\nRespuesta generada hasta ahora: %q\n"+
				"Genera el siguiente segmento de la respuesta:",
			query, pasaje, contextoPrevio,
		)}},
		150,
	)
}

func simIssup(pasaje, segmento string) (string, error) {
	txt, err := callClaude(
		"Responde únicamente con: fully | partially | no",
		[]srMsg{{Role: "user", Content: fmt.Sprintf(
			"Pasaje: %s\nAfirmación: %s\n¿El pasaje apoya la afirmación?",
			pasaje, segmento,
		)}},
		10,
	)
	if err != nil {
		return "no", err
	}
	token := strings.ToLower(txt)
	if strings.Contains(token, "fully") {
		return "fully", nil
	}
	if strings.Contains(token, "partial") {
		return "partially", nil
	}
	return "no", nil
}

func simIsuse(query, respuesta string) (int, error) {
	txt, err := callClaude(
		"Responde únicamente con un número del 1 al 5.",
		[]srMsg{{Role: "user", Content: fmt.Sprintf(
			"Query: %s\nRespuesta: %s\n¿Cuál es la utilidad? (1=nula, 5=perfecta)",
			query, respuesta,
		)}},
		5,
	)
	if err != nil {
		return 3, err
	}
	n, err2 := strconv.Atoi(strings.TrimSpace(txt)[:1])
	if err2 != nil {
		return 3, nil
	}
	return n, nil
}

// ── Pipeline Self-RAG ──────────────────────────────────────────────────────

func selfRAG(query string, maxSegmentos int) (string, error) {
	fmt.Printf("\nQuery: %q\n%s\n", query, strings.Repeat("─", 60))

	var respuestaAcumulada string
	var segmentosValidos []string

	for i := 0; i < maxSegmentos; i++ {
		fmt.Printf("\n[Segmento %d]\n", i+1)

		retrieve, err := simRetrieve(query, respuestaAcumulada)
		if err != nil {
			return "", err
		}
		fmt.Printf("  Retrieve=%s\n", retrieve)

		if retrieve == "continue" {
			fmt.Println("  → generación suficiente, parando")
			break
		}

		if retrieve == "no" {
			segmento, err := callClaude("", []srMsg{{
				Role: "user",
				Content: fmt.Sprintf(
					"Query: %s\nContexto previo: %q\nContinúa la respuesta en 1-2 frases:",
					query, respuestaAcumulada,
				),
			}}, 100)
			if err != nil {
				return "", err
			}
			segmento = strings.TrimSpace(segmento)
			fmt.Printf("  (sin retrieval) %s\n", truncar(segmento, 80))
			segmentosValidos = append(segmentosValidos, segmento)
			respuestaAcumulada += " " + segmento
			continue
		}

		pasajes := selfRagBM25(query, 2)
		pasaje := ""
		if len(pasajes) > 0 {
			pasaje = pasajes[0]
		}
		fmt.Printf("  Pasaje: %s\n", truncar(pasaje, 70))

		isRel, err := simIsrel(query, pasaje)
		if err != nil {
			return "", err
		}
		fmt.Printf("  ISREL=%s\n", isRel)
		if isRel == "irrelevant" {
			fmt.Println("  → pasaje irrelevante, saltando segmento")
			continue
		}

		segmento, err := simSegmento(query, pasaje, respuestaAcumulada)
		if err != nil {
			return "", err
		}
		segmento = strings.TrimSpace(segmento)
		fmt.Printf("  Segmento: %s\n", truncar(segmento, 80))

		isSup, err := simIssup(pasaje, segmento)
		if err != nil {
			return "", err
		}
		fmt.Printf("  ISSUP=%s\n", isSup)
		if isSup == "no" {
			fmt.Println("  → segmento no apoyado por el pasaje, descartado")
			continue
		}

		segmentosValidos = append(segmentosValidos, segmento)
		respuestaAcumulada += " " + segmento
	}

	respuestaFinal := strings.TrimSpace(strings.Join(segmentosValidos, " "))
	isUse, err := simIsuse(query, respuestaFinal)
	if err != nil {
		return respuestaFinal, nil
	}
	fmt.Printf("\nISUSE=%d/5\n", isUse)
	return respuestaFinal, nil
}

func truncar(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// ── Main ──────────────────────────────────────────────────────────────────

func main() {
	queries := []string{
		"¿Qué es Self-RAG y en qué se diferencia del RAG clásico?",
		"¿Cuándo conviene usar retrieval en la generación?",
	}
	for _, q := range queries {
		resp, err := selfRAG(q, 3)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			continue
		}
		fmt.Printf("\n=== Respuesta final ===\n%s\n\n", resp)
	}
}
