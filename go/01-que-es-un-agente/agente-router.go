// Nivel ★☆☆ router: LLM elige una ruta entre N. Sin loop, sin tools.

// Cómo ejecutar: make go FILE=go/01-que-es-un-agente/agente-router.go

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

var (
	routerModel = envOr("MODEL", "claude-sonnet-4-6")
	routerAPIURL = envBaseURL()
)

var rutas = []string{"facturacion", "soporte_tecnico", "ventas", "otro"}

func route(input string) string {
	system := "Clasifica el mensaje del usuario en exactamente una de estas rutas: " +
		strings.Join(rutas, ", ") +
		". Responde solo con el nombre de la ruta, sin explicacion ni puntuacion."
	body, _ := json.Marshal(map[string]any{
		"model":      routerModel,
		"max_tokens": 16,
		"system":     system,
		"messages":   []map[string]string{{"role": "user", "content": input}},
	})
	req, _ := http.NewRequestWithContext(context.Background(), "POST", routerAPIURL, bytes.NewReader(body))
	req.Header.Set("x-api-key", os.Getenv("ANTHROPIC_API_KEY"))
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("content-type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "otro"
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	var r struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return "otro"
	}
	text := ""
	for _, b := range r.Content {
		if b.Type == "text" {
			text += b.Text
		}
	}
	text = strings.ToLower(strings.TrimSpace(text))
	for _, ruta := range rutas {
		if ruta == text {
			return text
		}
	}
	return "otro"
}

func main() {
	cases := []string{
		"No me llego la factura de marzo",
		"El servicio se cae cada vez que entro",
		"Quiero cambiar al plan empresarial",
		"Hace buen tiempo hoy",
	}
	for _, c := range cases {
		fmt.Printf("%-18s  %s\n", route(c), c)
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
