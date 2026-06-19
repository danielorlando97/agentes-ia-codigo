// Mini-proyecto: El debate de 3.
//
// Tres agentes con perspectivas diferentes debaten una propuesta técnica.
// Al final, un juez sintetiza el consenso. Observa cómo los agentes
// construyen sobre los argumentos de los otros y cuándo convergen.
//
// Cómo ejecutar:
//
//	export ANTHROPIC_API_KEY=sk-ant-...
//	make go FILE=go/12-multi-agente/mini-debate-3.go
//
// Variables de entorno:
//
//	MODEL     — modelo a usar (default: claude-haiku-4-5-20251001)
//	RONDAS    — rondas de debate (default: 2)
//	PROPUESTA — propuesta técnica a debatir
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
)

const debateVersion = "2023-06-01"

func debateEndpoint() string {
	if base := os.Getenv("ANTHROPIC_BASE_URL"); base != "" {
		return base + "/v1/messages"
	}
	return "https://api.anthropic.com/v1/messages"
}

var (
	debateModel = getDebateEnv("MODEL", "claude-haiku-4-5-20251001")
	rondas      = getDebateEnvInt("RONDAS", 2)
	propuesta   = getDebateEnv("PROPUESTA",
		"Migrar el backend de Python a TypeScript para mejorar la mantenibilidad del equipo")
)

func getDebateEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getDebateEnvInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

// ── Roles ──────────────────────────────────────────────────────────────────

type rol struct {
	System string
	Emoji  string
}

var roles = map[string]rol{
	"optimista": {
		Emoji: "🟢",
		System: "Eres el Arquitecto Optimista en un panel técnico. Tu rol es defender los beneficios " +
			"de la propuesta presentada. Argumentas con datos concretos, ejemplos reales y ROI. " +
			"Sé conciso (3-4 frases). Puedes responder a objeciones específicas de los otros panelistas.",
	},
	"escéptico": {
		Emoji: "🔴",
		System: "Eres el Ingeniero Escéptico en un panel técnico. Tu rol es identificar riesgos, " +
			"costos ocultos y casos donde la propuesta podría fallar. " +
			"Sé conciso (3-4 frases). No rechaces la propuesta en su totalidad — señala condiciones " +
			"bajo las cuales sí tendría sentido.",
	},
	"pragmatico": {
		Emoji: "🟡",
		System: "Eres el Lead Engineer Pragmático en un panel técnico. Tu rol es evaluar la propuesta " +
			"desde la perspectiva de implementación real: timeline, recursos, migration path. " +
			"Propones variantes o fases que reduzcan el riesgo. Sé conciso (3-4 frases).",
	},
}

var ordenRoles = []string{"optimista", "escéptico", "pragmatico"}

// ── API ────────────────────────────────────────────────────────────────────

type debMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type debReq struct {
	Model     string   `json:"model"`
	MaxTokens int      `json:"max_tokens"`
	System    string   `json:"system,omitempty"`
	Messages  []debMsg `json:"messages"`
}

type debResp struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

func callAPI(system, content string, maxTokens int) (string, error) {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	payload := debReq{
		Model:     debateModel,
		MaxTokens: maxTokens,
		System:    system,
		Messages:  []debMsg{{Role: "user", Content: content}},
	}
	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", debateEndpoint(), bytes.NewReader(body))
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", debateVersion)
	req.Header.Set("content-type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	var dr debResp
	if err := json.Unmarshal(data, &dr); err != nil || len(dr.Content) == 0 {
		return "", fmt.Errorf("respuesta inválida: %s", data)
	}
	return strings.TrimSpace(dr.Content[0].Text), nil
}

// ── Debate ─────────────────────────────────────────────────────────────────

type entrada struct {
	Rol       string
	Argumento string
	Ronda     int
}

func turnoAgente(r string, hist []entrada) (string, error) {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Propuesta: %s\n\n", propuesta))
	start := 0
	if len(hist) > 6 {
		start = len(hist) - 6
	}
	if len(hist) > 0 {
		sb.WriteString("Debate hasta ahora:\n")
		for _, e := range hist[start:] {
			sb.WriteString(fmt.Sprintf("%s: %s\n", strings.ToUpper(e.Rol), e.Argumento))
		}
	}
	sb.WriteString(fmt.Sprintf("\nTu turno como %s:", strings.ToUpper(r)))
	return callAPI(roles[r].System, sb.String(), 256)
}

func sintetizarDebate(hist []entrada) (string, error) {
	systemJuez := "Eres el Juez Sintetizador de un debate técnico. Analiza todos los argumentos presentados " +
		"y produce una síntesis equilibrada: qué aspectos de la propuesta tienen mérito, " +
		"qué riesgos son legítimos y una recomendación final con condiciones claras. " +
		"Sé objetivo y basa la recomendación en los argumentos más sólidos del debate."
	var lines []string
	for _, e := range hist {
		lines = append(lines, fmt.Sprintf("%s: %s", strings.ToUpper(e.Rol), e.Argumento))
	}
	content := fmt.Sprintf("Propuesta: %s\n\nDebate:\n%s", propuesta, strings.Join(lines, "\n"))
	return callAPI(systemJuez, content, 512)
}

// ── Main ───────────────────────────────────────────────────────────────────

func main() {
	fmt.Printf("\n%s\n", strings.Repeat("=", 64))
	fmt.Println("  DEBATE DE 3 AGENTES")
	fmt.Printf("  Modelo: %s  |  Rondas: %d\n", debateModel, rondas)
	fmt.Printf("%s\n", strings.Repeat("=", 64))
	fmt.Printf("\n  Propuesta: %s\n", propuesta)

	var historial []entrada

	for ronda := 1; ronda <= rondas; ronda++ {
		fmt.Printf("\n  ── Ronda %d ──\n", ronda)
		for _, r := range ordenRoles {
			fmt.Printf("\n  %s %s:\n", roles[r].Emoji, strings.ToUpper(r))
			argumento, err := turnoAgente(r, historial)
			if err != nil {
				fmt.Printf("  [error: %v]\n", err)
				continue
			}
			fmt.Printf("  %s\n", argumento)
			historial = append(historial, entrada{Rol: r, Argumento: argumento, Ronda: ronda})
		}
	}

	fmt.Printf("\n%s\n", strings.Repeat("─", 64))
	fmt.Println("  ⚖️  SÍNTESIS DEL JUEZ:")
	sintesis, err := sintetizarDebate(historial)
	if err != nil {
		fmt.Printf("  [error: %v]\n", err)
	} else {
		fmt.Printf("\n  %s\n", sintesis)
	}

	fmt.Printf("\n%s\n", strings.Repeat("=", 64))
	fmt.Println("  Estadísticas del debate:")
	fmt.Printf("  • %d intervenciones (%d rondas × 3 agentes)\n", len(historial), rondas)
	fmt.Printf("  • Tokens estimados: ~%d (sin contar síntesis)\n", len(historial)*300)
	fmt.Println("  • Patrón: perspectivas divergentes → síntesis convergente")
	fmt.Printf("%s\n", strings.Repeat("=", 64))
}
