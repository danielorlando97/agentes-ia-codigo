// Construir un system prompt modular y medir el efecto del prompt caching.
//
// Demuestra:
// - System prompt con 5 bloques: identidad, instrucciones, herramientas, restricciones, ejemplos
// - Bloque estático con cache_control para los bloques que no cambian entre requests
// - Bloque dinámico sin cache para fecha y estado de sesión
// - Medir tokens cacheados vs no cacheados
// - Calcular ahorro de tokens en un batch de 10 requests con el mismo system prompt
// Sin SDK: HTTP directo contra la API de Anthropic.

// Cómo ejecutar: make go FILE=go/04-prompts/anatomia-system-prompt.go

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

var (
	cacheModel = envOr("MODEL", "claude-sonnet-4-6")
	cacheAPIURL = envBaseURL()
)

// ─── 1. Bloques estáticos ─────────────────────────────────────────────────────

const bloqueIdentidad = `Eres TechBot, el asistente de soporte técnico de TechStore.
Tu única función es resolver dudas sobre los productos y servicios de TechStore.
Eres directo, preciso y siempre confirmas si has entendido bien la pregunta antes de responder.`

const bloqueInstrucciones = `<instrucciones>
  Antes de responder, verifica que la pregunta está dentro de tu dominio (productos TechStore).
  Si la pregunta es sobre facturación, deriva al equipo de ventas sin dar precios.
  Si la pregunta es técnica, intenta resolverla en máximo 3 pasos.
  Termina siempre con: ¿Te ha sido útil esta respuesta?
</instrucciones>`

const bloqueHerramientas = `<herramientas_disponibles>
  - buscar_producto(nombre): busca información de un producto en el catálogo
  - consultar_estado_pedido(id_pedido): devuelve el estado de un pedido
  - crear_ticket_soporte(descripcion, prioridad): abre un ticket en el sistema
  Nota: no tienes acceso a información de precios ni a cuentas de usuario.
</herramientas_disponibles>`

const bloqueRestricciones = `<restricciones>
  NUNCA inventes precios. Si se pregunta por un precio, di: "No tengo ese dato. Contacta con ventas."
  NUNCA compartas información personal de otros clientes.
  NUNCA ejecutes acciones destructivas (cancelar pedidos, eliminar datos).
  Solo responde preguntas sobre TechStore. Fuera de dominio: redirige amablemente.
</restricciones>`

const bloqueEjemplos = `<ejemplos>
  <ejemplo>
    <usuario>Mi pedido #12345 no ha llegado</usuario>
    <asistente>Entendido. Consultaré el estado de tu pedido. ¿Tienes el número de seguimiento del transportista? ¿Te ha sido útil esta respuesta?</asistente>
  </ejemplo>
  <ejemplo>
    <usuario>¿Cuánto cuesta el Laptop ProX?</usuario>
    <asistente>No tengo acceso a información de precios actualizada. Por favor contacta con nuestro equipo de ventas en ventas@techstore.com. ¿Te ha sido útil esta respuesta?</asistente>
  </ejemplo>
</ejemplos>`

// ─── 2. Construcción de system prompts ───────────────────────────────────────

type systemBlock struct {
	Type         string            `json:"type"`
	Text         string            `json:"text"`
	CacheControl *cacheControlType `json:"cache_control,omitempty"`
}

type cacheControlType struct {
	Type string `json:"type"`
}

func buildSystemCached(dynamicInfo string) []systemBlock {
	staticContent := strings.Join([]string{
		bloqueIdentidad,
		bloqueInstrucciones,
		bloqueHerramientas,
		bloqueRestricciones,
		bloqueEjemplos,
	}, "\n\n")

	return []systemBlock{
		{
			Type:         "text",
			Text:         staticContent,
			CacheControl: &cacheControlType{Type: "ephemeral"},
		},
		{
			Type: "text",
			Text: dynamicInfo,
			// Sin CacheControl: siempre paga costo completo
		},
	}
}

func buildSystemNoCache(dynamicInfo string) string {
	return strings.Join([]string{
		bloqueIdentidad,
		bloqueInstrucciones,
		bloqueHerramientas,
		bloqueRestricciones,
		bloqueEjemplos,
		dynamicInfo,
	}, "\n\n")
}

// ─── 3. Tipos de request/response ─────────────────────────────────────────────

