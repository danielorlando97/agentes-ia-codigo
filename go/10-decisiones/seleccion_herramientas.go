// Selección dinámica de herramientas por similitud Jaccard.
//
// Demuestra el mecanismo de tool retrieval sin dependencias externas:
// Jaccard sobre word sets reemplaza embeddings para mostrar el bucle
// selección → agente → selección.
//
// Requiere: ANTHROPIC_API_KEY en el entorno.

// Cómo ejecutar: make go FILE=go/10-decisiones/seleccion_herramientas.go

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
	topK        = 3
	threshold   = 0.05
	systemAgente = "Eres un agente de asistencia. Tienes acceso a un subconjunto de herramientas " +
	"relevantes para la tarea actual. Usa las herramientas disponibles para responder. " +
	"Si no tienes suficiente información después de una ronda, indica qué necesitarías."
)

var (
	modelSel = envOr("MODEL", "claude-sonnet-4-6")
	apiURLSel = envBaseURL()
)

// --- Tipos de herramienta ---

type ToolSel struct {
	Name        string
	Description string
	IndexText   string
}

// ToolIndex es map[toolName]map[word]bool — set de palabras por herramienta.
type ToolIndex map[string]map[string]bool

// --- Indexación offline ---

func indexarToolsSel(tools []ToolSel) ToolIndex {
	idx := make(ToolIndex, len(tools))
	for _, t := range tools {
		idx[t.Name] = wordSetSel(t.IndexText)
	}
	return idx
}

func wordSetSel(text string) map[string]bool {
	set := make(map[string]bool)
	for _, w := range strings.Fields(strings.ToLower(text)) {
		set[w] = true
	}
	return set
}

// --- Selección en runtime ---

type scoredTool struct {
	name  string
	score float64
}

func seleccionarToolsSel(query string, idx ToolIndex) []string {
	queryWords := wordSetSel(query)
	scores := make([]scoredTool, 0, len(idx))

	for toolName, toolWords := range idx {
		union := make(map[string]bool)
		for w := range queryWords {
			union[w] = true
		}
		for w := range toolWords {
			union[w] = true
		}
		inter := 0
		for w := range queryWords {
			if toolWords[w] {
				inter++
			}
		}
		score := 0.0
		if len(union) > 0 {
			score = float64(inter) / float64(len(union))
		}
		scores = append(scores, scoredTool{toolName, score})
	}

	sort.Slice(scores, func(i, j int) bool {
		return scores[i].score > scores[j].score
	})

	result := make([]string, 0, topK)
	for _, s := range scores {
		if len(result) >= topK {
			break
		}
		if s.score >= threshold {
			result = append(result, s.name)
		}
	}
	return result
}

func construirQuerySeleccionSel(tarea, ultimoResultado string) string {
	if ultimoResultado == "" {
		return tarea
	}
	ctx := ultimoResultado
	if len(ctx) > 200 {
		ctx = ctx[:200]
	}
	return fmt.Sprintf("%s — contexto: %s", tarea, ctx)
}

// --- API types ---

type toolDefSel struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type msgSel struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type reqSel struct {
	Model     string       `json:"model"`
	MaxTokens int          `json:"max_tokens"`
	System    string       `json:"system"`
	Tools     []toolDefSel `json:"tools"`
	Messages  []msgSel     `json:"messages"`
}

