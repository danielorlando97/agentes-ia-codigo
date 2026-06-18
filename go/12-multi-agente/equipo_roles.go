// Patrón Equipo de Roles (MetaGPT style): PM → Architect → ProjectManager → Engineer → QA
// Coordinados por un Message Pool con campo cause_by. Engineer tiene 3 reintentos.

// Cómo ejecutar: make go FILE=go/12-multi-agente/equipo_roles.go

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

var modelEquipo = envOr("SMALL_MODEL", "claude-haiku-4-5-20251001")
const maxEngineerRetries = 3

// ---- Message Pool ----

type PoolMessage struct {
	Content string
	Type    string
	CauseBy string
}

type MessagePool struct {
	messages []PoolMessage
}

func (p *MessagePool) Publish(content, tipo, causeBy string) {
	p.messages = append(p.messages, PoolMessage{Content: content, Type: tipo, CauseBy: causeBy})
}

func (p *MessagePool) Latest(tipo string) *PoolMessage {
	for i := len(p.messages) - 1; i >= 0; i-- {
		if p.messages[i].Type == tipo {
			return &p.messages[i]
		}
	}
	return nil
}

func (p *MessagePool) Summary() string {
	var sb strings.Builder
	for _, m := range p.messages {
		content := m.Content
		if len(content) > 60 {
			content = content[:60]
		}
		sb.WriteString(fmt.Sprintf("  [%s] %s: %s...\n", m.CauseBy, m.Type, content))
	}
	return sb.String()
}

// ---- Roles ----

type RoleSpec struct {
	ID           string
	SystemPrompt string
}

var roleSpecs = map[string]RoleSpec{
	"pm": {
		ID: "pm",
		SystemPrompt: "Eres un Product Manager senior. A partir del requisito del usuario, " +
			"escribe un PRD claro y estructurado. Incluye: objetivo, funcionalidades, " +
			"criterios de aceptación y restricciones técnicas. Sé específico.",
	},
	"architect": {
		ID: "architect",
		SystemPrompt: "Eres un Arquitecto de Software. A partir del PRD, diseña la arquitectura. " +
			"Incluye: componentes principales, interfaces, decisiones de diseño (con justificación) " +
			"y lista de archivos/módulos a crear.",
	},
	"pm_mgr": {
		ID: "pm_mgr",
		SystemPrompt: "Eres un Project Manager técnico. A partir del System Design, " +
			"crea un plan de implementación serializable con tareas y dependencias explícitas. " +
			"El ingeniero ejecutará este plan directamente.",
	},
	"engineer": {
		ID: "engineer",
		SystemPrompt: "Eres un Ingeniero de Software senior. A partir del plan, " +
			"escribe el código funcional completo. Incluye todos los archivos necesarios " +
			"con sintaxis correcta. El QA ejecutará tests — asegúrate de que sea ejecutable.",
	},
	"qa": {
		ID: "qa",
		SystemPrompt: "Eres un QA Engineer. Revisa el código contra el PRD y el System Design. " +
			"Busca: bugs de lógica, casos borde no cubiertos, violaciones de requisitos. " +
			`Responde con JSON: {"tiene_bugs": true/false, "bugs": [...], "veredicto": "PASA" o "FALLA"}.`,
	},
}

// ---- Helper ----

