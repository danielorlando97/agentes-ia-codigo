// Patrón Mercado/Subasta: el orquestador descompone la tarea, consulta a los workers
// por bids (confianza + tokens estimados), asigna según utilidad = confianza/tokens.

// Cómo ejecutar: make go FILE=go/12-multi-agente/mercado.go

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

var modelMercado = envOr("SMALL_MODEL", "claude-haiku-4-5-20251001")

type Worker struct {
	ID           string
	Capabilities []string
	CurrentLoad  int
	SystemPrompt string
}

type Bid struct {
	WorkerID        string
	Confidence      float64
	EstimatedTokens int
}

type Subtarea struct {
	ID                 string `json:"id"`
	Descripcion        string `json:"descripcion"`
	CapacidadRequerida string `json:"capacidad_requerida"`
}

func utility(bid Bid) float64 {
	if bid.EstimatedTokens <= 0 {
		return 0
	}
	return bid.Confidence / float64(bid.EstimatedTokens)
}

func makeWorker(id string, capabilities []string) Worker {
	caps := strings.Join(capabilities, ", ")
	return Worker{
		ID:           id,
		Capabilities: capabilities,
		CurrentLoad:  0,
		SystemPrompt: fmt.Sprintf("Eres el worker %s. Tus capacidades son: %s. Ejecuta las tareas que te asignen con precisión.", id, caps),
	}
}

