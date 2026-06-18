// Cómo ejecutar: make go FILE=go/10-decisiones/abstencion.go
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
)

var modelAbs = envOr("MODEL", "claude-sonnet-4-6")
var apiURLAbs = envBaseURL()

const umbralResponder = 0.8
const umbralSoft = 0.5

type PredictionResult struct {
	Tipo      string  // "respuesta" | "soft" | "abstencion"
	Contenido string
	Confianza float64
}

type msgAbs struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type reqBodyAbs struct {
	Model       string   `json:"model"`
	MaxTokens   int      `json:"max_tokens"`
	Temperature float64  `json:"temperature"`
	Messages    []msgAbs `json:"messages"`
}

type contentBlockAbs struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type respBodyAbs struct {
	Content []contentBlockAbs `json:"content"`
}

var reNumeroAbs = regexp.MustCompile(`\b(\d+(?:\.\d+)?)\b`)

func extraerRespuestaAbs(texto string) string {
	matches := reNumeroAbs.FindAllString(texto, -1)
	if len(matches) > 0 {
		return matches[len(matches)-1]
	}
	lineas := strings.Split(texto, "\n")
	for i := len(lineas) - 1; i >= 0; i-- {
		l := strings.TrimSpace(lineas[i])
		if l != "" {
			if len(l) > 50 {
				return l[:50]
			}
			return l
		}
	}
	if len(texto) > 50 {
		return texto[:50]
	}
	return texto
}

func llamarAPIAbs(prompt string) (string, error) {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")

	body := reqBodyAbs{
		Model:       modelAbs,
		MaxTokens:   200,
		Temperature: 0.7,
		Messages:    []msgAbs{{Role: "user", Content: prompt}},
	}

	data, err := json.Marshal(body)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest("POST", apiURLAbs, bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("content-type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var rb respBodyAbs
	if err := json.Unmarshal(raw, &rb); err != nil {
		return "", err
	}
	if len(rb.Content) == 0 {
		return "", fmt.Errorf("respuesta vacía de la API")
	}
	return rb.Content[0].Text, nil
}

// selfConsistencyAbs lanza k llamadas en paralelo con goroutines.
func selfConsistencyAbs(prompt string, k int) (string, float64, error) {
	resultados := make([]string, k)
	errs := make([]error, k)

	var wg sync.WaitGroup
	var mu sync.Mutex

	for i := 0; i < k; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			texto, err := llamarAPIAbs(prompt)
			mu.Lock()
			if err != nil {
				errs[idx] = err
			} else {
				resultados[idx] = extraerRespuestaAbs(texto)
			}
			mu.Unlock()
		}(i)
	}
	wg.Wait()

	for _, err := range errs {
		if err != nil {
			return "", 0, err
		}
	}

	conteos := make(map[string]int)
	for _, r := range resultados {
		conteos[r]++
	}

	var mejor string
	var votos int
	for respuesta, count := range conteos {
		if count > votos {
			mejor = respuesta
			votos = count
		}
	}

	return mejor, float64(votos) / float64(k), nil
}

func selectivePredict(query string) (PredictionResult, error) {
	respuesta, confianza, err := selfConsistencyAbs(query, 5)
	if err != nil {
		return PredictionResult{}, err
	}

	if confianza >= umbralResponder {
		return PredictionResult{
			Tipo:      "respuesta",
			Contenido: respuesta,
			Confianza: confianza,
		}, nil
	} else if confianza >= umbralSoft {
		return PredictionResult{
			Tipo:      "soft",
			Contenido: fmt.Sprintf("Según mi información (no verificada): %s. Recomiendo verificar.", respuesta),
			Confianza: confianza,
		}, nil
	}
	return PredictionResult{
		Tipo:      "abstencion",
		Contenido: "No tengo suficiente certeza para responder esto correctamente.",
		Confianza: confianza,
	}, nil
}

func main() {
	queries := []string{
		"¿Cuánto es 8 × 7? Da solo el número.",
		"¿Quién ganó el Premio Nobel de Literatura en 2019?",
		"¿Cuál es el precio exacto de la acción de Apple en este momento?",
	}

	for _, q := range queries {
		fmt.Printf("Query: %s\n", q)
		resultado, err := selectivePredict(q)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  error: %v\n", err)
			continue
		}
		fmt.Printf("  Tipo:      %s\n", resultado.Tipo)
		fmt.Printf("  Contenido: %s\n", resultado.Contenido)
		fmt.Printf("  Confianza: %.2f\n", resultado.Confianza)
		fmt.Println()
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