type cacheRequestCached struct {
	Model     string        `json:"model"`
	MaxTokens int           `json:"max_tokens"`
	System    []systemBlock `json:"system"`
	Messages  []cacheMsg    `json:"messages"`
}

type cacheRequestNoCache struct {
	Model     string     `json:"model"`
	MaxTokens int        `json:"max_tokens"`
	System    string     `json:"system"`
	Messages  []cacheMsg `json:"messages"`
}

type cacheMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type cacheAPIResponse struct {
	Usage struct {
		InputTokens              int `json:"input_tokens"`
		OutputTokens             int `json:"output_tokens"`
		CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
		CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	} `json:"usage"`
}

// ─── 4. Llamadas a la API ─────────────────────────────────────────────────────

func callCacheAPI(payload interface{}) (*cacheAPIResponse, error) {
	body, _ := json.Marshal(payload)
	req, _ := http.NewRequestWithContext(context.Background(), "POST", cacheAPIURL, bytes.NewReader(body))
	req.Header.Set("x-api-key", os.Getenv("ANTHROPIC_API_KEY"))
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("anthropic-beta", "prompt-caching-2024-07-31")
	req.Header.Set("content-type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)
	var r cacheAPIResponse
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("parse %s: %w", string(data), err)
	}
	return &r, nil
}

// ─── 5. Resultados de requests ────────────────────────────────────────────────

type requestResult struct {
	QuestionIdx          int
	Question             string
	InputTokens          int
	OutputTokens         int
	CacheCreationTokens  int
	CacheReadTokens      int
}

var questions = []string{
	"¿Dónde puedo ver el estado de mi pedido #45678?",
	"El adaptador HDMI que compré no funciona con mi televisor Samsung.",
	"¿Tienen garantía extendida para laptops?",
	"Necesito abrir un ticket porque recibí el producto equivocado.",
	"¿Cómo puedo devolver un artículo defectuoso?",
	"La aplicación de TechStore no me deja iniciar sesión.",
	"¿Tienen repuestos para el teclado MechType K85?",
	"Mi factura del mes pasado tiene un error de importe.",
	"¿Cuánto tiempo tarda el envío estándar?",
	"El mouse inalámbrico pierde conexión constantemente.",
}

func runBatchCached(qs []string) ([]requestResult, error) {
	var results []requestResult
	for i, q := range qs {
		now := time.Now().Format("2006-01-02 15:04:05")
		dynamicInfo := fmt.Sprintf("Fecha y hora: %s\nID de sesión: session-demo-%04d", now, i+1)
		system := buildSystemCached(dynamicInfo)

		payload := cacheRequestCached{
			Model:     cacheModel,
			MaxTokens: 200,
			System:    system,
			Messages:  []cacheMsg{{Role: "user", Content: q}},
		}

		resp, err := callCacheAPI(payload)
		if err != nil {
			return nil, err
		}

		r := requestResult{
			QuestionIdx:         i + 1,
			Question:            q,
			InputTokens:         resp.Usage.InputTokens,
			OutputTokens:        resp.Usage.OutputTokens,
			CacheCreationTokens: resp.Usage.CacheCreationInputTokens,
			CacheReadTokens:     resp.Usage.CacheReadInputTokens,
		}
		results = append(results, r)

		fmt.Printf("  Request %2d/%d: input=%5d, cache_write=%5d, cache_read=%5d\n",
			i+1, len(qs), r.InputTokens, r.CacheCreationTokens, r.CacheReadTokens)
	}
	return results, nil
}

func runBatchNoCache(qs []string) ([]requestResult, error) {
	var results []requestResult
	for i, q := range qs {
		now := time.Now().Format("2006-01-02 15:04:05")
		dynamicInfo := fmt.Sprintf("Fecha y hora: %s\nID de sesión: session-demo-%04d", now, i+1)
		system := buildSystemNoCache(dynamicInfo)

		payload := cacheRequestNoCache{
			Model:     cacheModel,
			MaxTokens: 200,
			System:    system,
			Messages:  []cacheMsg{{Role: "user", Content: q}},
		}

		resp, err := callCacheAPI(payload)
		if err != nil {
			return nil, err
		}

		results = append(results, requestResult{
			QuestionIdx:  i + 1,
			Question:     q,
			InputTokens:  resp.Usage.InputTokens,
			OutputTokens: resp.Usage.OutputTokens,
		})
	}
	return results, nil
}

