// Versionado de prompts y A/B testing: registro inmutable, rollback, canary deployments.

// Cómo ejecutar: make go FILE=go/17-produccion/versionado.go

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"time"
)

type VersionPrompt struct {
	ID          string
	Version     string
	Prompt      string
	Modelo      string
	Creado      time.Time
	Evaluacion  map[string]interface{}
	Activo      bool
	CanaryPeso  float64
}

var registro = map[string]*VersionPrompt{}
var historialActivos []string

func registrarPrompt(prompt, modelo, version string, evaluacion map[string]interface{}) string {
	h := sha256.Sum256([]byte(prompt + "::" + modelo))
	pid := fmt.Sprintf("%x", h[:8])
	registro[pid] = &VersionPrompt{
		ID:         pid,
		Version:    version,
		Prompt:     prompt,
		Modelo:     modelo,
		Creado:     time.Now(),
		Evaluacion: evaluacion,
	}
	fmt.Printf("[version] Registrado prompt %s v%s (%s)\n", pid, version, modelo)
	return pid
}

func activarPrompt(promptID string) {
	for _, p := range registro {
		p.Activo = false
		p.CanaryPeso = 0
	}
	registro[promptID].Activo = true
	historialActivos = append(historialActivos, promptID)
	fmt.Printf("[version] Activado prompt %s v%s\n", promptID, registro[promptID].Version)
}

func rollback() string {
	if len(historialActivos) < 2 {
		fmt.Println("[version] No hay versión anterior para rollback")
		return ""
	}
	historialActivos = historialActivos[:len(historialActivos)-1]
	anteriorID := historialActivos[len(historialActivos)-1]
	activarPrompt(anteriorID)
	fmt.Printf("[version] Rollback a %s\n", anteriorID)
	return anteriorID
}

func activarCanary(canaryID string, peso float64) {
	if _, ok := registro[canaryID]; !ok {
		fmt.Printf("[canary] Prompt %s no registrado\n", canaryID)
		return
	}
	registro[canaryID].CanaryPeso = peso
	fmt.Printf("[canary] Prompt %s recibe %.0f%% del tráfico\n", canaryID, peso*100)
}

func obtenerPromptParaRequest() *VersionPrompt {
	for _, p := range registro {
		if p.CanaryPeso > 0 && rand.Float64() < p.CanaryPeso {
			return p
		}
	}
	for _, p := range registro {
		if p.Activo {
			return p
		}
	}
	return nil
}

func llamarConVersion(pregunta string) map[string]string {
	pv := obtenerPromptParaRequest()
	if pv == nil {
		return map[string]string{"error": "no hay prompt activo"}
	}

	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	reqBody, _ := json.Marshal(map[string]interface{}{
		"model":      pv.Modelo,
		"max_tokens": 256,
		"system":     pv.Prompt,
		"messages":   []map[string]string{{"role": "user", "content": pregunta}},
	})

	req, _ := http.NewRequest("POST", envBaseURL(), bytes.NewReader(reqBody))
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("content-type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return map[string]string{"error": err.Error()}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var apiResp struct {
		Content []struct{ Text string `json:"text"` } `json:"content"`
	}
	json.Unmarshal(body, &apiResp)
	texto := ""
	if len(apiResp.Content) > 0 {
		texto = apiResp.Content[0].Text
	}
	return map[string]string{
		"respuesta":      texto,
		"prompt_version": pv.Version,
		"prompt_id":      pv.ID,
	}
}

func main() {
	rand.Seed(time.Now().UnixNano())

	sonnet := "claude-sonnet-4-6-20250219"

	fmt.Println("=== Registro y activación ===")
	v1 := registrarPrompt(
		"Eres un asistente técnico conciso.",
		sonnet, "1.0.0",
		map[string]interface{}{"pass_rate": 0.82, "casos": 50},
	)
	activarPrompt(v1)

	v2 := registrarPrompt(
		"Eres un asistente técnico conciso. Usa ejemplos de código cuando sea útil.",
		sonnet, "1.1.0",
		map[string]interface{}{"pass_rate": 0.87, "casos": 50},
	)
	_ = v2

	fmt.Println("\n=== Canary deployment (10% tráfico a v1.1.0) ===")
	activarCanary(v2, 0.10)

	fmt.Println("\n=== Llamadas con selección automática de versión ===")
	for i := 1; i <= 5; i++ {
		r := llamarConVersion("¿Qué es un agente ReAct?")
		respuesta := r["respuesta"]
		if len(respuesta) > 60 {
			respuesta = respuesta[:60]
		}
		fmt.Printf("Request %d: version=%s | respuesta=%s...\n", i, r["prompt_version"], respuesta)
	}

	fmt.Println("\n=== Rollback ===")
	activarPrompt(v2)
	rollback()
}

func envBaseURL() string {
	if v := os.Getenv("ANTHROPIC_BASE_URL"); v != "" {
		return v + "/v1/messages"
	}
	return "https://api.anthropic.com/v1/messages"
}
