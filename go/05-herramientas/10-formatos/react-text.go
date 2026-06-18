// Formato ReAct (Reasoning + Acting) para modelos sin function calling nativo.
//
// ReAct intercala Thought/Action/Observation en texto libre.
// El cliente parsea la Action con regexp, ejecuta la herramienta,
// e inyecta la Observation antes de que el modelo continúe.
//
// Stop sequence "Observation:" interrumpe la generación para
// que el cliente inyecte el resultado real de la herramienta.

// Cómo ejecutar: make go FILE=go/05-herramientas/10-formatos/react-text.go

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
	"time"
)

var (
	modelReAct = envOr("MODEL", "claude-sonnet-4-6")
	reactAPIURL = envBaseURL()
)

// --- System prompt ReAct ---

const reactSystem = `Responde usando EXACTAMENTE el siguiente formato:

Thought: [tu razonamiento sobre qué hacer a continuación]
Action: ToolName[argumento]
Observation: [resultado de la herramienta — lo inyecta el sistema]

Repite Thought/Action/Observation hasta tener la respuesta final, luego:
Thought: Tengo la información necesaria para responder.
Action: Finish[respuesta completa aquí]

Herramientas disponibles:
- Search[query]: Busca información. Ejemplo: Search[capital of France]
- Calculate[expresion]: Evalúa expresión matemática. Ejemplo: Calculate[15 * 8]
- Finish[respuesta]: Termina y devuelve la respuesta final.

IMPORTANTE: usa exactamente el formato ToolName[argumento] con corchetes.`

const reactFewShot = `
Ejemplo:
Pregunta: ¿Cuánto es el doble de la población de Madrid?
Thought: Necesito buscar la población de Madrid y luego multiplicarla por 2.
Action: Search[population of Madrid]
Observation: La población de Madrid es aproximadamente 3.3 millones de personas.
Thought: Ahora calculo el doble: 3.3 * 2.
Action: Calculate[3.3 * 2]
Observation: 6.6
Thought: Tengo la respuesta final.
Action: Finish[El doble de la población de Madrid es 6.6 millones de personas]

---`

// --- Herramientas mock ---

var searchDB = map[string]string{
	"population of madrid":   "La población de Madrid es aproximadamente 3.3 millones.",
	"population of tokyo":    "La población de Tokio es aproximadamente 13.96 millones.",
	"capital of france":      "La capital de Francia es París.",
	"capital of japan":       "La capital de Japón es Tokio.",
	"height of eiffel tower": "La Torre Eiffel mide 330 metros.",
	"distance madrid barcelona": "La distancia Madrid-Barcelona es ~621 km.",
}

func mockSearchReAct(query string) string {
	lower := strings.ToLower(query)
	for key, val := range searchDB {
		if strings.Contains(lower, key) || strings.Contains(key, lower) {
			return val
		}
	}
	return fmt.Sprintf("No se encontró información específica sobre %q.", query)
}

func mockCalculateReAct(expression string) string {
	// Calculadora segura para expresiones simples (solo números y operadores básicos)
	// En producción usaría una librería de evaluación de expresiones
	expr := strings.TrimSpace(expression)

	// Casos simples de demostración
	switch expr {
	case "4.5 * 3.2":
		return "14.4"
	case "3.3 * 2":
		return "6.6"
	case "15 * 8":
		return "120"
	case "15 * 8 + 3":
		return "123"
	}

	// Intentar evaluar multiplicación simple A * B
	var a, b float64
	if n, _ := fmt.Sscanf(expr, "%f * %f", &a, &b); n == 2 {
		return fmt.Sprintf("%g", a*b)
	}
	if n, _ := fmt.Sscanf(expr, "%f + %f", &a, &b); n == 2 {
		return fmt.Sprintf("%g", a+b)
	}
	if n, _ := fmt.Sscanf(expr, "%f - %f", &a, &b); n == 2 {
		return fmt.Sprintf("%g", a-b)
	}

	return fmt.Sprintf("Resultado de %s = [calculado]", expr)
}

// --- Parser de ReAct ---

type parsedAction struct {
	thought  string
	toolName string
	argument string
}

