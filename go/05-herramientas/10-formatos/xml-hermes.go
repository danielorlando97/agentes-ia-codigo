// Formato XML estilo Hermes (NousResearch) para tool calling.
//
// Los tags <tool_call> / </tool_call> son tokens únicos en el vocabulario
// del modelo Hermes — el parser detecta límites O(1) por token, no O(n).
// El output NO es XML real: se parsea con regex, no con un parser XML.
//
// Aquí se instruye a Claude a responder en formato Hermes para demostrar
// el parser. En producción se usaría Hermes 2 Pro o Hermes 3 (Llama 3.1).

// Cómo ejecutar: make go FILE=go/05-herramientas/10-formatos/xml-hermes.go

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

var modelHermes = envOr("SMALL_MODEL", "claude-haiku-4-5-20251001")

// --- Tipos ---

type HermesMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type HermesResp struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

type ToolCallH struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments"`
}

// --- Definición de tools en formato Hermes ---

var toolsHermes = []map[string]interface{}{
	{
		"type": "function",
		"function": map[string]interface{}{
			"name": "get_weather",
			"description": "Get current weather for a city. " +
				"Use when the user asks about weather conditions or temperature.",
			"parameters": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"location": map[string]interface{}{"type": "string", "description": "City and country, e.g. 'Madrid, Spain'"},
					"unit":     map[string]interface{}{"type": "string", "enum": []string{"celsius", "fahrenheit"}, "description": "Temperature unit. Default: celsius."},
				},
				"required": []string{"location"},
			},
		},
	},
	{
		"type": "function",
		"function": map[string]interface{}{
			"name":        "get_time",
			"description": "Get current local time for a given timezone.",
			"parameters": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"timezone": map[string]interface{}{"type": "string", "description": "IANA timezone string, e.g. 'Europe/Madrid'"},
				},
				"required": []string{"timezone"},
			},
		},
	},
}

func buildSystemHermes() string {
	toolsJSON, _ := json.MarshalIndent(toolsHermes, "", "  ")
	return fmt.Sprintf(`Eres un asistente con acceso a herramientas.

<tools>
%s
</tools>

Cuando necesites usar una herramienta, responde con este formato exacto:

<tool_call>
{"name": "<nombre_herramienta>", "arguments": {<argumentos en JSON>}}
</tool_call>

Puedes emitir múltiples <tool_call> para llamadas paralelas. El output dentro del tag
no es XML real — el sistema lo parsea con regex, no con un parser XML.
Después de recibir los resultados en <tool_response>, responde al usuario.`, string(toolsJSON))
}

// --- Parser de <tool_call> por regex ---

var toolCallRe = regexp.MustCompile(`(?s)<tool_call>\s*(\{.*?\})\s*</tool_call>`)

func extraerToolCalls(respuesta string) []ToolCallH {
	matches := toolCallRe.FindAllStringSubmatch(respuesta, -1)
	var resultado []ToolCallH
	for _, m := range matches {
		var tc ToolCallH
		if err := json.Unmarshal([]byte(m[1]), &tc); err != nil {
			continue // JSON malformado — en producción usar json_repair
		}
		if tc.Arguments == nil {
			tc.Arguments = map[string]interface{}{}
		}
		resultado = append(resultado, tc)
	}
	return resultado
}

// --- Mock de ejecución ---

func ejecutarHerramientaH(nombre string, args map[string]interface{}) map[string]interface{} {
	switch nombre {
	case "get_weather":
		unit := "celsius"
		if u, ok := args["unit"].(string); ok {
			unit = u
		}
		return map[string]interface{}{
			"location":    args["location"],
			"temperature": 22,
			"unit":        unit,
			"conditions":  "parcialmente nublado",
		}
	case "get_time":
		return map[string]interface{}{"timezone": args["timezone"], "local_time": "14:35:00"}
	}
	return map[string]interface{}{"error": fmt.Sprintf("herramienta desconocida: %s", nombre)}
}

func formatearToolResponse(nombre string, resultado map[string]interface{}) string {
	content, _ := json.Marshal(map[string]interface{}{"name": nombre, "content": resultado})
	return fmt.Sprintf("<tool_response>\n%s\n</tool_response>", string(content))
}

// --- Llamada API ---

func llamarAnthropicHermes(system string, msgs []HermesMsg) (string, error) {
	body, _ := json.Marshal(map[string]interface{}{
		"model":      modelHermes,
		"max_tokens": 1024,
		"system":     system,
		"messages":   msgs,
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
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("API error %d: %s", resp.StatusCode, respBody)
	}
	var result HermesResp
	json.Unmarshal(respBody, &result)
	if len(result.Content) > 0 {
		return strings.TrimSpace(result.Content[0].Text), nil
	}
	return "", fmt.Errorf("respuesta vacía")
}

// --- Loop de tool use con formato Hermes ---

func hermesLoop(pregunta string) (string, error) {
	system := buildSystemHermes()
	historial := []HermesMsg{{Role: "user", Content: pregunta}}

	for paso := 0; paso < 10; paso++ {
		texto, err := llamarAnthropicHermes(system, historial)
		if err != nil {
			return "", err
		}
		historial = append(historial, HermesMsg{Role: "assistant", Content: texto})

		toolCalls := extraerToolCalls(texto)
		if len(toolCalls) == 0 {
			return texto, nil // Respuesta final sin tool calls
		}

		// Ejecutar todas las tool calls (pueden ser paralelas en Hermes)
		var responsesXml []string
		for _, tc := range toolCalls {
			resultado := ejecutarHerramientaH(tc.Name, tc.Arguments)
			argsJSON, _ := json.Marshal(tc.Arguments)
			resJSON, _ := json.Marshal(resultado)
			display := string(resJSON)
			if len(display) > 60 {
				display = display[:60]
			}
			fmt.Printf("  → %s(%s) = %s\n", tc.Name, string(argsJSON), display)
			responsesXml = append(responsesXml, formatearToolResponse(tc.Name, resultado))
		}

		historial = append(historial, HermesMsg{Role: "user", Content: strings.Join(responsesXml, "\n")})
	}
	return "[límite de pasos alcanzado]", nil
}

func main() {
	fmt.Println("=== Formato XML Hermes (NousResearch style) ===")
	fmt.Println("Parser de <tool_call> por regex — no es XML real\n")

	// Demo del parser con respuesta simulada
	respuestaSimulada := `Voy a consultar el tiempo y la hora simultáneamente.
<tool_call>
{"name": "get_weather", "arguments": {"location": "Madrid, Spain", "unit": "celsius"}}
</tool_call><tool_call>
{"name": "get_time", "arguments": {"timezone": "Europe/Madrid"}}
</tool_call>`

	fmt.Println("Respuesta simulada del modelo:")
	fmt.Println(respuestaSimulada)
	calls := extraerToolCalls(respuestaSimulada)
	callsJSON, _ := json.MarshalIndent(calls, "", "  ")
	fmt.Printf("\nTool calls extraídas por regex: %s\n\n", string(callsJSON))

	// Loop completo con el modelo
	fmt.Println(strings.Repeat("=", 60))
	fmt.Println("Pregunta: ¿Qué tiempo hace en Tokio?")
	respuesta, err := hermesLoop("¿Qué tiempo hace en Tokio?")
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
	// Eliminar los tags XML para mostrar la respuesta limpia
	respuestaLimpia := toolCallRe.ReplaceAllString(respuesta, "")
	fmt.Printf("Respuesta final: %s\n", strings.TrimSpace(respuestaLimpia))
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