type blockSel struct {
	Type  string          `json:"type"`
	Text  string          `json:"text,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
}

type respSel struct {
	Content    []blockSel `json:"content"`
	StopReason string     `json:"stop_reason"`
}

func llamarAPI(req reqSel, apiKey string) (respSel, error) {
	body, _ := json.Marshal(req)

	httpReq, _ := http.NewRequest("POST", apiURLSel, bytes.NewReader(body))
	httpReq.Header.Set("x-api-key", apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")
	httpReq.Header.Set("content-type", "application/json")

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return respSel{}, err
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	var r respSel
	if err := json.Unmarshal(raw, &r); err != nil {
		return respSel{}, fmt.Errorf("parse error: %w\nbody: %s", err, raw)
	}
	return r, nil
}

// --- Agente con selección dinámica ---

func agenteSel(tarea string, tools []ToolSel, apiKey string) {
	idx := indexarToolsSel(tools)
	toolsByName := make(map[string]ToolSel, len(tools))
	for _, t := range tools {
		toolsByName[t.Name] = t
	}

	ultimoResultado := ""
	fmt.Printf("\nTarea: %s\n", tarea)
	fmt.Println(strings.Repeat("=", 60))

	for turno := 1; turno <= 2; turno++ {
		querySel := construirQuerySeleccionSel(tarea, ultimoResultado)
		nombres := seleccionarToolsSel(querySel, idx)

		queryCorta := querySel
		if len(queryCorta) > 80 {
			queryCorta = queryCorta[:80]
		}
		fmt.Printf("\n[Turno %d] Query de selección: %s\n", turno, queryCorta)
		fmt.Printf("[Turno %d] Tools seleccionadas: %s\n", turno, strings.Join(nombres, ", "))

		toolDefs := make([]toolDefSel, 0, len(nombres))
		for _, name := range nombres {
			if t, ok := toolsByName[name]; ok {
				toolDefs = append(toolDefs, toolDefSel{
					Name:        t.Name,
					Description: t.Description,
					InputSchema: json.RawMessage(`{
						"type": "object",
						"properties": {
							"query": {"type": "string", "description": "Parámetro de consulta"}
						},
						"required": ["query"]
					}`),
				})
			}
		}

		resp, err := llamarAPI(reqSel{
			Model:     modelSel,
			MaxTokens: 512,
			System:    systemAgente,
			Tools:     toolDefs,
			Messages:  []msgSel{{Role: "user", Content: tarea}},
		}, apiKey)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error API: %v\n", err)
			return
		}

		acciones := []string{}
		texto := ""
		for _, block := range resp.Content {
			switch block.Type {
			case "tool_use":
				acciones = append(acciones, fmt.Sprintf("%s(%s)", block.Name, string(block.Input)))
			case "text":
				texto = block.Text
			}
		}

		if len(acciones) > 0 {
			ultimoResultado = "Llamadas planeadas: " + strings.Join(acciones, "; ")
			fmt.Printf("[Turno %d] Acciones: %s\n", turno, ultimoResultado)
		} else {
			if len(texto) > 200 {
				texto = texto[:200]
			}
			ultimoResultado = texto
			fmt.Printf("[Turno %d] Respuesta directa: %s\n", turno, ultimoResultado)
			break
		}
	}
}

// --- Catálogo de herramientas ---

var toolsSel = []ToolSel{
	{
		Name:        "buscar_contratos",
		Description: "Busca contratos por nombre de cliente, fecha o estado",
		IndexText:   "buscar contratos cliente acuerdo documento legal renovación fecha vencimiento",
	},
	{
		Name:        "calcular_fechas",
		Description: "Calcula diferencias entre fechas, días hasta vencimiento, rangos",
		IndexText:   "calcular fechas diferencia días semanas meses vencimiento plazo duración",
	},
	{
		Name:        "consultar_crm",
		Description: "Obtiene información de clientes del CRM: contactos, historial, estado",
		IndexText:   "crm cliente contacto historial estado cuenta empresa organización",
	},
	{
		Name:        "generar_factura",
		Description: "Crea facturas con detalle de servicios, impuestos y datos de pago",
		IndexText:   "factura generar crear cobro pago servicio impuesto importe total",
	},
	{
		Name:        "enviar_email",
		Description: "Envía correos electrónicos a clientes o equipos internos",
		IndexText:   "email correo enviar notificación mensaje destinatario asunto adjunto",
	},
	{
		Name:        "analizar_logs",
		Description: "Analiza logs de sistema para diagnosticar errores y anomalías",
		IndexText:   "logs errores sistema diagnosticar anomalía stack trace excepción servidor",
	},
}

func main() {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "ANTHROPIC_API_KEY no está definido")
		os.Exit(1)
	}

	agenteSel(
		"Busca el contrato de Acme Corp y calcula cuántos días faltan para su renovación",
		toolsSel,
		apiKey,
	)
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
