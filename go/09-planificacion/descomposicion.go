// Descomposición de tareas con DAG explícito y ejecución paralela.
//
// El planificador LLM genera subtareas con dependencias;
// el executor las resuelve en oleadas paralelas con goroutines + WaitGroup.
//
// Requiere: ANTHROPIC_API_KEY en el entorno.

// Cómo ejecutar: make go FILE=go/09-planificacion/descomposicion.go

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
	modelPlanner = envOr("MODEL", "claude-sonnet-4-6")
	modelExecutor = envOr("SMALL_MODEL", "claude-haiku-4-5-20251001")
	apiURL = envBaseURL()
)

// --- API types ---

type apiMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type apiRequest struct {
	Model     string       `json:"model"`
	MaxTokens int          `json:"max_tokens"`
	Messages  []apiMessage `json:"messages"`
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type apiResponse struct {
	Content []contentBlock `json:"content"`
}

func llmCall(model string, maxTokens int, prompt string) (string, error) {
	body, _ := json.Marshal(apiRequest{
		Model:     model,
		MaxTokens: maxTokens,
		Messages:  []apiMessage{{Role: "user", Content: prompt}},
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
	var r apiResponse
	if err := json.Unmarshal(raw, &r); err != nil || len(r.Content) == 0 {
		return "", fmt.Errorf("respuesta inválida: %s", raw)
	}
	return r.Content[0].Text, nil
}

// --- Plan types ---

type Subtarea struct {
	ID       string   `json:"id"`
	Objetivo string   `json:"objetivo"`
	Deps     []string `json:"deps"`
}

var jsonArrayRe = regexp.MustCompile(`(?s)\[.*\]`)

func parsearPlan(texto string) ([]Subtarea, error) {
	m := jsonArrayRe.FindString(texto)
	if m == "" {
		return nil, fmt.Errorf("no se encontró array JSON en: %s", texto[:minInt(300, len(texto))])
	}
	var plan []Subtarea
	if err := json.Unmarshal([]byte(m), &plan); err != nil {
		return nil, fmt.Errorf("JSON inválido: %w", err)
	}
	return plan, nil
}

func validarPlan(plan []Subtarea) error {
	ids := make(map[string]bool, len(plan))
	for _, s := range plan {
		ids[s.ID] = true
	}
	for _, s := range plan {
		for _, dep := range s.Deps {
			if !ids[dep] {
				return fmt.Errorf("subtarea %s depende de '%s' que no existe", s.ID, dep)
			}
		}
	}
	return nil
}

// --- DAG executor ---

type resultEntry struct {
	id     string
	result string
}

func ejecutarSubtarea(s Subtarea, resultados map[string]string) (string, error) {
	var contexto string
	if len(resultados) > 0 {
		var lines []string
		for k, v := range resultados {
			lines = append(lines, fmt.Sprintf("[%s] %s", k, v))
		}
		contexto = strings.Join(lines, "\n")
	} else {
		contexto = "(ninguno)"
	}

	prompt := fmt.Sprintf(
		"Contexto de subtareas ya completadas:\n%s\n\n"+
			"Ejecuta esta subtarea y devuelve el resultado como texto conciso (máx 150 palabras):\n%s",
		contexto, s.Objetivo,
	)
	return llmCall(modelExecutor, 300, prompt)
}

func ejecutarDag(plan []Subtarea) (map[string]string, error) {
	resultados := make(map[string]string)
	completadas := make(map[string]bool)
	var mu sync.Mutex // protege resultados y completadas

	pendientes := make([]Subtarea, len(plan))
	copy(pendientes, plan)

	for len(pendientes) > 0 {
		// Subtareas con todas las deps satisfechas
		var ejecutables []Subtarea
		for _, s := range pendientes {
			listo := true
			for _, dep := range s.Deps {
				if !completadas[dep] {
					listo = false
					break
				}
			}
			if listo {
				ejecutables = append(ejecutables, s)
			}
		}

		if len(ejecutables) == 0 {
			var ids []string
			for _, s := range pendientes {
				ids = append(ids, s.ID)
			}
			return nil, fmt.Errorf("plan bloqueado — sin ejecutables: %v", ids)
		}

		ids := make([]string, len(ejecutables))
		for i, s := range ejecutables {
			ids[i] = s.ID
		}
		fmt.Printf("  [oleada] paralelo: %v\n", ids)

		// Ejecutar oleada en paralelo con goroutines
		var wg sync.WaitGroup
		results := make(chan resultEntry, len(ejecutables))
		errs := make(chan error, len(ejecutables))

		// Snapshot de resultados para que todas las goroutines vean el mismo estado
		mu.Lock()
		snapResultados := make(map[string]string, len(resultados))
		for k, v := range resultados {
			snapResultados[k] = v
		}
		mu.Unlock()

		for _, s := range ejecutables {
			wg.Add(1)
			go func(sub Subtarea) {
				defer wg.Done()
				r, err := ejecutarSubtarea(sub, snapResultados)
				if err != nil {
					errs <- fmt.Errorf("%s: %w", sub.ID, err)
					return
				}
				results <- resultEntry{sub.ID, r}
			}(s)
		}

		wg.Wait()
		close(results)
		close(errs)

		if err := <-errs; err != nil {
			return nil, err
		}

		mu.Lock()
		for entry := range results {
			resultados[entry.id] = entry.result
			completadas[entry.id] = true
			preview := entry.result
			if len(preview) > 60 {
				preview = preview[:60]
			}
			fmt.Printf("    %s ✓ %s...\n", entry.id, preview)
		}
		mu.Unlock()

		// Filtrar completadas de pendientes
		var resto []Subtarea
		for _, s := range pendientes {
			if !completadas[s.ID] {
				resto = append(resto, s)
			}
		}
		pendientes = resto
	}

	return resultados, nil
}

func descomponerYEjecutar(tarea string) (string, error) {
	// 1. Planificar
	promptPlanificador := fmt.Sprintf(
		"Descompón la siguiente tarea en subtareas atómicas.\n"+
			"Responde ÚNICAMENTE con un array JSON válido, sin texto adicional.\n"+
			"Cada elemento debe tener:\n"+
			"  \"id\": string único (S1, S2, ...),\n"+
			"  \"objetivo\": string de una oración con el objetivo de la subtarea,\n"+
			"  \"deps\": array de IDs que deben completarse primero ([] si ninguna)\n\n"+
			"Regla: maximiza las subtareas con deps=[] (ejecutables en paralelo desde el inicio).\n"+
			"Tarea: %s", tarea,
	)

	planTexto, err := llmCall(modelPlanner, 800, promptPlanificador)
	if err != nil {
		return "", fmt.Errorf("planificador: %w", err)
	}

	plan, err := parsearPlan(planTexto)
	if err != nil {
		return "", fmt.Errorf("parseo: %w", err)
	}
	if err := validarPlan(plan); err != nil {
		return "", fmt.Errorf("validación: %w", err)
	}

	fmt.Printf("Plan generado (%d subtareas):\n", len(plan))
	for _, s := range plan {
		depsStr := "(sin deps)"
		if len(s.Deps) > 0 {
			depsStr = fmt.Sprintf("[deps: %v]", s.Deps)
		}
		objetivo := s.Objetivo
		if len(objetivo) > 60 {
			objetivo = objetivo[:60]
		}
		fmt.Printf("  %s: %s %s\n", s.ID, objetivo, depsStr)
	}

	// 2. Ejecutar DAG
	fmt.Println("\nEjecutando DAG:")
	resultados, err := ejecutarDag(plan)
	if err != nil {
		return "", fmt.Errorf("ejecución: %w", err)
	}

	// 3. Sintetizar
	var resultadosLines []string
	for k, v := range resultados {
		resultadosLines = append(resultadosLines, fmt.Sprintf("[%s] %s", k, v))
	}
	promptSintesis := fmt.Sprintf(
		"Tarea original: %s\n\nResultados de cada subtarea:\n%s\n\n"+
			"Genera la respuesta final integrando todos los resultados. Sé conciso.",
		tarea, strings.Join(resultadosLines, "\n"),
	)

	return llmCall(modelPlanner, 600, promptSintesis)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func main() {
	tarea := "Escribe un breve análisis comparativo de Python vs TypeScript para " +
		"desarrollo de agentes IA: (1) ecosistema de librerías, " +
		"(2) rendimiento async, (3) tipado y mantenibilidad."

	fmt.Printf("Tarea: %s\n\n", tarea)

	resultado, err := descomponerYEjecutar(tarea)
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