func llamarLLMMercado(system, user string, temperature float64) (string, error) {
	type Msg struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	body, _ := json.Marshal(map[string]interface{}{
		"model":       modelMercado,
		"max_tokens":  800,
		"system":      system,
		"messages":    []Msg{{Role: "user", Content: user}},
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
	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	json.Unmarshal(respBody, &result)
	if len(result.Content) > 0 {
		return strings.TrimSpace(result.Content[0].Text), nil
	}
	return "", fmt.Errorf("respuesta vacía")
}

func orquestadorDescomponer(tarea string) ([]Subtarea, error) {
	system := "Eres un orquestador. Descompone la tarea en subtareas concretas. " +
		"Para cada subtarea especifica la capacidad necesaria. " +
		`Responde SOLO con JSON válido: [{"id": "s1", "descripcion": "...", "capacidad_requerida": "..."}, ...]`
	raw, err := llamarLLMMercado(system, "Tarea: "+tarea, 0.0)
	if err != nil {
		return nil, err
	}
	inicio := strings.Index(raw, "[")
	fin := strings.LastIndex(raw, "]") + 1
	if inicio == -1 || fin == 0 {
		return nil, fmt.Errorf("no se encontró JSON array en respuesta")
	}
	var subtareas []Subtarea
	return subtareas, json.Unmarshal([]byte(raw[inicio:fin]), &subtareas)
}

func solicitarBid(worker Worker, subtarea Subtarea) Bid {
	system := fmt.Sprintf(
		"Eres el worker %s. Tus capacidades: %s. Carga actual: %d tareas. "+
			"Evalúa si puedes ejecutar la subtarea. "+
			`Responde SOLO con JSON: {"confidence": <0.0-1.0>, "estimated_tokens": <int>}. `+
			"Si no puedes, confidence debe ser 0.0.",
		worker.ID, strings.Join(worker.Capabilities, ", "), worker.CurrentLoad,
	)
	raw, err := llamarLLMMercado(
		system,
		fmt.Sprintf("Subtarea: %s\nCapacidad requerida: %s", subtarea.Descripcion, subtarea.CapacidadRequerida),
		0.0,
	)
	if err != nil {
		return Bid{WorkerID: worker.ID, Confidence: 0, EstimatedTokens: 1}
	}
	inicio := strings.Index(raw, "{")
	fin := strings.LastIndex(raw, "}") + 1
	if inicio == -1 || fin == 0 {
		return Bid{WorkerID: worker.ID, Confidence: 0, EstimatedTokens: 1}
	}
	var parsed struct {
		Confidence      float64 `json:"confidence"`
		EstimatedTokens int     `json:"estimated_tokens"`
	}
	if err := json.Unmarshal([]byte(raw[inicio:fin]), &parsed); err != nil {
		return Bid{WorkerID: worker.ID, Confidence: 0, EstimatedTokens: 1}
	}
	tokens := parsed.EstimatedTokens
	if tokens < 1 {
		tokens = 200
	}
	return Bid{WorkerID: worker.ID, Confidence: parsed.Confidence, EstimatedTokens: tokens}
}

func ejecutarSubtarea(worker Worker, subtarea Subtarea, contexto string) (string, error) {
	ctxTexto := contexto
	if ctxTexto == "" {
		ctxTexto = "(ninguno)"
	}
	user := fmt.Sprintf("Subtarea a ejecutar: %s\n\nContexto disponible:\n%s", subtarea.Descripcion, ctxTexto)
	return llamarLLMMercado(worker.SystemPrompt, user, 0.0)
}

func orquestadorSintetizar(tarea string, resultados map[string]string) (string, error) {
	system := "Eres un orquestador. Sintetiza los resultados de las subtareas en una respuesta final coherente."
	var partes []string
	for id, r := range resultados {
		partes = append(partes, fmt.Sprintf("Subtarea %s:\n%s", id, r))
	}
	return llamarLLMMercado(
		system,
		fmt.Sprintf("Tarea original: %s\n\nResultados de subtareas:\n%s", tarea, strings.Join(partes, "\n\n")),
		0.0,
	)
}

func mercado(tarea string, workers []Worker) (string, error) {
	fmt.Printf("[Mercado] Descomponiendo tarea: %s\n", tarea)
	subtareas, err := orquestadorDescomponer(tarea)
	if err != nil {
		return "", err
	}
	fmt.Printf("  → %d subtareas identificadas\n", len(subtareas))

	resultados := make(map[string]string)

	for _, subtarea := range subtareas {
		fmt.Printf("\n[Licitación] Subtarea: %s\n", subtarea.Descripcion)

		var disponibles []Worker
		for _, w := range workers {
			if w.CurrentLoad < 3 {
				disponibles = append(disponibles, w)
			}
		}

		bids := make([]Bid, len(disponibles))
		for i, w := range disponibles {
			bids[i] = solicitarBid(w, subtarea)
		}

		var filtrados []Bid
		for _, b := range bids {
			if b.Confidence > 0.1 {
				filtrados = append(filtrados, b)
			}
		}
		if len(filtrados) == 0 {
			fmt.Println("  ✗ Ningún worker disponible para esta subtarea")
			resultados[subtarea.ID] = "[Sin worker disponible]"
			continue
		}

		mejor := filtrados[0]
		for _, b := range filtrados[1:] {
			if utility(b) > utility(mejor) {
				mejor = b
			}
		}

		var workerAsignado *Worker
		for i := range workers {
			if workers[i].ID == mejor.WorkerID {
				workerAsignado = &workers[i]
				break
			}
		}
		fmt.Printf("  → Asignado a %s (confianza=%.2f, tokens_est=%d)\n",
			workerAsignado.ID, mejor.Confidence, mejor.EstimatedTokens)

		workerAsignado.CurrentLoad++
		var ctxParts []string
		for id, r := range resultados {
			ctxParts = append(ctxParts, fmt.Sprintf("%s: %s", id, r))
		}
		resultado, err := ejecutarSubtarea(*workerAsignado, subtarea, strings.Join(ctxParts, "\n"))
		workerAsignado.CurrentLoad--
		if err != nil {
			return "", err
		}
		resultados[subtarea.ID] = resultado
		display := resultado
		if len(display) > 60 {
			display = display[:60]
		}
		fmt.Printf("  ✓ Completada: %s...\n", display)
	}

	fmt.Println("\n[Síntesis] Combinando resultados...")
	return orquestadorSintetizar(tarea, resultados)
}

func main() {
	workers := []Worker{
		makeWorker("W1", []string{"búsqueda web", "síntesis de información", "redacción"}),
		makeWorker("W2", []string{"análisis de datos", "estadísticas", "cálculos"}),
		makeWorker("W3", []string{"búsqueda web", "análisis competitivo", "comparación"}),
	}

	tarea := "Investiga y compara las principales características de los tres frameworks web de Python más populares (Django, FastAPI, Flask) para un equipo de startup de 5 personas."
	fmt.Printf("Tarea: %s\n\n", tarea)

	resultado, err := mercado(tarea, workers)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
	fmt.Printf("\nResultado final:\n%s\n", resultado)
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
