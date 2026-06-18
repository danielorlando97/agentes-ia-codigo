// Tree of Thoughts (ToT) — Yao et al. 2023 (arXiv:2305.10601).
//
// totBFS: beam search en anchura; conserva los beamWidth mejores por nivel.
// totDFS: búsqueda en profundidad con backtracking cuando el evaluador dice 'impossible'.
// proponer: genera k pensamientos candidatos desde el estado actual (temp=0.7).
// evaluar: clasifica cada estado como sure/maybe/impossible (temp=0.0).
// esSolucion: función configurable por el llamador.
//
// Requiere: ANTHROPIC_API_KEY en el entorno.

// Cómo ejecutar: make go FILE=go/08-bucle/tree_of_thoughts.go

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
)

const (
	branchingFactor   = 3
	profundidadMax    = 3
	beamWidth         = 3
)

var (
	totModel = envOr("MODEL", "claude-sonnet-4-6")
)

// --- API types ---

type totMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type totRequest struct {
	Model       string   `json:"model"`
	MaxTokens   int      `json:"max_tokens"`
	Temperature float64  `json:"temperature"`
	Messages    []totMsg `json:"messages"`
}

type totContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type totResponse struct {
	Content []totContentBlock `json:"content"`
}

func totCall(apiKey string, req totRequest) (string, error) {
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
	var out totResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("parse: %w\n%s", err, raw)
	}
	if len(out.Content) == 0 {
		return "", fmt.Errorf("empty response")
	}
	return out.Content[0].Text, nil
}

// --- ToT Agent ---

type ToTAgent struct {
	APIKey          string
	EsSolucion      func(estado, objetivo string) bool
	BranchingFactor int
	ProfundidadMax  int
	BeamWidth       int
	Model           string
}

func NewToTAgent(apiKey string, esSolucion func(string, string) bool) *ToTAgent {
	return &ToTAgent{
		APIKey:          apiKey,
		EsSolucion:      esSolucion,
		BranchingFactor: branchingFactor,
		ProfundidadMax:  profundidadMax,
		BeamWidth:       beamWidth,
		Model:           totModel,
	}
}

func (a *ToTAgent) proponer(estado string, k int) []string {
	prompt := fmt.Sprintf(
		"Estado actual del problema:\n%s\n\n"+
			"Genera %d posibles próximos pasos (uno por línea, comenzando con un verbo).",
		estado, k,
	)
	text, err := totCall(a.APIKey, totRequest{
		Model:       a.Model,
		MaxTokens:   300,
		Temperature: 0.7,
		Messages:    []totMsg{{Role: "user", Content: prompt}},
	})
	if err != nil {
		return nil
	}
	var lineas []string
	for _, l := range strings.Split(text, "\n") {
		if s := strings.TrimSpace(l); s != "" {
			lineas = append(lineas, s)
		}
	}
	if len(lineas) > k {
		lineas = lineas[:k]
	}
	return lineas
}

func (a *ToTAgent) evaluar(estado, objetivo string) string {
	prompt := fmt.Sprintf(
		"Objetivo: %s\nEstado: %s\n\n"+
			"¿Es posible alcanzar el objetivo desde este estado?\n"+
			"Responde SOLO una de estas tres palabras: sure | maybe | impossible",
		objetivo, estado,
	)
	text, err := totCall(a.APIKey, totRequest{
		Model:       a.Model,
		MaxTokens:   5,
		Temperature: 0.0,
		Messages:    []totMsg{{Role: "user", Content: prompt}},
	})
	if err != nil {
		return "maybe"
	}
	t := strings.ToLower(text)
	if strings.Contains(t, "sure") {
		return "sure"
	}
	if strings.Contains(t, "impossible") {
		return "impossible"
	}
	return "maybe"
}

