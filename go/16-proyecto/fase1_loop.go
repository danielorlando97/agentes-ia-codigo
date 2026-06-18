// Fase 1: llamada única al LLM sin herramientas. Verifica que el system prompt
// produce el schema JSON correcto antes de añadir complejidad.

// Cómo ejecutar: make go FILE=go/16-proyecto/fase1_loop.go

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
)
var (
	model  = envOr("MODEL", "claude-sonnet-4-6")
	apiURL = envBaseURL()
)

const systemPromptFase1 = `Eres un agente de revisión de código.
Recibes código Python y produces una revisión técnica estructurada.

Tu revisión debe identificar:
- Bugs (severidad: critical, high, medium, low)
- Problemas de estilo o mantenibilidad
- Sugerencias de mejora

Responde SIEMPRE en JSON con este schema:
{
  "hallazgos": [
    {
      "linea": <número o null>,
      "severidad": "<critical|high|medium|low>",
      "tipo": "<bug|estilo|rendimiento|seguridad>",
      "descripcion": "<descripción del hallazgo>",
      "sugerencia": "<cómo corregirlo>"
    }
  ],
  "resumen": "<párrafo de resumen de la revisión>"
}`

// ---- Tipos mínimos de la API de Anthropic ----

type Fase1Request struct {
	Model     string          `json:"model"`
	MaxTokens int             `json:"max_tokens"`
	System    string          `json:"system"`
	Messages  []Fase1Message  `json:"messages"`
}

type Fase1Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type Fase1Response struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

// ---- Llamada a la API ----

func llamarAnthropicFase1(req Fase1Request) (*Fase1Response, error) {
	body, _ := json.Marshal(req)
	httpReq, _ := http.NewRequest("POST", apiURL, bytes.NewReader(body))
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", os.Getenv("ANTHROPIC_API_KEY"))
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, respBody)
	}

	var result Fase1Response
	json.Unmarshal(respBody, &result)
	return &result, nil
}

// ---- Lógica principal ----

func revisarCodigo(codigo string) (map[string]interface{}, error) {
	req := Fase1Request{
		Model:     model,
		MaxTokens: 2048,
		System:    systemPromptFase1,
		Messages: []Fase1Message{
			{
				Role:    "user",
				Content: fmt.Sprintf("Revisa este código:\n\n```python\n%s\n```", codigo),
			},
		},
	}

	resp, err := llamarAnthropicFase1(req)
	if err != nil {
		return nil, err
	}

	if len(resp.Content) == 0 || resp.Content[0].Type != "text" {
		return nil, fmt.Errorf("respuesta sin bloque de texto")
	}

	var resultado map[string]interface{}
	if err := json.Unmarshal([]byte(resp.Content[0].Text), &resultado); err != nil {
		return nil, fmt.Errorf("JSON inválido en respuesta: %w", err)
	}
	return resultado, nil
}

func main() {
	codigoEjemplo := `
def calcular_promedio(numeros):
    total = 0
    for n in numeros:
        total += n
    return total / len(numeros)  # bug: ZeroDivisionError si numeros está vacío

usuarios = {}
def get_usuario(id):
    return usuarios[id]  # bug: KeyError si id no existe
`

	resultado, err := revisarCodigo(codigoEjemplo)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}

	output, _ := json.MarshalIndent(resultado, "", "  ")
	fmt.Println(string(output))
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