// ─── 6. Análisis de resultados ────────────────────────────────────────────────

func analyzeSavingsGo(cached []requestResult, noCache []requestResult) {
	var totalInCached, totalInNoCache, totalWrites, totalReads int
	for _, r := range cached {
		totalInCached += r.InputTokens
		totalWrites += r.CacheCreationTokens
		totalReads += r.CacheReadTokens
	}
	for _, r := range noCache {
		totalInNoCache += r.InputTokens
	}

	costNoCache := float64(totalInNoCache) / 1_000_000 * 3.0
	costCached := float64(totalWrites)/1_000_000*3.75 +
		float64(totalReads)/1_000_000*0.30 +
		float64(totalInCached-totalReads)/1_000_000*3.0

	n := float64(len(cached))

	fmt.Printf("\n%s\n", strings.Repeat("═", 68))
	fmt.Println("  ANÁLISIS DE TOKENS Y AHORRO POR CACHING")
	fmt.Printf("%s\n", strings.Repeat("═", 68))
	fmt.Printf("\n  Batch: %d requests con el mismo system prompt estático\n", len(cached))
	fmt.Printf("\n  %-45s %10s %10s\n", "Métrica", "Sin cache", "Con cache")
	fmt.Printf("  %s\n", strings.Repeat("-", 67))
	fmt.Printf("  %-45s %10d %10d\n", "Tokens input totales", totalInNoCache, totalInCached)
	fmt.Printf("  %-45s %10s %10d\n", "Tokens cache_creation (escritura)", "—", totalWrites)
	fmt.Printf("  %-45s %10s %10d\n", "Tokens cache_read (lectura)", "—", totalReads)
	fmt.Printf("  %-45s %10.0f %10.0f\n", "Tokens input promedio por request",
		float64(totalInNoCache)/n, float64(totalInCached)/n)
	fmt.Printf("\n  %-45s $%9.4f $%9.4f\n", "Costo estimado del batch (USD)", costNoCache, costCached)

	if costNoCache > 0 {
		savingsPct := (1 - costCached/costNoCache) * 100
		savingsAbs := costNoCache - costCached
		fmt.Printf("\n  Ahorro: $%.4f USD (%.1f%%)\n", savingsAbs, savingsPct)
	}

	fmt.Printf("\n  Desglose por request (con cache):\n")
	fmt.Printf("  %4s  %7s  %12s  %11s\n", "Req", "Input", "Cache write", "Cache read")
	fmt.Printf("  %s\n", strings.Repeat("-", 40))
	for _, r := range cached {
		fmt.Printf("  %4d  %7d  %12d  %11d\n",
			r.QuestionIdx, r.InputTokens, r.CacheCreationTokens, r.CacheReadTokens)
	}

	fmt.Println("\n  Notas:")
	fmt.Println("  - Request 1: paga cache_creation (escribir el cache por primera vez)")
	fmt.Println("  - Requests 2+: pagan cache_read (~10% del precio de input estándar)")
	fmt.Println("  - TTL del cache: 5 minutos. Se renueva en cada hit.")
	fmt.Println("  - Solo el bloque estático se cachea; el bloque dinámico paga costo completo.")
}

// ─── 7. Main ──────────────────────────────────────────────────────────────────

func main() {
	staticContent := strings.Join([]string{
		bloqueIdentidad, bloqueInstrucciones, bloqueHerramientas, bloqueRestricciones, bloqueEjemplos,
	}, "\n\n")
	fmt.Printf("Bloque estático: %d chars (~%d tokens estimados)\n",
		len(staticContent), len(staticContent)/4)

	fmt.Printf("\n%s\n", strings.Repeat("═", 68))
	fmt.Println("  BATCH CON CACHING (10 requests)")
	fmt.Printf("%s\n", strings.Repeat("═", 68))
	cachedResults, err := runBatchCached(questions)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error batch cached: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("\n%s\n", strings.Repeat("═", 68))
	fmt.Println("  BATCH SIN CACHING (10 requests) — para comparación")
	fmt.Printf("%s\n", strings.Repeat("═", 68))
	noCacheResults, err := runBatchNoCache(questions)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error batch no-cache: %v\n", err)
		os.Exit(1)
	}

	analyzeSavingsGo(cachedResults, noCacheResults)
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