// totBFS: beam search en anchura. Conserva los beamWidth mejores nodos por nivel.
func (a *ToTAgent) TotBFS(estadoInicial, objetivo string) string {
	type nodo struct {
		estado string
		eval   string // "sure" | "maybe"
	}

	frontera := []string{estadoInicial}

	for profundidad := range a.ProfundidadMax {
		var candidatos []nodo

		for _, estado := range frontera {
			if a.EsSolucion(estado, objetivo) {
				return estado
			}
			for _, prop := range a.proponer(estado, a.BranchingFactor) {
				nuevo := estado + "\n" + prop
				ev := a.evaluar(nuevo, objetivo)
				if ev != "impossible" {
					candidatos = append(candidatos, nodo{nuevo, ev})
				}
			}
		}

		// Ordenar: "sure" primero
		sort.SliceStable(candidatos, func(i, j int) bool {
			if candidatos[i].eval == "sure" && candidatos[j].eval != "sure" {
				return true
			}
			return false
		})

		if len(candidatos) > a.BeamWidth {
			candidatos = candidatos[:a.BeamWidth]
		}

		frontera = make([]string, len(candidatos))
		for i, c := range candidatos {
			frontera[i] = c.estado
		}

		fmt.Printf("  [BFS depth=%d] %d nodos en frontera\n", profundidad+1, len(frontera))

		if len(frontera) == 0 {
			break
		}
	}

	if len(frontera) > 0 {
		return frontera[0]
	}
	return ""
}

// totDFS: búsqueda en profundidad con backtracking.
func (a *ToTAgent) TotDFS(estado, objetivo string, depth int) string {
	if a.EsSolucion(estado, objetivo) {
		return estado
	}
	if depth >= a.ProfundidadMax {
		return ""
	}

	for _, prop := range a.proponer(estado, a.BranchingFactor) {
		nuevo := estado + "\n" + prop
		if a.evaluar(nuevo, objetivo) == "impossible" {
			trunc := prop
			if len(trunc) > 40 {
				trunc = trunc[:40]
			}
			fmt.Printf("  [DFS depth=%d] backtrack: '%s'\n", depth+1, trunc)
			continue
		}
		if resultado := a.TotDFS(nuevo, objetivo, depth+1); resultado != "" {
			return resultado
		}
	}

	return "" // backtrack desde este nodo
}

func main() {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "ANTHROPIC_API_KEY no está configurada")
		os.Exit(1)
	}

	esSolucion := func(estado, objetivo string) bool {
		e := strings.ToLower(estado)
		return strings.Contains(e, "5-7-5") ||
			(strings.Contains(e, "haiku") && strings.Contains(e, "sílabas") && strings.Count(estado, "\n") > 5)
	}

	agent := &ToTAgent{
		APIKey:          apiKey,
		EsSolucion:      esSolucion,
		BranchingFactor: 2,
		ProfundidadMax:  2,
		BeamWidth:       2,
		Model:           totModel,
	}

	objetivo := "Escribe un haiku sobre el otoño con 5-7-5 sílabas"
	estadoInicial := fmt.Sprintf("Objetivo: %s\nEstado: ningún pensamiento todavía.", objetivo)

	fmt.Println("=== BFS ===")
	resultado := agent.TotBFS(estadoInicial, objetivo)
	if resultado != "" {
		if len(resultado) > 300 {
			resultado = resultado[len(resultado)-300:]
		}
		fmt.Printf("Mejor estado:\n%s\n", resultado)
	} else {
		fmt.Println("No se encontró solución con BFS")
	}

	fmt.Println("\n=== DFS ===")
	resultadoDFS := agent.TotDFS(estadoInicial, objetivo, 0)
	if resultadoDFS != "" {
		if len(resultadoDFS) > 300 {
			resultadoDFS = resultadoDFS[len(resultadoDFS)-300:]
		}
		fmt.Printf("Estado DFS:\n%s\n", resultadoDFS)
	} else {
		fmt.Println("No se encontró solución con DFS")
	}
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
