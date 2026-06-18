// ReAct (Reason + Act) — Yao et al. 2022 (arXiv:2210.03629).
//
// Implementación text-based fiel al paper: el modelo genera Thought + Action
// en texto libre; el ejecutor parsea la acción, llama la herramienta e
// inyecta la Observation. stop_sequences corta antes de "Observation:" —
// el ejecutor inyecta esa parte y continúa el loop.
//
// Requiere: ANTHROPIC_API_KEY en el entorno.

// Cómo ejecutar: make go FILE=go/08-bucle/react_agent.go

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
)

const (
	maxIterations  = 10
)

var (
	modelReAct = envOr("MODEL", "claude-sonnet-4-6")
	anthropicAPI = envBaseURL()
)

var fewShot = `Thought: Necesito buscar la capital de Australia.
Action: Search[capital Australia]
Observation: La capital de Australia es Canberra.
Thought: Tengo la respuesta.
Action: Finish[Canberra]

---

Thought: Necesito saber quién fue el padre de Zeus.
Action: Search[padre Zeus mitología]
Observation: Crono es el padre de Zeus. Era un Titán que gobernó el cosmos.
Thought: La respuesta es Crono.
Action: Finish[Crono]

---

`

const systemReAct = "Responde siguiendo el formato Thought/Action/Observation del ejemplo. " +
	"Las acciones disponibles son: Search[query] y Finish[respuesta]."

var actionRe = regexp.MustCompile(`Action:\s*(\w+)\[(.+?)\]`)

// --- API types ---

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type request struct {
	Model         string    `json:"model"`
	MaxTokens     int       `json:"max_tokens"`
	System        string    `json:"system"`
	Messages      []message `json:"messages"`
	StopSequences []string  `json:"stop_sequences"`
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type response struct {
	Content    []contentBlock `json:"content"`
	StopReason string         `json:"stop_reason"`
}

// --- ReAct agent ---

type ToolFn func(args string) string

type ReActAgent struct {
	Tools         map[string]ToolFn
	Model         string
	MaxIterations int
	apiKey        string
}

func NewReActAgent(tools map[string]ToolFn) *ReActAgent {
	return &ReActAgent{
		Tools:         tools,
		Model:         modelReAct,
		MaxIterations: maxIterations,
		apiKey:        os.Getenv("ANTHROPIC_API_KEY"),
	}
}

func (a *ReActAgent) call(prompt string) (string, error) {
	body, _ := json.Marshal(request{
		Model:         a.Model,
		MaxTokens:     300,
		System:        systemReAct,
		Messages:      []message{{Role: "user", Content: prompt}},
		StopSequences: []string{"Observation:"},
	})

	req, _ := http.NewRequest("POST", anthropicAPI, bytes.NewReader(body))
	req.Header.Set("x-api-key", a.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("content-type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	var r response
	if err := json.Unmarshal(raw, &r); err != nil {
		return "", fmt.Errorf("parse error: %w\nbody: %s", err, raw)
	}
	if len(r.Content) == 0 {
		return "", fmt.Errorf("empty response: %s", raw)
	}
	return r.Content[0].Text, nil
}

// parseAction extrae (toolName, toolArgs) del formato Action: Name[args].
func parseAction(text string) (string, string, bool) {
	m := actionRe.FindStringSubmatch(text)
	if m == nil {
		return "", "", false
	}
	return m[1], strings.TrimSpace(m[2]), true
}

func (a *ReActAgent) Run(task string) (string, error) {
	prompt := fewShot + "Task: " + task + "\n"

	for i := range a.MaxIterations {
		generated, err := a.call(prompt)
		if err != nil {
			return "", fmt.Errorf("iter %d: %w", i+1, err)
		}
		prompt += generated

		fmt.Printf("[iter %d] %s\n", i+1, truncate(strings.TrimSpace(generated), 100))

		toolName, toolArgs, ok := parseAction(generated)
		if !ok {
			break
		}

		if toolName == "Finish" {
			return toolArgs, nil
		}

		fn, exists := a.Tools[toolName]
		var observation string
		if exists {
			observation = fn(toolArgs)
		} else {
			observation = fmt.Sprintf("[Error: herramienta '%s' no encontrada]", toolName)
		}

		prompt += "Observation: " + observation + "\n"
		fmt.Printf("  Observation: %s\n", truncate(observation, 80))
	}

	return "[MAX_ITERATIONS sin respuesta]", nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// --- Demo ---

var kb = map[string]string{
	"capital españa":    "Madrid es la capital de España.",
	"capital francia":   "París es la capital de Francia.",
	"capital alemania":  "Berlín es la capital de Alemania.",
	"padre zeus":        "Crono es el padre de Zeus en la mitología griega.",
	"capital australia": "La capital de Australia es Canberra.",
}

func search(query string) string {
	q := strings.ToLower(query)
	for key, val := range kb {
		words := strings.Split(key, " ")
		allFound := true
		for _, w := range words {
			if !strings.Contains(q, w) {
				allFound = false
				break
			}
		}
		if allFound {
			return val
		}
	}
	return "No encontré información sobre esa consulta."
}

func main() {
	agent := NewReActAgent(map[string]ToolFn{
		"Search": search,
	})

	result, err := agent.Run("¿Cuáles son las capitales de España y Francia?")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("\nRespuesta final: %s\n", result)
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
