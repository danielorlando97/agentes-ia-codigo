// Comparación de outputs con: sin rol, rol corto (~20 tokens), rol largo (~150 tokens).
//
// Demuestra:
// - Cómo el rol afecta el formato, nivel de detalle y tokens usados
// - Tarea: categorizar tickets de soporte por prioridad
// Sin SDK: HTTP directo contra la API de Anthropic.

// Cómo ejecutar: make go FILE=go/04-prompts/role-prompting.go

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
)

var (
	roleModel = envOr("MODEL", "claude-sonnet-4-6")
	roleAPIURL = envBaseURL()
)

// ─── 1. Tipos ─────────────────────────────────────────────────────────────────

type ticket struct {
	ID               string
	Text             string
	ExpectedPriority string
}

type resultItem struct {
	name string
	res  classTicketResult
}

type roleAPIRequest struct {
	Model     string        `json:"model"`
	MaxTokens int           `json:"max_tokens"`
	System    string        `json:"system"`
	Messages  []roleMessage `json:"messages"`
}

type roleMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type roleAPIResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Usage struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

type classTicketResult struct {
	Priority     string
	Reason       string
	RawOutput    string
	TokensInput  int
	TokensOutput int
}

type styleMarkers struct {
	MentionsSLA    bool
	MentionsImpact bool
	LengthChars    int
}

// ─── 2. Tickets ───────────────────────────────────────────────────────────────

var tickets = []ticket{
	{"T-001", "Nuestro sistema de pagos dejó de funcionar hace 10 minutos. No podemos procesar ninguna transacción. Perdemos dinero cada segundo.", "urgente"},
	{"T-002", "El botón de exportar a CSV en el módulo de reportes no funciona. Aparece un error 500. Lo necesitamos para el informe mensual del lunes.", "alta"},
	{"T-003", "¿Podrían añadir un modo oscuro a la interfaz? Sería más cómodo para trabajar de noche.", "baja"},
	{"T-004", "Necesito cambiar el correo de mi cuenta. He seguido los pasos de la documentación pero el botón de confirmar no aparece.", "media"},
	{"T-005", "La aplicación móvil se cierra inesperadamente al intentar adjuntar archivos de más de 10 MB. Ocurre en iOS 17 y Android 14. Varios clientes nos han reportado esto.", "alta"},
}

// ─── 3. System prompts ───────────────────────────────────────────────────────

const systemNoRole = `Categoriza tickets de soporte técnico por prioridad.

Las prioridades posibles son:
- urgente: el sistema está caído o hay pérdida económica inmediata
- alta: funcionalidad crítica bloqueada, afecta a múltiples usuarios
- media: funcionalidad degradada, hay workaround disponible
- baja: mejoras, preguntas o problemas menores sin impacto operativo

Responde con el siguiente formato JSON exacto:
{"prioridad": "<urgente|alta|media|baja>", "razon": "<una oración breve>"}`

const systemShortRole = `Eres un agente de soporte técnico.

Categoriza tickets por prioridad.

Las prioridades posibles son:
- urgente: el sistema está caído o hay pérdida económica inmediata
- alta: funcionalidad crítica bloqueada, afecta a múltiples usuarios
- media: funcionalidad degradada, hay workaround disponible
- baja: mejoras, preguntas o problemas menores sin impacto operativo

Responde con el siguiente formato JSON exacto:
{"prioridad": "<urgente|alta|media|baja>", "razon": "<una oración breve>"}`

const systemLongRole = `Eres Elena Martínez, ingeniera de soporte técnico senior con 8 años de experiencia en plataformas SaaS B2B. Especialista en triaging de incidencias críticas, llevas el registro de tiempo de resolución más bajo del equipo. Tu filosofía: priorizar con precisión quirúrgica porque una mala priorización ralentiza todo el equipo. Eres directa, metódica y nunca escatimas en claridad al justificar una decisión. Conoces de memoria las SLAs del equipo: urgente=1h, alta=4h, media=24h, baja=72h.

Categoriza tickets por prioridad siguiendo los criterios SLA:
- urgente: el sistema está caído o hay pérdida económica inmediata (SLA: 1h)
- alta: funcionalidad crítica bloqueada, afecta a múltiples usuarios (SLA: 4h)
- media: funcionalidad degradada, hay workaround disponible (SLA: 24h)
- baja: mejoras, preguntas o problemas menores sin impacto operativo (SLA: 72h)

Responde con el siguiente formato JSON exacto:
{"prioridad": "<urgente|alta|media|baja>", "razon": "<una oración breve>"}`

// ─── 4. Llamada a la API ──────────────────────────────────────────────────────

var jsonPattern = regexp.MustCompile(`\{[^}]+\}`)

func callRoleAPI(system, ticketText string) (*roleAPIResponse, error) {
	payload := roleAPIRequest{
		Model:     roleModel,
		MaxTokens: 200,
		System:    system,
		Messages:  []roleMessage{{Role: "user", Content: "Ticket: " + ticketText}},
	}

	body, _ := json.Marshal(payload)
	req, _ := http.NewRequestWithContext(context.Background(), "POST", roleAPIURL, bytes.NewReader(body))
	req.Header.Set("x-api-key", os.Getenv("ANTHROPIC_API_KEY"))
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("content-type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)
	var r roleAPIResponse
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("parse %s: %w", string(data), err)
	}
	return &r, nil
}

