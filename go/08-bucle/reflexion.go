// Reflexion — Shinn et al. 2023 (arXiv:2303.11366).
//
// ReflexionAgent: loop actor → evaluador → reflector hasta maxIntentos.
// Tres evaluadores: UnitTestEvaluator (determinista), HeuristicEvaluator
// (sin modelo), LLMJudgeEvaluator (LLM-as-judge).
// slidingWindowMemory: mantiene solo las últimas N reflexiones en el contexto.
//
// Requiere: ANTHROPIC_API_KEY en el entorno.

// Cómo ejecutar: make go FILE=go/08-bucle/reflexion.go

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
	maxIntentos     = 3
	maxReflexiones  = 3
)

var (
	reflexionModel = envOr("MODEL", "claude-sonnet-4-6")
)

// --- Trayectoria ---

type Trayectoria struct {
	Pasos          []string
	ResultadoFinal string
}

func (t *Trayectoria) Log(paso string) {
	t.Pasos = append(t.Pasos, paso)
}

func (t *Trayectoria) ToText() string {
	return strings.Join(t.Pasos, "\n")
}

// --- API types ---

type refMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type refRequest struct {
	Model     string   `json:"model"`
	MaxTokens int      `json:"max_tokens"`
	Messages  []refMsg `json:"messages"`
}

type refContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type refResponse struct {
	Content []refContentBlock `json:"content"`
}

func refCall(apiKey string, req refRequest) (string, error) {
	body, _ := json.Marshal(req)
	r, _ := http.NewRequest("POST", envBaseURL(), bytes.NewReader(body))
	r.Header.Set("x-api-key", apiKey)
	r.Header.Set("anthropic-version", "2023-06-01")
	r.Header.Set("content-type", "application/json")

	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	var out refResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("parse: %w\n%s", err, raw)
	}
	if len(out.Content) == 0 {
		return "", fmt.Errorf("empty response")
	}
	return out.Content[0].Text, nil
}

// --- Evaluator interface ---

type Evaluator interface {
	Evaluar(tray *Trayectoria, tarea string) (bool, string)
}

// UnitTestEvaluator: test determinista. Más confiable cuando el criterio es computable.
type UnitTestEvaluator struct {
	TestFn func(resultado string) bool
}

func (e *UnitTestEvaluator) Evaluar(tray *Trayectoria, _ string) (bool, string) {
	ok := e.TestFn(tray.ResultadoFinal)
	if ok {
		return true, "Test superado."
	}
	return false, "El resultado no cumple el criterio del test."
}

// HeuristicEvaluator: heurísticas sin modelo. Señal ruidosa pero instantánea.
type HeuristicEvaluator struct{}

func (e *HeuristicEvaluator) Evaluar(tray *Trayectoria, _ string) (bool, string) {
	if strings.TrimSpace(tray.ResultadoFinal) == "" {
		return false, "El agente no produjo respuesta."
	}
	n := len(tray.Pasos)
	if n >= 2 && tray.Pasos[n-1] == tray.Pasos[n-2] {
		return false, "El agente repitió el mismo paso dos veces consecutivas."
	}
	return true, "Heurísticas superadas."
}

// LLMJudgeEvaluator: LLM-as-judge. Introduce bias del juez.
type LLMJudgeEvaluator struct {
	APIKey string
	Model  string
}

func (e *LLMJudgeEvaluator) Evaluar(tray *Trayectoria, tarea string) (bool, string) {
	model := e.Model
	if model == "" {
		model = reflexionModel
	}
	prompt := fmt.Sprintf(
		"Tarea: %s\nRespuesta: %s\n\n"+
			"¿La respuesta completa la tarea? Responde: ÉXITO o FALLO y una frase de feedback.",
		tarea, tray.ResultadoFinal,
	)
	text, err := refCall(e.APIKey, refRequest{
		Model:     model,
		MaxTokens: 150,
		Messages:  []refMsg{{Role: "user", Content: prompt}},
	})
	if err != nil {
		return false, fmt.Sprintf("error del juez: %v", err)
	}
	return strings.Contains(strings.ToUpper(text), "ÉXITO"), text
}

// --- Reflector ---

type ReflectorAgent struct {
	APIKey string
	Model  string
}

