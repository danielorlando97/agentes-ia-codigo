// Extracción de datos estructurados de texto libre usando tres métodos.
//
// Demuestra:
// - Método 1: instrucción libre — "devuelve JSON con campos X, Y, Z"
// - Método 2: JSON schema en el prompt — schema explícito con tipos
// - Método 3: tool_use forzado — constrained decoding via herramienta
// - Métricas: tasa de fallo de parsing, precisión de extracción, tokens consumidos
// Sin SDK: HTTP directo contra la API de Anthropic.

// Cómo ejecutar: make go FILE=go/04-prompts/structured-output.go

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"regexp"
	"strings"
)

var (
	soModel = envOr("MODEL", "claude-sonnet-4-6")
	soAPIURL = envBaseURL()
)

// ─── 1. Tipos ─────────────────────────────────────────────────────────────────

type reviewInput struct {
	Text     string
	Expected reviewExpected
}

type reviewExpected struct {
	NombreProducto  string
	Precio          float64
	Rating          float64
	AspectoPositivo *string
	AspectoNegativo *string
}

type extractionResult struct {
	Method       string
	RawOutput    string
	Data         map[string]interface{}
	ParseOk      bool
	Error        string
	TokensInput  int
	TokensOutput int
}

// ─── 2. Datos de entrada ──────────────────────────────────────────────────────

func strPtr(s string) *string { return &s }

var soReviews = []reviewInput{
	{
		Text: "Compré el 'Altavoz Bluetooth Pro X200' por 79.99€ el mes pasado. La calidad de sonido es impresionante para su precio. Le doy 4.5 sobre 5 estrellas. El único problema es que la batería dura solo 6 horas, menos de lo prometido.",
		Expected: reviewExpected{"Altavoz Bluetooth Pro X200", 79.99, 4.5, strPtr("calidad de sonido"), strPtr("batería")},
	},
	{
		Text: "El 'Ratón Ergonómico ErgoMaster 3000' cuesta 149€ y es una maravilla. Llevo 3 meses usándolo sin ningún problema de muñeca. Puntuación: 5/5. No tiene ningún defecto reseñable.",
		Expected: reviewExpected{"Ratón Ergonómico ErgoMaster 3000", 149.0, 5.0, strPtr("sin problemas de muñeca"), nil},
	},
	{
		Text: "El Teclado Mecánico TechType K85 que compré por 89 euros es un fiasco total. Teclas que se atascan, ruido excesivo y el software no funciona en Mac. No doy más de 1.5 de 5. Muy decepcionado.",
		Expected: reviewExpected{"Teclado Mecánico TechType K85", 89.0, 1.5, nil, strPtr("teclas, ruido, software")},
	},
}

// ─── 3. Llamada genérica a la API ─────────────────────────────────────────────