func classifyTicket(system, ticketText string) (classTicketResult, error) {
	resp, err := callRoleAPI(system, ticketText)
	if err != nil {
		return classTicketResult{}, err
	}

	output := ""
	if len(resp.Content) > 0 {
		output = strings.TrimSpace(resp.Content[0].Text)
	}

	priority := "unknown"
	reason := output

	if m := jsonPattern.FindString(output); m != "" {
		var data map[string]string
		if err := json.Unmarshal([]byte(m), &data); err == nil {
			if p, ok := data["prioridad"]; ok {
				priority = p
			}
			if r, ok := data["razon"]; ok {
				reason = r
			}
		}
	}

	return classTicketResult{
		Priority:     priority,
		Reason:       reason,
		RawOutput:    output,
		TokensInput:  resp.Usage.InputTokens,
		TokensOutput: resp.Usage.OutputTokens,
	}, nil
}

// ─── 5. Análisis de estilo ────────────────────────────────────────────────────

var slaPattern = regexp.MustCompile(`sla|hora|horas|plazo`)
var impactPattern = regexp.MustCompile(`usuario|cliente|pérdida|impacto`)

func detectStyleMarkers(output string) styleMarkers {
	lower := strings.ToLower(output)
	return styleMarkers{
		MentionsSLA:    slaPattern.MatchString(lower),
		MentionsImpact: impactPattern.MatchString(lower),
		LengthChars:    len(output),
	}
}

// ─── 6. Impresión de resultados ───────────────────────────────────────────────

func printTicketComparison(t ticket, results []resultItem) {
	fmt.Printf("\n%s\n", strings.Repeat("═", 72))
	text := t.Text
	if len(text) > 70 {
		text = text[:70] + "..."
	}
	fmt.Printf("  TICKET %s: %s\n", t.ID, text)
	fmt.Printf("  Prioridad esperada: %s\n", t.ExpectedPriority)
	fmt.Printf("%s\n", strings.Repeat("─", 72))

	for _, item := range results {
		correct := "✓"
		if item.res.Priority != t.ExpectedPriority {
			correct = "✗"
		}
		markers := detectStyleMarkers(item.res.RawOutput)
		fmt.Printf("\n  [%s] Prioridad: %s %s\n", item.name, item.res.Priority, correct)
		fmt.Printf("  Razón: %s\n", item.res.Reason)
		fmt.Printf("  Tokens: %d input / %d output\n", item.res.TokensInput, item.res.TokensOutput)
		fmt.Printf("  Estilo → SLA: %v, Impacto: %v, Longitud: %d chars\n",
			markers.MentionsSLA, markers.MentionsImpact, markers.LengthChars)
	}
}

func printRoleSummary(allResults [][]resultItem, ticketList []ticket) {
	type stat struct {
		correct  int
		tokensIn int
		withSLA  int
	}

	variantNames := make([]string, len(allResults[0]))
	for i, item := range allResults[0] {
		variantNames[i] = item.name
	}
	stats := make(map[string]*stat)
	for _, v := range variantNames {
		stats[v] = &stat{}
	}

	for i, ticketResults := range allResults {
		for _, item := range ticketResults {
			if item.res.Priority == ticketList[i].ExpectedPriority {
				stats[item.name].correct++
			}
			stats[item.name].tokensIn += item.res.TokensInput
			if detectStyleMarkers(item.res.RawOutput).MentionsSLA {
				stats[item.name].withSLA++
			}
		}
	}

	n := float64(len(ticketList))
	fmt.Printf("\n%s\n", strings.Repeat("═", 72))
	fmt.Println("  TABLA COMPARATIVA FINAL")
	fmt.Printf("%s\n", strings.Repeat("═", 72))
	fmt.Printf("  %-28s %10s %10s %14s\n", "Variante", "Accuracy", "Tokens/in", "Menciona SLA")
	fmt.Printf("  %s\n", strings.Repeat("-", 64))
	for _, v := range variantNames {
		s := stats[v]
		fmt.Printf("  %-28s %9.0f%% %9.0f %13.0f%%\n",
			v, float64(s.correct)/n*100, float64(s.tokensIn)/n, float64(s.withSLA)/n*100)
	}
	fmt.Println("\n  El rol largo añade tokens pero puede cambiar el lenguaje de la razón.")
	fmt.Println("  Alta accuracy con rol corto = el rol no añade valor semántico al resultado.")
}

// ─── 7. Main ──────────────────────────────────────────────────────────────────

func main() {
	type variant struct {
		name   string
		system string
	}
	variants := []variant{
		{"Sin rol", systemNoRole},
		{"Rol corto", systemShortRole},
		{"Rol largo", systemLongRole},
	}

	var allResults [][]resultItem

	for _, t := range tickets {
		var ticketResults []resultItem
		for _, v := range variants {
			r, err := classifyTicket(v.system, t.Text)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			ticketResults = append(ticketResults, resultItem{v.name, r})
		}
		printTicketComparison(t, ticketResults)
		allResults = append(allResults, ticketResults)
	}

	printRoleSummary(allResults, tickets)
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
