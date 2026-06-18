// Evaluación de trayectoria: precision, recall, step efficiency, LLM-as-judge.

// Cómo ejecutar: make go FILE=go/14-observabilidad/trajectory.go

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

var modelTrajectory = envOr("SMALL_MODEL", "claude-haiku-4-5-20251001")

type Paso struct {
	Herramienta string
	Params      map[string]interface{}
	Resultado   interface{}
}

func (p Paso) String() string {
	type kv struct{ k string; v interface{} }
	var items []kv
	for k, v := range p.Params {
		items = append(items, kv{k, v})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].k < items[j].k })
	var parts []string
	for _, item := range items {
		parts = append(parts, fmt.Sprintf("('%s', %v)", item.k, item.v))
	}
	return fmt.Sprintf("%s([%s])", p.Herramienta, strings.Join(parts, ", "))
}

type ResultadoTrayectoria struct {
	TrajectoryPrecision  float64
	TrajectoryRecall     float64
	TrajectoryExactMatch bool
	StepEfficiency       float64
	NPasosAgente         int
	NPasosGT             int
	PrimerErrorHerramienta map[string]interface{}
}

func evaluarTrayectoria(agente, groundTruth []Paso) ResultadoTrayectoria {
	gtSet := make(map[string]bool)
	for _, p := range groundTruth {
		gtSet[p.String()] = true
	}

	var tp int
	for _, p := range agente {
		if gtSet[p.String()] {
			tp++
		}
	}

	precision := 0.0
	if len(agente) > 0 {
		precision = float64(tp) / float64(len(agente))
	}
	recall := 0.0
	if len(groundTruth) > 0 {
		recall = float64(tp) / float64(len(groundTruth))
	}
	efficiency := 0.0
	if len(agente) > 0 {
		efficiency = float64(len(groundTruth)) / float64(len(agente))
	}

	gtStrs := make([]string, len(groundTruth))
	for i, p := range groundTruth {
		gtStrs[i] = p.String()
	}
	agenteStrs := make([]string, len(agente))
	for i, p := range agente {
		agenteStrs[i] = p.String()
	}
	exactMatch := strings.Join(agenteStrs, "|") == strings.Join(gtStrs, "|")

	var primerError map[string]interface{}
	for i := 0; i < len(agente) && i < len(groundTruth); i++ {
		if agente[i].Herramienta != groundTruth[i].Herramienta {
			primerError = map[string]interface{}{
				"step":   i,
				"agente": agente[i].Herramienta,
				"gt":     groundTruth[i].Herramienta,
			}
			break
		}
	}

	return ResultadoTrayectoria{
		TrajectoryPrecision:    round3(precision),
		TrajectoryRecall:       round3(recall),
		TrajectoryExactMatch:   exactMatch,
		StepEfficiency:         round3(efficiency),
		NPasosAgente:           len(agente),
		NPasosGT:               len(groundTruth),
		PrimerErrorHerramienta: primerError,
	}
}

func round3(f float64) float64 {
	return float64(int(f*1000+0.5)) / 1000
}

func evaluarTrayectoriaConJuez(trayectoria []Paso, objetivo string) map[string]interface{} {
	var sb strings.Builder
	for i, p := range trayectoria {
		sb.WriteString(fmt.Sprintf("Step %d: %s(%v) → %v\n", i+1, p.Herramienta, p.Params, p.Resultado))
	}

	prompt := fmt.Sprintf(`Evalúa si la siguiente secuencia de pasos es eficiente y correcta para el objetivo dado.

OBJETIVO: %s

PASOS EJECUTADOS:
%s

Responde en JSON con este formato exacto:
{"es_correcta": <true/false>, "es_eficiente": <true/false>, "pasos_innecesarios": [<indices base-1>], "pasos_faltantes": [<descripción>], "puntuacion": <1-10>, "razon": "<explicación breve>"}`,
		objetivo, sb.String())

	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	reqBody, _ := json.Marshal(map[string]interface{}{
		"model":     modelTrajectory,
		"max_tokens": 512,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
	})

	req, _ := http.NewRequest("POST", envBaseURL(), bytes.NewReader(reqBody))
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("content-type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var apiResp struct {
		Content []struct{ Text string `json:"text"` } `json:"content"`
	}
	json.Unmarshal(body, &apiResp)
	if len(apiResp.Content) == 0 {
		return map[string]interface{}{"error": "no content"}
	}

	var result map[string]interface{}
	if err := json.Unmarshal([]byte(apiResp.Content[0].Text), &result); err != nil {
		return map[string]interface{}{"error": "parse fallido", "raw": apiResp.Content[0].Text[:200]}
	}
	return result
}

var groundTruth = map[string][]Paso{
	"precio_cobre": {
		{Herramienta: "search_web", Params: map[string]interface{}{"query": "precio cobre USD libra hoy"}},
		{Herramienta: "parse_number", Params: map[string]interface{}{"texto": "$resultado_anterior"}},
	},
}

func main() {
	fmt.Println("=== Evaluación de trayectoria ===\n")

	gt := groundTruth["precio_cobre"]
	trayCorrecta := []Paso{
		{Herramienta: "search_web", Params: map[string]interface{}{"query": "precio cobre USD libra hoy"}, Resultado: "$4.23/lb"},
		{Herramienta: "parse_number", Params: map[string]interface{}{"texto": "$4.23/lb"}, Resultado: 4.23},
	}
	res := evaluarTrayectoria(trayCorrecta, gt)
	fmt.Println("Trayectoria correcta:")
	fmt.Printf("  Precision: %.3f | Recall: %.3f\n", res.TrajectoryPrecision, res.TrajectoryRecall)
	fmt.Printf("  Exact match: %v | Efficiency: %.3f\n", res.TrajectoryExactMatch, res.StepEfficiency)

	trayIneficiente := []Paso{
		{Herramienta: "search_web", Params: map[string]interface{}{"query": "precio cobre"}},
		{Herramienta: "search_web", Params: map[string]interface{}{"query": "precio cobre USD"}},
		{Herramienta: "search_web", Params: map[string]interface{}{"query": "precio cobre USD libra hoy"}, Resultado: "$4.23/lb"},
		{Herramienta: "parse_number", Params: map[string]interface{}{"texto": "$4.23/lb"}, Resultado: 4.23},
	}
	res2 := evaluarTrayectoria(trayIneficiente, gt)
	fmt.Println("\nTrayectoria ineficiente (3 búsquedas en lugar de 1):")
	fmt.Printf("  Precision: %.3f | Recall: %.3f\n", res2.TrajectoryPrecision, res2.TrajectoryRecall)
	fmt.Printf("  Exact match: %v | Efficiency: %.3f\n", res2.TrajectoryExactMatch, res2.StepEfficiency)
	if res2.StepEfficiency > 0 {
		fmt.Printf("  → Un agente así cuesta %.1f× más que el óptimo\n", 1/res2.StepEfficiency)
	}

	fmt.Println("\nEvaluación con LLM-as-judge:")
	veredicto := evaluarTrayectoriaConJuez(trayIneficiente, "Obtener el precio actual del cobre en USD/libra")
	fmt.Printf("  Es correcta: %v\n", veredicto["es_correcta"])
	fmt.Printf("  Es eficiente: %v\n", veredicto["es_eficiente"])
	fmt.Printf("  Puntuación: %v/10\n", veredicto["puntuacion"])
	if razon, ok := veredicto["razon"].(string); ok {
		if len(razon) > 200 {
			razon = razon[:200]
		}
		fmt.Printf("  Razón: %s\n", razon)
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
