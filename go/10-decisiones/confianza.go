// Cómo ejecutar: make go FILE=go/10-decisiones/confianza.go
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

var model = envOr("MODEL", "claude-sonnet-4-6")
var apiURL = envBaseURL()

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type requestBody struct {
	Model       string    `json:"model"`
	MaxTokens   int       `json:"max_tokens"`
	Temperature float64   `json:"temperature"`
	Messages    []message `json:"messages"`
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type responseBody struct {
	Content []contentBlock `json:"content"`
}

var reNumero = regexp.MustCompile(`\b(\d+(?:\.\d+)?)\b`)

func extraerRespuesta(texto string) string {
	matches := reNumero.FindAllString(texto, -1)
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
	return texto[:min50(len(texto))]
}

func min50(n int) int {
	if n < 50 {
		return n
	}
	return 50
}

func llamarAPI(prompt string) (string, error) {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")

	body := requestBody{
		Model:       model,
		MaxTokens:   200,
		Temperature: 0.7,
		Messages:    []message{{Role: "user", Content: prompt}},
	}

	data, err := json.Marshal(body)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest("POST", apiURL, bytes.NewReader(data))
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

	var rb responseBody
	if err := json.Unmarshal(raw, &rb); err != nil {
		return "", err
	}
	if len(rb.Content) == 0 {
		return "", fmt.Errorf("respuesta vacía de la API")
	}
	return rb.Content[0].Text, nil
}

// selfConsistency lanza k llamadas en paralelo y devuelve (mejorRespuesta, confianza).
func selfConsistency(prompt string, k int) (string, float64, error) {
	resultados := make([]string, k)
	errs := make([]error, k)

	var wg sync.WaitGroup
	var mu sync.Mutex

	for i := 0; i < k; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			texto, err := llamarAPI(prompt)
			mu.Lock()
			if err != nil {
				errs[idx] = err
			} else {
				resultados[idx] = extraerRespuesta(texto)
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

func main() {
	pregunta := "¿Cuánto es 17 × 23? Razona paso a paso y da solo el número al final."

	fmt.Printf("Pregunta: %s\n", pregunta)
	fmt.Println("Muestreando k=5 respuestas en paralelo con temperature=0.7...\n")

	resultados := make([]string, 5)
	errs := make([]error, 5)
	var wg sync.WaitGroup
	var mu sync.Mutex

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			texto, err := llamarAPI(pregunta)
			mu.Lock()
			if err != nil {
				errs[idx] = err
			} else {
				resultados[idx] = extraerRespuesta(texto)
			}
			mu.Unlock()
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			fmt.Fprintf(os.Stderr, "error en muestra %d: %v\n", i+1, err)
			os.Exit(1)
		}
	}

	for i, r := range resultados {
		fmt.Printf("  Muestra %d: '%s'\n", i+1, r)
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
	confianza := float64(votos) / 5.0

	fmt.Printf("\nDistribución de votos: %v\n", conteos)
	fmt.Printf("Respuesta: %s\n", mejor)
	fmt.Printf("Confianza: %.2f (%d/5 votos)\n", confianza, votos)
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