func (r *ReflectorAgent) Reflexionar(tarea string, intento int, tray *Trayectoria, feedback string) string {
	model := r.Model
	if model == "" {
		model = reflexionModel
	}
	trayText := tray.ToText()
	if len(trayText) > 1500 {
		trayText = trayText[:1500]
	}
	prompt := fmt.Sprintf(
		"Tarea: %s\nIntento #%d — resultado: FALLIDO\nTrayectoria:\n%s\nFeedback del evaluador: %s\n\n"+
			"Reflexiona sobre qué salió mal y qué harías diferente.\nSé específico (máximo 80 palabras).",
		tarea, intento, trayText, feedback,
	)
	text, _ := refCall(r.APIKey, refRequest{
		Model:     model,
		MaxTokens: 200,
		Messages:  []refMsg{{Role: "user", Content: prompt}},
	})
	return strings.TrimSpace(text)
}

// --- Sliding window memory ---

func slidingWindowMemory(reflexiones []string, maxN int) []string {
	if len(reflexiones) <= maxN {
		return reflexiones
	}
	return reflexiones[len(reflexiones)-maxN:]
}

// --- Reflexion agent ---

type ReflexionAgent struct {
	APIKey      string
	Evaluator   Evaluator
	Reflector   *ReflectorAgent
	ActorModel  string
	MaxIntentos int
}

func (a *ReflexionAgent) ejecutarActor(tarea string, reflexiones []string) *Trayectoria {
	var bloque string
	if len(reflexiones) > 0 {
		var items []string
		for _, r := range reflexiones {
			items = append(items, "- "+r)
		}
		bloque = "Reflexiones de intentos previos:\n" + strings.Join(items, "\n") + "\n\n"
	}

	prompt := bloque + "Completa esta tarea:\n\n" + tarea
	model := a.ActorModel
	if model == "" {
		model = reflexionModel
	}
	text, _ := refCall(a.APIKey, refRequest{
		Model:     model,
		MaxTokens: 500,
		Messages:  []refMsg{{Role: "user", Content: prompt}},
	})

	tray := &Trayectoria{}
	tray.Log(fmt.Sprintf("Actor ejecutado con %d reflexiones previas.", len(reflexiones)))
	tray.ResultadoFinal = strings.TrimSpace(text)
	return tray
}

func (a *ReflexionAgent) Run(tarea string) string {
	var memoria []string

	for intento := 1; intento <= a.MaxIntentos; intento++ {
		tray := a.ejecutarActor(tarea, slidingWindowMemory(memoria, maxReflexiones))
		exito, feedback := a.Evaluator.Evaluar(tray, tarea)

		mark := "✓"
		if !exito {
			mark = "✗"
		}
		fb := feedback
		if len(fb) > 70 {
			fb = fb[:70]
		}
		fmt.Printf("[intento %d/%d] %s %s\n", intento, a.MaxIntentos, mark, fb)

		if exito {
			return tray.ResultadoFinal
		}

		if intento < a.MaxIntentos && a.Reflector != nil {
			reflexion := a.Reflector.Reflexionar(tarea, intento, tray, feedback)
			memoria = append(memoria, reflexion)
			trunc := reflexion
			if len(trunc) > 90 {
				trunc = trunc[:90]
			}
			fmt.Printf("  Reflexión: %s\n", trunc)
		}
	}

	// Último intento guardado en tray — devolver mejor esfuerzo
	lastTray := a.ejecutarActor(tarea, slidingWindowMemory(memoria, maxReflexiones))
	return lastTray.ResultadoFinal
}

func main() {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "ANTHROPIC_API_KEY no está configurada")
		os.Exit(1)
	}

	// Evaluador determinista: la respuesta debe contener un número entre 10 y 50
	numRe := regexp.MustCompile(`\b(\d+)\b`)
	evaluator := &UnitTestEvaluator{
		TestFn: func(resultado string) bool {
			matches := numRe.FindAllString(resultado, -1)
			for _, m := range matches {
				var n int
				fmt.Sscanf(m, "%d", &n)
				if n >= 10 && n <= 50 {
					return true
				}
			}
			return false
		},
	}

	agent := &ReflexionAgent{
		APIKey:    apiKey,
		Evaluator: evaluator,
		Reflector: &ReflectorAgent{APIKey: apiKey},
		MaxIntentos: maxIntentos,
	}

	resultado := agent.Run(
		"Escribe exactamente un número entero entre 10 y 50, " +
			"seguido de por qué elegiste ese número.",
	)
	if len(resultado) > 200 {
		resultado = resultado[:200]
	}
	fmt.Printf("\nResultado final: %s\n", resultado)
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