func callSOAPI(payload interface{}) ([]byte, error) {
	body, _ := json.Marshal(payload)
	req, _ := http.NewRequestWithContext(context.Background(), "POST", soAPIURL, bytes.NewReader(body))
	req.Header.Set("x-api-key", os.Getenv("ANTHROPIC_API_KEY"))
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("content-type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

// ─── 4. Método 1: Instrucción libre ───────────────────────────────────────────

const systemFree = `Extrae los datos de la siguiente reseña de producto.
Devuelve SOLO JSON válido con estos campos exactos:
{
  "nombre_producto": "nombre del producto",
  "precio": <número con decimales>,
  "rating": <número entre 1 y 5>,
  "aspecto_positivo": "descripción o null",
  "aspecto_negativo": "descripción o null"
}
Sin texto antes ni después del JSON.`

func extractFreeInstruction(text string) (extractionResult, error) {
	payload := map[string]interface{}{
		"model":      soModel,
		"max_tokens": 300,
		"system":     systemFree,
		"messages":   []map[string]string{{"role": "user", "content": text}},
	}

	data, err := callSOAPI(payload)
	if err != nil {
		return extractionResult{}, err
	}

	var resp struct {
		Content []struct{ Text string } `json:"content"`
		Usage   struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	json.Unmarshal(data, &resp)

	output := ""
	if len(resp.Content) > 0 {
		output = strings.TrimSpace(resp.Content[0].Text)
	}

	var parsed map[string]interface{}
	parseErr := json.Unmarshal([]byte(output), &parsed)
	errStr := ""
	if parseErr != nil {
		errStr = parseErr.Error()
	}

	return extractionResult{
		Method:       "1-instruccion-libre",
		RawOutput:    output,
		Data:         parsed,
		ParseOk:      parseErr == nil,
		Error:        errStr,
		TokensInput:  resp.Usage.InputTokens,
		TokensOutput: resp.Usage.OutputTokens,
	}, nil
}

// ─── 5. Método 2: JSON schema en el prompt ────────────────────────────────────

const systemSchema = `Extrae los datos de la reseña de producto. El output debe ser JSON válido que siga este schema:

` + "```" + `json-schema
{
  "type": "object",
  "required": ["nombre_producto", "precio", "rating"],
  "properties": {
    "nombre_producto": { "type": "string", "description": "Nombre exacto del producto" },
    "precio": { "type": "number", "description": "Precio en euros" },
    "rating": { "type": "number", "description": "Puntuación del 1.0 al 5.0" },
    "aspecto_positivo": { "type": ["string", "null"], "description": "Principal aspecto positivo o null" },
    "aspecto_negativo": { "type": ["string", "null"], "description": "Principal aspecto negativo o null" }
  }
}
` + "```" + `

Responde SOLO con el JSON.`

var backtickPattern = regexp.MustCompile("(?s)^```(?:json)?\\n?|\\n?```$")

func extractWithSchema(text string) (extractionResult, error) {
	payload := map[string]interface{}{
		"model":      soModel,
		"max_tokens": 300,
		"system":     systemSchema,
		"messages":   []map[string]string{{"role": "user", "content": text}},
	}

	data, err := callSOAPI(payload)
	if err != nil {
		return extractionResult{}, err
	}

	var resp struct {
		Content []struct{ Text string } `json:"content"`
		Usage   struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	json.Unmarshal(data, &resp)

	output := ""
	if len(resp.Content) > 0 {
		output = strings.TrimSpace(resp.Content[0].Text)
	}
	clean := strings.TrimSpace(backtickPattern.ReplaceAllString(output, ""))

	var parsed map[string]interface{}
	parseErr := json.Unmarshal([]byte(clean), &parsed)
	errStr := ""
	if parseErr != nil {
		errStr = parseErr.Error()
	}

	return extractionResult{
		Method:       "2-json-schema-prompt",
		RawOutput:    output,
		Data:         parsed,
		ParseOk:      parseErr == nil,
		Error:        errStr,
		TokensInput:  resp.Usage.InputTokens,
		TokensOutput: resp.Usage.OutputTokens,
	}, nil
}

// ─── 6. Método 3: tool_use forzado ────────────────────────────────────────────

func extractWithTool(text string) (extractionResult, error) {
	toolDef := map[string]interface{}{
		"name":        "guardar_reseña",
		"description": "Guarda los datos estructurados extraídos de la reseña",
		"input_schema": map[string]interface{}{
			"type":     "object",
			"required": []string{"nombre_producto", "precio", "rating"},
			"properties": map[string]interface{}{
				"nombre_producto": map[string]string{"type": "string", "description": "Nombre exacto del producto"},
				"precio":          map[string]string{"type": "number", "description": "Precio en euros"},
				"rating":          map[string]string{"type": "number", "description": "Puntuación del 1.0 al 5.0"},
				"aspecto_positivo": map[string]string{"type": "string", "description": "Principal aspecto positivo"},
				"aspecto_negativo": map[string]string{"type": "string", "description": "Principal aspecto negativo"},
			},
		},
	}

	payload := map[string]interface{}{
		"model":       soModel,
		"max_tokens":  300,
		"tools":       []interface{}{toolDef},
		"tool_choice": map[string]string{"type": "tool", "name": "guardar_reseña"},
		"messages":    []map[string]string{{"role": "user", "content": "Extrae los datos de esta reseña:\n\n" + text}},
	}

	data, err := callSOAPI(payload)
	if err != nil {
		return extractionResult{}, err
	}

	var resp struct {
		Content []struct {
			Type  string                 `json:"type"`
			ID    string                 `json:"id"`
			Name  string                 `json:"name"`
			Input map[string]interface{} `json:"input"`
		} `json:"content"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	json.Unmarshal(data, &resp)

	for _, block := range resp.Content {
		if block.Type == "tool_use" {
			// Normalizar strings vacíos
			for _, field := range []string{"aspecto_positivo", "aspecto_negativo"} {
				if v, ok := block.Input[field]; ok {
					if s, ok := v.(string); ok && s == "" {
						block.Input[field] = nil
					}
				}
			}
			raw, _ := json.Marshal(block.Input)
			return extractionResult{
				Method:       "3-tool-use",
				RawOutput:    string(raw),
				Data:         block.Input,
				ParseOk:      true,
				TokensInput:  resp.Usage.InputTokens,
				TokensOutput: resp.Usage.OutputTokens,
			}, nil
		}
	}

	return extractionResult{
		Method:  "3-tool-use",
		ParseOk: false,
		Error:   "No se recibió tool_use block",
	}, nil
}

// ─── 7. Evaluación de precisión ───────────────────────────────────────────────

type evalResult struct {
	FieldChecks   map[string]bool
	CorrectFields int
	TotalFields   int
}

func evaluateExtraction(data map[string]interface{}, expected reviewExpected) evalResult {
	checks := map[string]bool{}

	expName := strings.ToLower(expected.NombreProducto)
	gotName := strings.ToLower(fmt.Sprintf("%v", data["nombre_producto"]))
	prefix := expName
	if len(prefix) > 15 {
		prefix = prefix[:15]
	}
	checks["nombre_producto"] = strings.Contains(gotName, prefix)

	gotPrice := 0.0
	if p, ok := data["precio"].(float64); ok {
		gotPrice = p
	}
	checks["precio"] = math.Abs(gotPrice-expected.Precio) <= 0.5

	gotRating := 0.0
	if r, ok := data["rating"].(float64); ok {
		gotRating = r
	}
	checks["rating"] = math.Abs(gotRating-expected.Rating) <= 0.5

	correct := 0
	for _, v := range checks {
		if v {
			correct++
		}
	}
	return evalResult{FieldChecks: checks, CorrectFields: correct, TotalFields: len(checks)}
}

// ─── 8. Impresión de resultados ───────────────────────────────────────────────

func printSOReviewComparison(rev reviewInput, results []extractionResult) {
	fmt.Printf("\n%s\n", strings.Repeat("═", 74))
	text := rev.Text
	if len(text) > 80 {
		text = text[:80] + "..."
	}
	fmt.Printf("  RESEÑA: %s\n", text)
	fmt.Printf("%s\n", strings.Repeat("─", 74))

	for _, r := range results {
		fmt.Printf("\n  [%s]\n", r.Method)
		parseStr := "✓"
		if !r.ParseOk {
			parseStr = "✗"
		}
		fmt.Printf("  Parsing: %s\n", parseStr)
		if r.Error != "" {
			errPreview := r.Error
			if len(errPreview) > 80 {
				errPreview = errPreview[:80]
			}
			fmt.Printf("  Error: %s\n", errPreview)
		}
		if len(r.Data) > 0 {
			ev := evaluateExtraction(r.Data, rev.Expected)
			var fieldParts []string
			for k, v := range ev.FieldChecks {
				sym := "✓"
				if !v {
					sym = "✗"
				}
				fieldParts = append(fieldParts, fmt.Sprintf("%s: %s", k, sym))
			}
			fmt.Printf("  Campos correctos: %d/%d\n", ev.CorrectFields, ev.TotalFields)
			fmt.Printf("  %s\n", strings.Join(fieldParts, "  "))
			nombre := fmt.Sprintf("%v", r.Data["nombre_producto"])
			if len(nombre) > 30 {
				nombre = nombre[:30]
			}
			fmt.Printf("  Extraído: nombre=%s, precio=%v, rating=%v\n",
				nombre, r.Data["precio"], r.Data["rating"])
		}
		fmt.Printf("  Tokens: %d input / %d output\n", r.TokensInput, r.TokensOutput)
	}
}

func printSOSummary(allResults [][]extractionResult, reviews []reviewInput) {
	type stat struct {
		parseOk       int
		correctFields int
		tokensIn      int
	}

	methodNames := make([]string, len(allResults[0]))
	for i, r := range allResults[0] {
		methodNames[i] = r.Method
	}
	stats := make(map[string]*stat)
	for _, m := range methodNames {
		stats[m] = &stat{}
	}

	n := float64(len(reviews))
	nFields := 3.0

	for i, revResults := range allResults {
		for _, r := range revResults {
			if r.ParseOk {
				stats[r.Method].parseOk++
			}
			if len(r.Data) > 0 {
				ev := evaluateExtraction(r.Data, reviews[i].Expected)
				stats[r.Method].correctFields += ev.CorrectFields
			}
			stats[r.Method].tokensIn += r.TokensInput
		}
	}

	fmt.Printf("\n%s\n", strings.Repeat("═", 74))
	fmt.Println("  TABLA COMPARATIVA FINAL")
	fmt.Printf("%s\n", strings.Repeat("═", 74))
	fmt.Printf("  %-28s %9s %11s %10s\n", "Método", "Parse OK", "Precisión", "Tokens/in")
	fmt.Printf("  %s\n", strings.Repeat("-", 60))
	for _, m := range methodNames {
		s := stats[m]
		fmt.Printf("  %-28s %8.0f%% %10.0f%% %9.0f\n",
			m,
			float64(s.parseOk)/n*100,
			float64(s.correctFields)/(n*nFields)*100,
			float64(s.tokensIn)/n)
	}
	fmt.Println("\n  El Método 3 garantiza estructura válida via tool_use forzado.")
	fmt.Println("  Si los tres métodos tienen alta precisión, la instrucción libre es suficiente.")
}

// ─── 9. Main ──────────────────────────────────────────────────────────────────

func main() {
	var allResults [][]extractionResult

	for _, rev := range soReviews {
		r1, err := extractFreeInstruction(rev.Text)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		r2, err := extractWithSchema(rev.Text)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		r3, err := extractWithTool(rev.Text)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		results := []extractionResult{r1, r2, r3}
		printSOReviewComparison(rev, results)
		allResults = append(allResults, results)
	}

	printSOSummary(allResults, soReviews)
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
