// Patrón supervisor/worker: un LLM descompone la tarea y despacha a workers especializados.

// Cómo ejecutar: make go FILE=go/12-multi-agente/supervisor_worker.go

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

var model = envOr("SMALL_MODEL", "claude-haiku-4-5-20251001")

var workers = map[string]string{
	"investigador": "Eres un investigador. Busca y sintetiza información factual. Devuelve hechos concretos.",
	"redactor":     "Eres un redactor. Redacta contenido claro y bien estructurado basado en el contexto dado.",
	"revisor":      "Eres un revisor. Identifica problemas concretos y devuelve el texto corregido.",
}

const supervisorSystem = `Eres un supervisor que descompone tareas y las despacha a workers.
Workers disponibles: investigador, redactor, revisor.
Planifica los pasos necesarios. Responde SIEMPRE con JSON válido.

Para planificar: {"accion": "planificar", "pasos": [{"worker": "<nombre>", "instruccion": "<qué hacer>"}]}
Para terminar:   {"accion": "terminar", "respuesta": "<respuesta final>"}
Para redirigir:  {"accion": "redirigir", "worker": "<nombre>", "correccion": "<qué corregir>"}`

type Mensaje struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type AnthropicRequest struct {
	Model     string    `json:"model"`
	MaxTokens int       `json:"max_tokens"`
	System    string    `json:"system"`
	Messages  []Mensaje `json:"messages"`
}

type ContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type AnthropicResponse struct {
	Content []ContentBlock `json:"content"`
}

type Paso struct {
	Worker     string `json:"worker"`
	Instruccion string `json:"instruccion"`
}

type Decision struct {
	Accion    string `json:"accion"`
	Pasos     []Paso `json:"pasos,omitempty"`
	Respuesta string `json:"respuesta,omitempty"`
	Worker    string `json:"worker,omitempty"`
	Correccion string `json:"correccion,omitempty"`
}

func llamarLLM(system string, mensajes []Mensaje, maxTokens int) (string, error) {
	payload := AnthropicRequest{
		Model:     model,
		MaxTokens: maxTokens,
		System:    system,
		Messages:  mensajes,
	}
	body, _ := json.Marshal(payload)

	req, _ := http.NewRequest("POST", envBaseURL(), bytes.NewReader(body))
	req.Header.Set("x-api-key", os.Getenv("ANTHROPIC_API_KEY"))
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("content-type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)
	var ar AnthropicResponse
	if err := json.Unmarshal(data, &ar); err != nil || len(ar.Content) == 0 {
		return "", fmt.Errorf("respuesta inesperada: %s", data)
	}
	return ar.Content[0].Text, nil
}

func llamarWorker(worker, instruccion string) (string, error) {
	system, ok := workers[worker]
	if !ok {
		return "", fmt.Errorf("worker desconocido: %s", worker)
	}
	return llamarLLM(system, []Mensaje{{Role: "user", Content: instruccion}}, 800)
}

func llamarSupervisor(mensajes []Mensaje) (Decision, error) {
	texto, err := llamarLLM(supervisorSystem, mensajes, 600)
	if err != nil {
		return Decision{}, err
	}
	inicio := strings.Index(texto, "{")
	fin := strings.LastIndex(texto, "}") + 1
	if inicio < 0 || fin <= inicio {
		return Decision{}, fmt.Errorf("JSON no encontrado en: %s", texto)
	}
	var d Decision
	return d, json.Unmarshal([]byte(texto[inicio:fin]), &d)
}

func supervisorWorker(tarea string, maxRondas int) (string, error) {
	mensajes := []Mensaje{{Role: "user", Content: "Tarea: " + tarea}}
	resultados := map[string]string{}

	// Fase 1: planificación
	decision, err := llamarSupervisor(mensajes)
	if err != nil {
		return "", err
	}
	decisionJSON, _ := json.Marshal(decision)
	mensajes = append(mensajes, Mensaje{Role: "assistant", Content: string(decisionJSON)})

	if decision.Accion != "planificar" {
		return decision.Respuesta, nil
	}

	// Fase 2: ejecución del plan
	for _, paso := range decision.Pasos {
		instruccion := paso.Instruccion
		for nombre, resultado := range resultados {
			if len(resultado) > 500 {
				resultado = resultado[:500]
			}
			instruccion = strings.ReplaceAll(instruccion, "$"+nombre, resultado)
		}

		resultado, err := llamarWorker(paso.Worker, instruccion)
		if err != nil {
			return "", err
		}
		resultados[paso.Worker] = resultado
		mensajes = append(mensajes, Mensaje{
			Role:    "user",
			Content: fmt.Sprintf("Resultado de %s:\n%s", paso.Worker, resultado),
		})
	}

	// Fase 3: evaluación del supervisor
	for ronda := 0; ronda < maxRondas; ronda++ {
		mensajes = append(mensajes, Mensaje{
			Role:    "user",
			Content: "¿La tarea está completa? Responde con JSON: terminar o redirigir.",
		})
		decision, err = llamarSupervisor(mensajes)
		if err != nil {
			return "", err
		}
		decisionJSON, _ = json.Marshal(decision)
		mensajes = append(mensajes, Mensaje{Role: "assistant", Content: string(decisionJSON)})

		if decision.Accion == "terminar" {
			return decision.Respuesta, nil
		}

		if decision.Accion == "redirigir" {
			resultado, err := llamarWorker(decision.Worker, decision.Correccion)
			if err != nil {
				return "", err
			}
			resultados[decision.Worker] = resultado
			mensajes = append(mensajes, Mensaje{
				Role:    "user",
				Content: fmt.Sprintf("Resultado corregido de %s:\n%s", decision.Worker, resultado),
			})
		}
	}

	// Fallback
	if r, ok := resultados["revisor"]; ok {
		return r, nil
	}
	if r, ok := resultados["redactor"]; ok {
		return r, nil
	}
	return "Sin resultado.", nil
}

func main() {
	tarea := "Escribe un párrafo explicando qué es el patrón supervisor/worker en sistemas multi-agente."
	fmt.Printf("Tarea: %s\n\n", tarea)

	resultado, err := supervisorWorker(tarea, 3)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Resultado:\n%s\n", resultado)
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
