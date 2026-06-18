// Patrón Debate: N agentes generan respuestas independientes, luego leen las de
// los demás y actualizan las suyas por R rondas. Agregación por mayoría o árbitro.

// Cómo ejecutar: make go FILE=go/12-multi-agente/debate.go

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

var modelAgents = envOr("SMALL_MODEL", "claude-haiku-4-5-20251001")
var modelArbiter = envOr("MODEL", "claude-sonnet-4-6")

type DebateMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type DebateResp struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

func llamarLLMDebate(system string, messages []DebateMsg, model string, temperature float64) (string, error) {
	body, _ := json.Marshal(map[string]interface{}{
		"model":       model,
		"max_tokens":  600,
		"system":      system,
		"messages":    messages,
		"temperature": temperature,
	})
	req, _ := http.NewRequest("POST", envBaseURL(), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", os.Getenv("ANTHROPIC_API_KEY"))
	req.Header.Set("anthropic-version", "2023-06-01")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	var result DebateResp
	json.Unmarshal(respBody, &result)
	if len(result.Content) > 0 {
		return strings.TrimSpace(result.Content[0].Text), nil
	}
	return "", fmt.Errorf("respuesta vacía")
}

func majorityVote(respuestas []string) string {
	conteo := make(map[string]int)
	for _, r := range respuestas {
		key := strings.ToLower(strings.TrimSpace(r))
		conteo[key]++
	}
	maxCount := 0
	modal := ""
	for k, v := range conteo {
		if v > maxCount {
			maxCount = v
			modal = k
		}
	}
	for _, r := range respuestas {
		if strings.ToLower(strings.TrimSpace(r)) == modal {
			return r
		}
	}
	return respuestas[0]
}

func llmArbiter(pregunta string, respuestas []string) (string, error) {
	var partes []string
	for i, r := range respuestas {
		partes = append(partes, fmt.Sprintf("Agente %d:\n%s", i+1, r))
	}
	system := "Eres un árbitro experto. Lee las respuestas de distintos agentes, " +
		"identifica cuál razonamiento es más sólido y sintetiza la mejor respuesta. " +
		"Si hay contradicción, explica cuál es correcta y por qué."
	return llamarLLMDebate(
		system,
		[]DebateMsg{{
			Role: "user",
			Content: fmt.Sprintf(
				"Pregunta original: %s\n\nRespuestas:\n%s\n\nProporciona la respuesta final más precisa.",
				pregunta, strings.Join(partes, "\n\n"),
			),
		}},
		modelArbiter,
		0.0,
	)
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func debate(pregunta string, nAgents int, nRounds int, useArbiter bool) (string, error) {
	agentSystem := "Eres un agente analítico. Responde con precisión y razonamiento claro. " +
		"Cuando veas las respuestas de otros agentes, actualiza la tuya si tienen razón; " +
		"mantén tu posición y justifícala si no la tienen."

	// Ronda 0: respuestas independientes con temperatura alta para diversidad
	respuestas := make([]string, 0, nAgents)
	fmt.Printf("[Ronda 0] Generando %d respuestas independientes...\n", nAgents)
	for i := 0; i < nAgents; i++ {
		r, err := llamarLLMDebate(
			agentSystem,
			[]DebateMsg{{Role: "user", Content: pregunta}},
			modelAgents, 0.7,
		)
		if err != nil {
			return "", err
		}
		respuestas = append(respuestas, r)
		fmt.Printf("  Agente %d: %s...\n", i+1, truncateStr(r, 80))
	}

	// Rondas de debate
	for ronda := 0; ronda < nRounds; ronda++ {
		fmt.Printf("\n[Ronda %d] Actualizando respuestas...\n", ronda+1)
		nuevas := make([]string, 0, nAgents)
		for i := 0; i < nAgents; i++ {
			var otrosParts []string
			for j, r := range respuestas {
				if j != i {
					label := j + 1
					if j >= i {
						label = j + 2
					}
					otrosParts = append(otrosParts, fmt.Sprintf("Agente %d: %s", label, r))
				}
			}
			actualizada, err := llamarLLMDebate(
				agentSystem,
				[]DebateMsg{
					{Role: "user", Content: pregunta},
					{Role: "assistant", Content: respuestas[i]},
					{Role: "user", Content: fmt.Sprintf(
						"Otros agentes respondieron:\n%s\n\n"+
							"Usa sus argumentos para mejorar tu respuesta. "+
							"Si tienen razón, actualiza. Si no, mantén y justifica.",
						strings.Join(otrosParts, "\n\n"),
					)},
				},
				modelAgents, 0.3,
			)
			if err != nil {
				return "", err
			}
			nuevas = append(nuevas, actualizada)
			fmt.Printf("  Agente %d (actualizado): %s...\n", i+1, truncateStr(actualizada, 80))
		}
		respuestas = nuevas
	}

	fmt.Println("\n[Agregación]")
	if useArbiter {
		fmt.Println("  Usando árbitro LLM...")
		return llmArbiter(pregunta, respuestas)
	}
	fmt.Println("  Usando majority_vote...")
	return majorityVote(respuestas), nil
}

func main() {
	pregunta := "Un tren parte de la ciudad A a 60 km/h. Otro parte simultáneamente " +
		"de la ciudad B a 90 km/h en dirección contraria. Las ciudades están a 300 km. " +
		"¿En cuántos minutos se cruzan los trenes?"
	fmt.Printf("Pregunta: %s\n\n", pregunta)

	resultado, err := debate(pregunta, 3, 2, false)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
	fmt.Printf("\nRespuesta final (majority_vote):\n%s\n", resultado)

	fmt.Printf("\n%s\n\n", strings.Repeat("=", 60))

	resultadoArbitro, err := debate(pregunta, 3, 2, true)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
	fmt.Printf("\nRespuesta final (árbitro LLM):\n%s\n", resultadoArbitro)
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
