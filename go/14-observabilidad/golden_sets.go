// Cómo ejecutar: make go FILE=go/14-observabilidad/golden_sets.go
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

var modelGolden = envOr("SMALL_MODEL", "claude-haiku-4-5-20251001")

type CasoEval struct {
	ID       string
	Input    string
	Expected string
	Tipo     string
	Criterio string
	Peso     float64
	TestFn   func(string) bool
}

type ResultadoEval struct {
	Caso   CasoEval
	Output string
	Paso   bool
	Detalle string
}

func evaluarCriterio(output string, caso CasoEval) (bool, string) {
	switch caso.Criterio {
	case "exact":
		paso := strings.TrimSpace(output) == strings.TrimSpace(caso.Expected)
		a := strings.TrimSpace(output)
		if len(a) > 60 {
			a = a[:60]
		}
		b := caso.Expected
		if len(b) > 60 {
			b = b[:60]
		}
		return paso, fmt.Sprintf("exact: '%s' vs '%s'", a, b)

	case "contains":
		paso := strings.Contains(strings.ToLower(output), strings.ToLower(caso.Expected))
		si := "no"
		if paso {
			si = "sí"
		}
		return paso, fmt.Sprintf("contains '%s': %s", caso.Expected, si)

	case "regex":
		re, err := regexp.Compile("(?i)" + caso.Expected)
		if err != nil {
			return false, fmt.Sprintf("regex inválido: %v", err)
		}
		paso := re.MatchString(output)
		match := "no match"
		if paso {
			match = "match"
		}
		return paso, fmt.Sprintf("regex '%s': %s", caso.Expected, match)

	case "no_tool":
		paso := !strings.Contains(output, "[TOOL:") && !strings.Contains(output, "tool_use")
		ok := "ok"
		if !paso {
			ok = "herramienta ejecutada"
		}
		return paso, "no_tool: " + ok

	case "fn":
		if caso.TestFn != nil {
			paso := caso.TestFn(output)
			ok := "ok"
			if !paso {
				ok = "fallo"
			}
			return paso, "fn: " + ok
		}
	}
	return false, fmt.Sprintf("criterio '%s' no reconocido", caso.Criterio)
}

type MensajeGolden struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type APIRequestGolden struct {
	Model     string          `json:"model"`
	MaxTokens int             `json:"max_tokens"`
	Messages  []MensajeGolden `json:"messages"`
}

type ContentBlockGolden struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type APIResponseGolden struct {
	Content []ContentBlockGolden `json:"content"`
}

func llamarAPIGolden(prompt string) (string, error) {
	payload := APIRequestGolden{
		Model:     modelGolden,
		MaxTokens: 256,
		Messages:  []MensajeGolden{{Role: "user", Content: prompt}},
	}
	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", envBaseURL(), bytes.NewReader(body))
	req.Header.Set("x-api-key", os.Getenv("ANTHROPIC_API_KEY"))
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("content-type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)
	var ar APIResponseGolden
	if err := json.Unmarshal(data, &ar); err != nil || len(ar.Content) == 0 {
		return "", fmt.Errorf("respuesta inesperada: %s", data)
	}
	for _, b := range ar.Content {
		if b.Type == "text" {
			return b.Text, nil
		}
	}
	return "", nil
}

func evaluarGoldenSet(
	agenteFn func(string) (string, error),
	goldenSet []CasoEval,
) map[string]interface{} {
	var resultados []ResultadoEval

	for _, caso := range goldenSet {
		output, err := agenteFn(caso.Input)
		if err != nil {
			output = ""
		}
		paso, detalle := evaluarCriterio(output, caso)
		resultados = append(resultados, ResultadoEval{Caso: caso, Output: output, Paso: paso, Detalle: detalle})
		estado := "✗"
		if paso {
			estado = "✓"
		}
		fmt.Printf("  [%s] [%s] %s: %s\n", estado, caso.Tipo, caso.ID, detalle)
	}

	pesoTotal := 0.0
	pesoPasado := 0.0
	for _, r := range resultados {
		pesoTotal += r.Caso.Peso
		if r.Paso {
			pesoPasado += r.Caso.Peso
		}
	}
	passRate := 0.0
	if pesoTotal > 0 {
		passRate = pesoPasado / pesoTotal
	}

	var fallos [][2]string
	for _, r := range resultados {
		if !r.Paso {
			fallos = append(fallos, [2]string{r.Caso.ID, r.Detalle})
		}
	}

	porTipo := map[string]map[string]int{}
	for _, r := range resultados {
		t := r.Caso.Tipo
		if _, ok := porTipo[t]; !ok {
			porTipo[t] = map[string]int{"total": 0, "pasados": 0}
		}
		porTipo[t]["total"]++
		if r.Paso {
			porTipo[t]["pasados"]++
		}
	}

	return map[string]interface{}{
		"pass_rate":           passRate,
		"pass_rate_ponderado": passRate,
		"total_casos":         len(resultados),
		"casos_fallidos":      len(fallos),
		"fallos":              fallos,
		"por_tipo":            porTipo,
	}
}

var goldenSet = []CasoEval{
	{
		ID:       "gs-001",
		Input:    "¿Cuántos días tiene una semana?",
		Expected: "7",
		Tipo:     "fact_lookup",
		Criterio: "contains",
		Peso:     1.0,
	},
	{
		ID:       "gs-002",
		Input:    "Lista 3 frutas separadas por coma.",
		Expected: `\w+,\s*\w+,\s*\w+`,
		Tipo:     "formatting",
		Criterio: "regex",
		Peso:     1.0,
	},
	{
		ID:       "gs-003",
		Input:    "¿Cuántos días tiene el año?",
		Expected: "365",
		Tipo:     "fact_lookup",
		Criterio: "contains",
		Peso:     1.5,
	},
	{
		ID:       "gs-004",
		Input:    "Responde solo con el número: 2 + 2",
		Expected: "4",
		Tipo:     "fact_lookup",
		Criterio: "exact",
		Peso:     1.0,
	},
}

func main() {
	fmt.Println("=== Golden set runner ===\n")

	resultado := evaluarGoldenSet(llamarAPIGolden, goldenSet)

	passRate := resultado["pass_rate"].(float64)
	fmt.Printf("\nPass rate: %.1f%%\n", passRate*100)
	fmt.Printf("Casos: %d total, %d fallidos\n", resultado["total_casos"], resultado["casos_fallidos"])

	porTipoJSON, _ := json.Marshal(resultado["por_tipo"])
	fmt.Printf("Por tipo: %s\n", porTipoJSON)

	umbralDeploy := 0.85
	if passRate < umbralDeploy {
		fmt.Printf("\n[BLOQUEADO] Pass rate %.1f%% < %.0f%%\n", passRate*100, umbralDeploy*100)
	} else {
		fmt.Printf("\n[OK] Deploy autorizado — pass rate %.1f%%\n", passRate*100)
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