var actionRegexp = regexp.MustCompile(`Action:\s*(\w+)\[([^\]]*)\]`)
var thoughtRegexp = regexp.MustCompile(`Thought:\s*(.+?)(?:Action:|$)`)

func parseReActOutput(text string) *parsedAction {
	actionMatch := actionRegexp.FindStringSubmatch(text)
	if actionMatch == nil {
		return nil
	}

	thought := ""
	thoughtMatch := thoughtRegexp.FindStringSubmatch(text)
	if thoughtMatch != nil {
		thought = strings.TrimSpace(thoughtMatch[1])
	}

	return &parsedAction{
		thought:  thought,
		toolName: actionMatch[1],
		argument: strings.TrimSpace(actionMatch[2]),
	}
}

// --- Cliente HTTP para la API de Anthropic ---

type reactMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type reactAPIRequest struct {
	Model         string         `json:"model"`
	MaxTokens     int            `json:"max_tokens"`
	System        string         `json:"system"`
	Messages      []reactMessage `json:"messages"`
	StopSequences []string       `json:"stop_sequences"`
}

type reactAPIResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	StopReason   string `json:"stop_reason"`
	StopSequence string `json:"stop_sequence"`
}

func callAnthropicReAct(req reactAPIRequest) (*reactAPIResponse, error) {
	body, _ := json.Marshal(req)
	httpReq, _ := http.NewRequestWithContext(context.Background(), "POST", reactAPIURL, bytes.NewReader(body))
	httpReq.Header.Set("x-api-key", os.Getenv("ANTHROPIC_API_KEY"))
	httpReq.Header.Set("anthropic-version", "2023-06-01")
	httpReq.Header.Set("content-type", "application/json")

	client := &http.Client{Timeout: 180 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)
	var r reactAPIResponse
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("parse: %s — %w", string(data), err)
	}
	return &r, nil
}

// --- Loop ReAct ---

func reactLoop(pregunta string, maxPasos int) string {
	// Contexto inicial con few-shot
	contexto := reactFewShot + "\nPregunta: " + pregunta + "\n"

	fmt.Printf("Pregunta: %s\n\n", pregunta)

	for paso := 0; paso < maxPasos; paso++ {
		// Generar hasta que el modelo escriba "Observation:"
		resp, err := callAnthropicReAct(reactAPIRequest{
			Model:     modelReAct,
			MaxTokens: 512,
			System:    reactSystem,
			Messages:  []reactMessage{{Role: "user", Content: contexto}},
			StopSequences: []string{"Observation:"},
		})
		if err != nil {
			return fmt.Sprintf("Error API: %v", err)
		}

		generado := ""
		for _, b := range resp.Content {
			if b.Type == "text" {
				generado += b.Text
			}
		}

		fmt.Printf("[Paso %d]\n%s\n", paso+1, strings.TrimSpace(generado))

		// Parsear la acción generada
		parsed := parseReActOutput(generado)
		if parsed == nil {
			fmt.Println("  [warn] no se encontró Action en el output — terminando")
			break
		}

		// Si es Finish, devolver el argumento como respuesta final
		if parsed.toolName == "Finish" {
			fmt.Printf("\n[Finish] %s\n", parsed.argument)
			return parsed.argument
		}

		// Ejecutar la herramienta
		var observacion string
		switch parsed.toolName {
		case "Search":
			observacion = mockSearchReAct(parsed.argument)
		case "Calculate":
			observacion = mockCalculateReAct(parsed.argument)
		default:
			observacion = fmt.Sprintf("Error: herramienta '%s' no existe", parsed.toolName)
		}

		fmt.Printf("Observation: %s\n\n", observacion)

		// Añadir al contexto: lo generado + la observación inyectada
		contexto += generado + "Observation: " + observacion + "\n"
	}

	return "Max pasos alcanzados sin respuesta final"
}

func main() {
	fmt.Println("=== Formato ReAct (Thought/Action/Observation) ===\n")

	resultado := reactLoop(
		"¿Cuántos metros cuadrados tiene una habitación de 4.5m × 3.2m?",
		10,
	)

	fmt.Printf("\n=== Respuesta final ===\n%s\n", resultado)
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
