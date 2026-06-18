// Replanificación dinámica: detecta divergencia tras cada paso y replantea los restantes.
//
// El evaluador LLM juzga si cada resultado permite continuar al paso siguiente.
// Si no, el replanificador regenera los pasos pendientes sin repetir los ya completados.
// maxReplans=3 previene el loop infinito documentado en AutoGPT.
//
// Requiere: ANTHROPIC_API_KEY en el entorno.

// Cómo ejecutar: make go FILE=go/09-planificacion/replanificacion.go

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
	maxReplans = 3
)

var (
	model = envOr("MODEL", "claude-sonnet-4-6")
	apiURL = envBaseURL()
)

// --- API types ---

type apiMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type apiReq struct {
	Model     string   `json:"model"`
	MaxTokens int      `json:"max_tokens"`
	Messages  []apiMsg `json:"messages"`
}

type apiBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type apiResp struct {
	Content []apiBlock `json:"content"`
}

func llmCall(prompt string, maxTokens int) (string, error) {
	body, _ := json.Marshal(apiReq{
		Model:     model,
		MaxTokens: maxTokens,
		Messages:  []apiMsg{{Role: "user", Content: prompt}},
	})

	req, _ := http.NewRequest("POST", apiURL, bytes.NewReader(body))
	req.Header.Set("x-api-key", os.Getenv("ANTHROPIC_API_KEY"))
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("content-type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	var r apiResp
	if err := json.Unmarshal(raw, &r); err != nil || len(r.Content) == 0 {
		return "", fmt.Errorf("respuesta inválida: %s", raw)
	}
	return strings.TrimSpace(r.Content[0].Text), nil
}

// --- Plan types ---

type entrada struct {
	paso      string
	resultado string
	estado    string // "OK" | "PARCIAL"
}

var listRe = regexp.MustCompile(`(?m)^\d+[.)]\s+(.+)$`)

func parsearLista(texto string) []string {
	matches := listRe.FindAllStringSubmatch(texto, -1)
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		if s := strings.TrimSpace(m[1]); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// --- Core steps ---

func ejecutarPaso(paso string, historial []entrada) (string, error) {
	var lines []string
	for _, e := range historial {
		lines = append(lines, fmt.Sprintf("[%s] → %s", clip(e.paso, 40), clip(e.resultado, 80)))
	}
	contexto := "(sin pasos previos)"
	if len(lines) > 0 {
		contexto = strings.Join(lines, "\n")
	}

	prompt := fmt.Sprintf(
		"Contexto de pasos ya completados:\n%s\n\n"+
			"Ejecuta este paso y devuelve el resultado como texto conciso (máx 100 palabras):\n%s",
		contexto, paso,
	)
	return llmCall(prompt, 200)
}

func evaluarDivergencia(paso, resultado, proximo string) (bool, string, error) {
	if proximo == "" {
		return true, "", nil
	}
	prompt := fmt.Sprintf(
		"Paso ejecutado: %s\nResultado obtenido: %s\nPróximo paso del plan: %s\n\n"+
			"¿El resultado permite ejecutar el próximo paso?\n"+
			"Responde SOLO con una de estas palabras: SATISFACE | NO_SATISFACE\n"+
			"Si NO_SATISFACE, añade en la misma línea: | <razón breve>",
		paso, resultado, proximo,
	)
	resp, err := llmCall(prompt, 60)
	if err != nil {
		return false, "", err
	}
	satisface := strings.HasPrefix(strings.ToUpper(resp), "SATISFACE")
	razon := ""
	if idx := strings.Index(resp, "|"); idx >= 0 {
		razon = strings.TrimSpace(resp[idx+1:])
	}
	return satisface, razon, nil
}

func replanificar(
	tarea string, historial []entrada,
	pasoFallido, resultadoFallido, razon string,
) ([]string, error) {
	var lines []string
	for _, e := range historial {
		lines = append(lines, "- "+e.paso)
	}
	completados := "(ninguno)"
	if len(lines) > 0 {
		completados = strings.Join(lines, "\n")
	}

	prompt := fmt.Sprintf(
		"Tarea original: %s\n\nPasos ya completados exitosamente:\n%s\n\n"+
			"Paso que falló: %s\nResultado fallido: %s\nRazón del fallo: %s\n\n"+
			"Genera una lista numerada con los pasos RESTANTES para completar la tarea.\n"+
			"No repitas los pasos ya completados. Responde solo con la lista numerada.",
		tarea, completados, pasoFallido, resultadoFallido, razon,
	)
	resp, err := llmCall(prompt, 400)
	if err != nil {
		return nil, err
	}
	return parsearLista(resp), nil
}

func planExecuteDynamic(tarea string) (string, error) {
	// 1. Generar plan inicial
	planResp, err := llmCall(
		fmt.Sprintf("Genera una lista numerada de pasos atómicos para completar esta tarea.\n"+
			"Cada paso debe comenzar con un verbo y ser ejecutable de forma independiente.\n"+
			"Tarea: %s\nResponde solo con la lista numerada.", tarea),
		400,
	)
	if err != nil {
		return "", fmt.Errorf("planificador: %w", err)
	}
	plan := parsearLista(planResp)

	fmt.Printf("Plan inicial (%d pasos):\n", len(plan))
	for j, p := range plan {
		fmt.Printf("  %d. %s\n", j+1, clip(p, 70))
	}

	var historial []entrada
	replans := 0
	i := 0

	for i < len(plan) {
		paso := plan[i]
		proximo := ""
		if i+1 < len(plan) {
			proximo = plan[i+1]
		}

		resultado, err := ejecutarPaso(paso, historial)
		if err != nil {
			return "", fmt.Errorf("executor paso %d: %w", i+1, err)
		}

		satisface, razon, err := evaluarDivergencia(paso, resultado, proximo)
		if err != nil {
			return "", fmt.Errorf("evaluador paso %d: %w", i+1, err)
		}

		mark := "✓"
		if !satisface {
			mark = "✗"
		}
		fmt.Printf("\n[paso %d/%d] %s %s\n", i+1, len(plan), mark, clip(paso, 60))

		switch {
		case satisface:
			historial = append(historial, entrada{paso, resultado, "OK"})
			i++

		case replans < maxReplans:
			r := razon
			if r == "" {
				r = "(sin razón explícita)"
			}
			fmt.Printf("  → Divergencia: %s\n", r)

			nuevosPasos, err := replanificar(tarea, historial, paso, resultado, razon)
			if err != nil {
				return "", fmt.Errorf("replanificador: %w", err)
			}

			completados := make([]string, len(historial))
			for k, e := range historial {
				completados[k] = e.paso
			}
			plan = append(completados, nuevosPasos...)
			replans++
			fmt.Printf("  → Replan #%d: %d pasos nuevos desde paso %d\n", replans, len(nuevosPasos), i+1)

		default:
			historial = append(historial, entrada{paso, resultado, "PARCIAL"})
			i++
		}
	}

	// 2. Síntesis final
	var hLines []string
	for _, e := range historial {
		hLines = append(hLines, fmt.Sprintf("[%s] %s: %s", e.estado, e.paso, clip(e.resultado, 100)))
	}
	return llmCall(
		fmt.Sprintf("Tarea original: %s\n\nHistorial de ejecución:\n%s\n\n"+
			"Genera la respuesta final integrando todos los resultados.",
			tarea, strings.Join(hLines, "\n")),
		500,
	)
}

func main() {
	tarea := "Calcula cuántos días hay entre el 1 de enero y el 1 de julio de 2025, " +
		"luego calcula cuántas semanas y cuántos meses aproximados representa."

	fmt.Printf("Tarea: %s\n\n", tarea)

	resultado, err := planExecuteDynamic(tarea)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("\n=== Resultado final ===\n%s\n", resultado)
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
