// Router en cascada: keyword → Jaccard → LLM.
//
// Tres mecanismos ordenados por costo creciente. Solo sube a la siguiente
// capa si la actual no produce match.
//
// Requiere: ANTHROPIC_API_KEY en el entorno.

// Cómo ejecutar: make go FILE=go/10-decisiones/routing.go

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

const (
	jaccardThreshold = 0.15
)

var (
	modelRouting = envOr("MODEL", "claude-sonnet-4-6")
	apiURLRouting = envBaseURL()
)

var jsonRe = regexp.MustCompile(`\{[\s\S]*\}`)

// --- Tipos ---

type Route struct {
	Name        string
	Description string
	Keywords    []string
	Examples    []string
}

var defaultRoute = Route{
	Name:        "DEFAULT",
	Description: "Ruta de fallback para inputs que no encajan en ninguna especialización",
}

// --- Jaccard ---

func jaccardScore(a, b string) float64 {
	wordsA := wordSet(a)
	wordsB := wordSet(b)

	union := make(map[string]struct{})
	for w := range wordsA {
		union[w] = struct{}{}
	}
	for w := range wordsB {
		union[w] = struct{}{}
	}
	if len(union) == 0 {
		return 0
	}

	inter := 0
	for w := range wordsA {
		if _, ok := wordsB[w]; ok {
			inter++
		}
	}
	return float64(inter) / float64(len(union))
}

func wordSet(text string) map[string]struct{} {
	set := make(map[string]struct{})
	for _, w := range strings.Fields(strings.ToLower(text)) {
		set[w] = struct{}{}
	}
	return set
}

// --- Capas del router ---

func routerKeyword(input string, routes []Route) *Route {
	lower := strings.ToLower(input)
	for i := range routes {
		for _, kw := range routes[i].Keywords {
			if strings.Contains(lower, strings.ToLower(kw)) {
				return &routes[i]
			}
		}
	}
	return nil
}

func routerJaccard(input string, routes []Route) *Route {
	bestIdx := -1
	bestScore := 0.0

	for i, route := range routes {
		score := 0.0
		for _, ex := range route.Examples {
			if s := jaccardScore(input, ex); s > score {
				score = s
			}
		}
		if score > bestScore {
			bestScore = score
			bestIdx = i
		}
	}

	if bestScore >= jaccardThreshold {
		return &routes[bestIdx]
	}
	return nil
}

// --- API types para LLM router ---

type msgRouting struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type reqRouting struct {
	Model     string       `json:"model"`
	MaxTokens int          `json:"max_tokens"`
	Messages  []msgRouting `json:"messages"`
}

type contentBlockRouting struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type respRouting struct {
	Content []contentBlockRouting `json:"content"`
}

type routerLLMResult struct {
	Destination string `json:"destination"`
	NextInputs  string `json:"next_inputs"`
}

func routerLLM(input string, routes []Route, apiKey string) Route {
	var sb strings.Builder
	for _, r := range routes {
		fmt.Fprintf(&sb, "- %s: %s\n", r.Name, r.Description)
	}

	prompt := fmt.Sprintf(`Clasifica el siguiente input en una de estas rutas:
%s- DEFAULT: ninguna de las anteriores

Input: %s

Responde ÚNICAMENTE con JSON válido:
{"destination": "<nombre_ruta>", "next_inputs": "<input reformulado si aplica, sino igual al original>"}`,
		sb.String(), input)

	body, _ := json.Marshal(reqRouting{
		Model:     modelRouting,
		MaxTokens: 256,
		Messages:  []msgRouting{{Role: "user", Content: prompt}},
	})

	req, _ := http.NewRequest("POST", apiURLRouting, bytes.NewReader(body))
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("content-type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "LLM router HTTP error: %v\n", err)
		return defaultRoute
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	var r respRouting
	if err := json.Unmarshal(raw, &r); err != nil || len(r.Content) == 0 {
		fmt.Fprintf(os.Stderr, "LLM router parse error: %s\n", raw)
		return defaultRoute
	}

	text := r.Content[0].Text
	m := jsonRe.FindString(text)
	if m == "" {
		return defaultRoute
	}

	var parsed routerLLMResult
	if err := json.Unmarshal([]byte(m), &parsed); err != nil {
		return defaultRoute
	}

	routeMap := make(map[string]Route, len(routes))
	for _, route := range routes {
		routeMap[route.Name] = route
	}
	if found, ok := routeMap[parsed.Destination]; ok {
		return found
	}
	return defaultRoute
}

// --- Router en cascada ---

func cascadeRouter(input string, routes []Route, apiKey string) (Route, string) {
	if r := routerKeyword(input, routes); r != nil {
		return *r, "keyword"
	}
	if r := routerJaccard(input, routes); r != nil {
		return *r, "jaccard"
	}
	return routerLLM(input, routes, apiKey), "llm"
}

// --- Demo ---

var routes = []Route{
	{
		Name:        "soporte_tecnico",
		Description: "Problemas técnicos con el producto, errores, bugs, configuración",
		Keywords:    []string{"error", "falla", "bug", "no funciona", "excepción", "crash"},
		Examples: []string{
			"el endpoint de autenticación devuelve 500",
			"no puedo conectarme a la API",
			"el SDK lanza una excepción al inicializar",
			"la integración de webhook falla con timeout",
		},
	},
	{
		Name:        "facturacion",
		Description: "Preguntas sobre pagos, facturas, planes, precios, suscripciones",
		Keywords:    []string{"factura", "pago", "precio", "suscripción", "plan", "cobro"},
		Examples: []string{
			"quiero cambiar mi plan de facturación mensual a anual",
			"no me llegó la factura de este mes",
			"cómo cancelo mi suscripción",
			"cuánto cuesta el plan enterprise",
		},
	},
	{
		Name:        "general",
		Description: "Preguntas generales sobre la empresa, el producto, horarios, contacto",
		Keywords:    []string{"horario", "contacto", "email", "teléfono", "dirección"},
		Examples: []string{
			"cuál es el horario de atención al cliente",
			"cómo puedo contactar con soporte por correo",
			"dónde están ubicadas las oficinas",
		},
	},
}

func main() {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "ANTHROPIC_API_KEY no está definido")
		os.Exit(1)
	}

	queries := []string{
		"Tengo un bug en el SDK que hace crash la app al iniciar",
		"necesito cambiar cómo me cobran cada mes al plan anual",
		"¿cuánto tiempo lleva aproximadamente resolver una disputa de cargo?",
	}

	fmt.Println("=== Router en cascada ===\n")
	for _, query := range queries {
		route, mechanism := cascadeRouter(query, routes, apiKey)
		fmt.Printf("Input    : %s\n", query)
		fmt.Printf("Ruta     : %s\n", route.Name)
		fmt.Printf("Mecanismo: %s\n\n", mechanism)
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
