// Patrón Supervisor/Worker: despacho paralelo con workers especializados.
//
// El supervisor genera un plan de subtareas independientes; cada worker
// recibe su propia tarea con contexto aislado y system prompt especializado.
// Todos corren en paralelo con goroutines — sin DAG.
//
// Requiere: ANTHROPIC_API_KEY en el entorno.

// Cómo ejecutar: make go FILE=go/09-planificacion/supervisor_worker.go

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

var (
	modelSupervisor = envOr("MODEL", "claude-sonnet-4-6")
	modelWorker = envOr("SMALL_MODEL", "claude-haiku-4-5-20251001")
	apiEndpoint = envBaseURL()
)

var workerSystems = map[string]string{
	"analista":     "Eres un analista técnico. Responde con datos concretos y estructura clara.",
	"investigador": "Eres un investigador especializado. Cita benchmarks y referencias cuando existan.",
	"arquitecto":   "Eres un arquitecto de software. Enfócate en decisiones de diseño y tradeoffs reales.",
	"critico":      "Eres un crítico técnico. Señala limitaciones, casos borde y riesgos concretos.",
}

const defaultSystem = "Eres un asistente técnico especializado. Responde de forma concisa y estructurada."

// --- API types ---

type apiMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type apiReq struct {
	Model     string   `json:"model"`
	MaxTokens int      `json:"max_tokens"`
	System    string   `json:"system,omitempty"`
	Messages  []apiMsg `json:"messages"`
}

type apiBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type apiResp struct {
	Content []apiBlock `json:"content"`
}

func llmCall(model string, maxTokens int, system, prompt string) (string, error) {
	body, _ := json.Marshal(apiReq{
		Model:     model,
		MaxTokens: maxTokens,
		System:    system,
		Messages:  []apiMsg{{Role: "user", Content: prompt}},
	})

	req, _ := http.NewRequest("POST", apiEndpoint, bytes.NewReader(body))
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
	return r.Content[0].Text, nil
}

// --- Plan types ---

type Subtarea struct {
	ID          string `json:"id"`
	Descripcion string `json:"descripcion"`
	TipoWorker  string `json:"tipo_worker"`
}

var jsonArrayRe = regexp.MustCompile(`(?s)\[.*\]`)

func parsearPlan(texto string) ([]Subtarea, error) {
	m := jsonArrayRe.FindString(texto)
	if m == "" {
		n := len(texto)
		if n > 300 {
			n = 300
		}
		return nil, fmt.Errorf("no se encontró array JSON en: %s", texto[:n])
	}
	var plan []Subtarea
	if err := json.Unmarshal([]byte(m), &plan); err != nil {
		return nil, fmt.Errorf("JSON inválido: %w", err)
	}
	return plan, nil
}

// --- Worker execution ---

type workerResult struct {
	id     string
	result string
}

func ejecutarWorker(s Subtarea, results chan<- workerResult, errs chan<- error, wg *sync.WaitGroup) {
	defer wg.Done()

	system, ok := workerSystems[s.TipoWorker]
	if !ok {
		system = defaultSystem
	}

	r, err := llmCall(modelWorker, 300, system, s.Descripcion)
	if err != nil {
		errs <- fmt.Errorf("%s: %w", s.ID, err)
		return
	}
	results <- workerResult{s.ID, r}
}

func supervisorWorker(tarea string) (string, error) {
	// 1. Supervisor genera plan
	promptPlan := fmt.Sprintf(
		"Descompón la siguiente tarea en subtareas independientes que puedan ejecutarse en paralelo.\n"+
			"Responde ÚNICAMENTE con un array JSON válido, sin texto adicional.\n"+
			"Cada elemento debe tener:\n"+
			"  \"id\": string único (W1, W2, ...),\n"+
			"  \"descripcion\": objetivo concreto para el worker,\n"+
			"  \"tipo_worker\": uno de [\"analista\", \"investigador\", \"arquitecto\", \"critico\"]\n\n"+
			"Tarea: %s", tarea,
	)

	planTexto, err := llmCall(modelSupervisor, 600, "", promptPlan)
	if err != nil {
		return "", fmt.Errorf("planificador: %w", err)
	}

	plan, err := parsearPlan(planTexto)
	if err != nil {
		return "", fmt.Errorf("parseo: %w", err)
	}

	fmt.Printf("Plan (%d workers):\n", len(plan))
	for _, s := range plan {
		desc := s.Descripcion
		if len(desc) > 65 {
			desc = desc[:65]
		}
		fmt.Printf("  %s [%s]: %s\n", s.ID, s.TipoWorker, desc)
	}

	// 2. Todos los workers en paralelo — goroutine por worker
	fmt.Println("\nDispatcheando workers en paralelo...")
	results := make(chan workerResult, len(plan))
	errs := make(chan error, len(plan))
	var wg sync.WaitGroup

	for _, s := range plan {
		wg.Add(1)
		go ejecutarWorker(s, results, errs, &wg)
	}
	wg.Wait()
	close(results)
	close(errs)

	if err := <-errs; err != nil {
		return "", err
	}

	resultados := make(map[string]string, len(plan))
	for r := range results {
		resultados[r.id] = r.result
		preview := r.result
		if len(preview) > 60 {
			preview = preview[:60]
		}
		fmt.Printf("  %s ✓ %s...\n", r.id, preview)
	}

	// 3. Supervisor consolida
	var lines []string
	for wid, r := range resultados {
		lines = append(lines, fmt.Sprintf("[%s] %s", wid, r))
	}
	promptSintesis := fmt.Sprintf(
		"Tarea original: %s\n\nResultados de los workers:\n%s\n\n"+
			"Sintetiza una respuesta final integrando todos los resultados. Sé conciso y directo.",
		tarea, strings.Join(lines, "\n"),
	)

	return llmCall(modelSupervisor, 500, "", promptSintesis)
}

func main() {
	tarea := "Evalúa si Python o TypeScript es mejor para construir agentes IA en 2025: " +
		"considera ecosistema de librerías, rendimiento async, tipado y facilidad de debugging."

	fmt.Printf("Tarea: %s\n\n", tarea)

	resultado, err := supervisorWorker(tarea)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("\n=== Síntesis final ===\n%s\n", resultado)
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