func llamarLLMEquipo(system, user string, temperature float64) (string, error) {
	type Msg struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	body, _ := json.Marshal(map[string]interface{}{
		"model":       modelEquipo,
		"max_tokens":  1200,
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

type TestReport struct {
	TieneBugs bool     `json:"tiene_bugs"`
	Bugs      []string `json:"bugs"`
	Veredicto string   `json:"veredicto"`
}

func parsearTestReport(raw string) TestReport {
	inicio := strings.Index(raw, "{")
	fin := strings.LastIndex(raw, "}") + 1
	if inicio == -1 || fin == 0 {
		return TestReport{TieneBugs: false, Bugs: nil, Veredicto: "PASA"}
	}
	var report TestReport
	if err := json.Unmarshal([]byte(raw[inicio:fin]), &report); err != nil {
		return TestReport{TieneBugs: false, Bugs: nil, Veredicto: "PASA"}
	}
	return report
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// ---- Pipeline principal ----

func equipoRoles(requisitoUsuario string) (string, error) {
	pool := &MessagePool{}

	// PM: genera PRD
	fmt.Println("[PM] Generando PRD...")
	prd, err := llamarLLMEquipo(roleSpecs["pm"].SystemPrompt, "Requisito del usuario: "+requisitoUsuario, 0.0)
	if err != nil {
		return "", err
	}
	pool.Publish(prd, "PRD", "pm")
	fmt.Printf("  PRD: %s...\n", trunc(prd, 80))

	// Architect: observa PM → SystemDesign
	fmt.Println("\n[Architect] Diseñando sistema...")
	prdMsg := pool.Latest("PRD")
	systemDesign, err := llamarLLMEquipo(roleSpecs["architect"].SystemPrompt, "PRD:\n"+prdMsg.Content, 0.0)
	if err != nil {
		return "", err
	}
	pool.Publish(systemDesign, "SystemDesign", "architect")
	fmt.Printf("  SystemDesign: %s...\n", trunc(systemDesign, 80))

	// PM_Manager: observa Architect → Plan
	fmt.Println("\n[ProjectManager] Creando plan...")
	archMsg := pool.Latest("SystemDesign")
	plan, err := llamarLLMEquipo(
		roleSpecs["pm_mgr"].SystemPrompt,
		fmt.Sprintf("PRD:\n%s\n\nSystem Design:\n%s", prdMsg.Content, archMsg.Content),
		0.0,
	)
	if err != nil {
		return "", err
	}
	pool.Publish(plan, "Plan", "pm_mgr")
	fmt.Printf("  Plan: %s...\n", trunc(plan, 80))

	// Engineer: observa PM_Manager → Code
	fmt.Println("\n[Engineer] Escribiendo código...")
	planMsg := pool.Latest("Plan")
	contextoEng := fmt.Sprintf("PRD:\n%s\n\nSystem Design:\n%s\n\nPlan:\n%s",
		prdMsg.Content, archMsg.Content, planMsg.Content)
	codigo, err := llamarLLMEquipo(roleSpecs["engineer"].SystemPrompt, contextoEng, 0.2)
	if err != nil {
		return "", err
	}
	pool.Publish(codigo, "Code", "engineer")
	fmt.Printf("  Code: %s...\n", trunc(codigo, 80))

	// QA: revisión inicial
	fmt.Println("\n[QA] Revisando código...")
	codeMsg := pool.Latest("Code")
	qaContexto := fmt.Sprintf("PRD:\n%s\n\nSystem Design:\n%s\n\nCódigo:\n%s",
		prdMsg.Content, archMsg.Content, codeMsg.Content)
	testReportRaw, err := llamarLLMEquipo(roleSpecs["qa"].SystemPrompt, qaContexto, 0.0)
	if err != nil {
		return "", err
	}
	pool.Publish(testReportRaw, "TestReport", "qa")
	reporte := parsearTestReport(testReportRaw)
	fmt.Printf("  Veredicto inicial: %s\n", reporte.Veredicto)

	// Bucle Engineer ↔ QA: hasta maxEngineerRetries
	intento := 0
	for reporte.TieneBugs && intento < maxEngineerRetries {
		intento++
		var bugsParts []string
		for _, b := range reporte.Bugs {
			bugsParts = append(bugsParts, "- "+b)
		}
		bugsTexto := strings.Join(bugsParts, "\n")
		fmt.Printf("\n[Engineer] Intento %d/%d — corrigiendo bugs:\n  %s\n",
			intento, maxEngineerRetries, trunc(bugsTexto, 120))

		codigoActual := pool.Latest("BugFix")
		if codigoActual == nil {
			codigoActual = pool.Latest("Code")
		}
		fix, err := llamarLLMEquipo(
			roleSpecs["engineer"].SystemPrompt,
			fmt.Sprintf("QA encontró bugs:\n%s\n\nCódigo actual:\n%s\n\nPRD:\n%s\n\nCorrige todos los bugs.",
				bugsTexto, codigoActual.Content, prdMsg.Content),
			0.2,
		)
		if err != nil {
			return "", err
		}
		pool.Publish(fix, "BugFix", "engineer")

		fmt.Printf("\n[QA] Re-revisando (intento %d)...\n", intento)
		testReportRaw, err = llamarLLMEquipo(
			roleSpecs["qa"].SystemPrompt,
			fmt.Sprintf("PRD:\n%s\n\nSystem Design:\n%s\n\nCódigo corregido:\n%s",
				prdMsg.Content, archMsg.Content, fix),
			0.0,
		)
		if err != nil {
			return "", err
		}
		pool.Publish(testReportRaw, "TestReport", "qa")
		reporte = parsearTestReport(testReportRaw)
		fmt.Printf("  Veredicto intento %d: %s\n", intento, reporte.Veredicto)
	}

	codigoFinal := pool.Latest("BugFix")
	if codigoFinal == nil {
		codigoFinal = pool.Latest("Code")
	}
	estado := "VALIDADO"
	if reporte.TieneBugs {
		estado = "MEJOR_INTENTO"
	}
	fmt.Printf("\n[Pipeline] Estado final: %s\n", estado)
	fmt.Printf("\n--- Pool de mensajes ---\n%s\n", pool.Summary())

	if codigoFinal == nil {
		return "[Sin código generado]", nil
	}
	return codigoFinal.Content, nil
}

func main() {
	requisito := "Implementa una función Python que calcule el número de Fibonacci de forma eficiente " +
		"usando memoización. Debe manejar n=0, n=1, y números grandes. " +
		"Incluye una función main() con ejemplos de uso."
	fmt.Printf("Requisito: %s\n%s\n\n", requisito, strings.Repeat("=", 60))

	resultado, err := equipoRoles(requisito)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
	fmt.Printf("\n%s\nCódigo final:\n%s\n", strings.Repeat("=", 60), resultado)
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
