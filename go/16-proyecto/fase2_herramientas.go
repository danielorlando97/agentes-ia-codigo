// Fase 2: añade las 4 herramientas al loop ReAct de la Fase 1.
// El modelo decide cuándo usarlas; el código ejecuta y devuelve resultados.

// Cómo ejecutar: make go FILE=go/16-proyecto/fase2_herramientas.go

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)
var (
	model  = envOr("MODEL", "claude-sonnet-4-6")
	apiURL = envBaseURL()
)

const systemPromptFase2 = `Eres un agente de revisión de código Python.
Analiza el código con cuidado, usa las herramientas disponibles para verificar comportamiento
cuando sea útil, y produce una revisión técnica estructurada.

Cuando tengas suficiente información, emite el resultado final como JSON con este schema exacto:
{
  "hallazgos": [
    {
      "linea": <número o null>,
      "severidad": "<critical|high|medium|low>",
      "tipo": "<bug|estilo|rendimiento|seguridad>",
      "descripcion": "<descripción concisa del hallazgo>",
      "sugerencia": "<cómo corregirlo>"
    }
  ],
  "resumen": "<párrafo de resumen>"
}`

// ---- Tipos de la API de Anthropic ----

type Fase2Request struct {
	Model     string      `json:"model"`
	MaxTokens int         `json:"max_tokens"`
	System    string      `json:"system"`
	Tools     []Fase2Tool `json:"tools"`
	Messages  []Fase2Msg  `json:"messages"`
}

type Fase2Tool struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema interface{} `json:"input_schema"`
}

type Fase2Msg struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"`
}

type Fase2ContentBlock struct {
	Type  string          `json:"type"`
	Text  string          `json:"text,omitempty"`
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
}

type Fase2ToolResult struct {
	Type      string `json:"type"`
	ToolUseID string `json:"tool_use_id"`
	Content   string `json:"content"`
}

type Fase2Response struct {
	Content    []Fase2ContentBlock `json:"content"`
	StopReason string              `json:"stop_reason"`
}

// ---- Herramientas ----

var herramientasFase2 = []Fase2Tool{
	{
		Name:        "read_file",
		Description: "Lee el contenido de un archivo del proyecto",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"path": map[string]interface{}{"type": "string", "description": "Ruta relativa al directorio del proyecto"},
			},
			"required": []string{"path"},
		},
	},
	{
		Name:        "run_code",
		Description: "Ejecuta un fragmento de código Python y devuelve stdout/stderr",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"code":    map[string]interface{}{"type": "string"},
				"timeout": map[string]interface{}{"type": "integer", "default": 10},
			},
			"required": []string{"code"},
		},
	},
	{
		Name:        "search_docs",
		Description: "Busca en la documentación técnica interna del equipo",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"query": map[string]interface{}{"type": "string"},
			},
			"required": []string{"query"},
		},
	},
	{
		Name:        "write_report",
		Description: "Escribe el informe final de revisión",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"content":  map[string]interface{}{"type": "string"},
				"filename": map[string]interface{}{"type": "string"},
			},
			"required": []string{"content", "filename"},
		},
	},
}

func ejecutarHerramientaFase2(nombre string, paramsRaw json.RawMessage, proyectoDir string) string {
	var params map[string]interface{}
	json.Unmarshal(paramsRaw, &params)

	switch nombre {
	case "read_file":
		rutaRel, _ := params["path"].(string)
		rutaAbs, err := filepath.Abs(filepath.Join(proyectoDir, rutaRel))
		if err != nil || !strings.HasPrefix(rutaAbs, filepath.Clean(proyectoDir)+string(filepath.Separator)) {
			if rutaAbs != filepath.Clean(proyectoDir) {
				return "Error: ruta fuera del directorio del proyecto"
			}
		}
		contenido, err := os.ReadFile(rutaAbs)
		if err != nil {
			return fmt.Sprintf("Error: archivo '%s' no encontrado", rutaRel)
		}
		return string(contenido)

	case "run_code":
		codigo, _ := params["code"].(string)
		timeoutSec := 10
		if t, ok := params["timeout"].(float64); ok {
			timeoutSec = int(t)
		}
		tmpdir, _ := os.MkdirTemp("", "agente-")
		defer os.RemoveAll(tmpdir)
		script := filepath.Join(tmpdir, "script.py")
		os.WriteFile(script, []byte(codigo), 0644)

		cmd := exec.Command("python3", script)
		cmd.Dir = tmpdir
		cmd.Env = []string{"PATH=/usr/local/bin:/usr/bin:/bin", "HOME=" + tmpdir}

		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr

		done := make(chan error, 1)
		if err := cmd.Start(); err != nil {
			return fmt.Sprintf("Error al iniciar proceso: %v", err)
		}
		go func() { done <- cmd.Wait() }()

		select {
		case <-time.After(time.Duration(timeoutSec) * time.Second):
			cmd.Process.Kill()
			return "Error: timeout de ejecución"
		case <-done:
		}

		output := strings.TrimSpace(stdout.String())
		if errStr := strings.TrimSpace(stderr.String()); errStr != "" {
			output += "\nSTDERR: " + errStr
		}
		if output == "" {
			return "(sin output)"
		}
		return output

	case "search_docs":
		query, _ := params["query"].(string)
		return fmt.Sprintf("[Documentación para '%s': ver estándares del equipo en /docs/]", query)

	case "write_report":
		content, _ := params["content"].(string)
		filename, _ := params["filename"].(string)
		reportsDir := filepath.Join(proyectoDir, "reports")
		os.MkdirAll(reportsDir, 0755)
		ruta := filepath.Join(reportsDir, filename)
		os.WriteFile(ruta, []byte(content), 0644)
		return fmt.Sprintf("Informe escrito en %s", ruta)
	}

	return fmt.Sprintf("Error: herramienta '%s' desconocida", nombre)
}

// ---- Llamada a la API ----

func llamarAnthropicFase2(req Fase2Request) (*Fase2Response, error) {
	body, _ := json.Marshal(req)
	httpReq, _ := http.NewRequest("POST", apiURL, bytes.NewReader(body))
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", os.Getenv("ANTHROPIC_API_KEY"))
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, respBody)
	}

	var result Fase2Response
	json.Unmarshal(respBody, &result)
	return &result, nil
}

// ---- Extracción de JSON ----

func extraerJSONFase2(texto string) (map[string]interface{}, error) {
	inicio := strings.Index(texto, "{")
	fin := strings.LastIndex(texto, "}") + 1
	if inicio == -1 || fin == 0 {
		maxLen := 300
		if len(texto) < maxLen {
			maxLen = len(texto)
		}
		return nil, fmt.Errorf("no se encontró JSON en output: %s", texto[:maxLen])
	}
	var result map[string]interface{}
	err := json.Unmarshal([]byte(texto[inicio:fin]), &result)
	return result, err
}

// ---- Loop ReAct ----

func agenteRevisionFase2(codigo, proyectoDir string) (map[string]interface{}, error) {
	mensajes := []Fase2Msg{
		{Role: "user", Content: fmt.Sprintf("Revisa este código:\n\n```python\n%s\n```", codigo)},
	}

	const maxPasos = 15

	for paso := 0; paso < maxPasos; paso++ {
		req := Fase2Request{
			Model:     model,
			MaxTokens: 4096,
			System:    systemPromptFase2,
			Tools:     herramientasFase2,
			Messages:  mensajes,
		}

		resp, err := llamarAnthropicFase2(req)
		if err != nil {
			return nil, err
		}

		if resp.StopReason == "end_turn" {
			for _, bloque := range resp.Content {
				if bloque.Type == "text" {
					return extraerJSONFase2(bloque.Text)
				}
			}
			return nil, fmt.Errorf("respuesta sin bloque de texto")
		}

		if resp.StopReason == "tool_use" {
			mensajes = append(mensajes, Fase2Msg{Role: "assistant", Content: resp.Content})

			var resultados []Fase2ToolResult
			for _, bloque := range resp.Content {
				if bloque.Type == "tool_use" {
					resultado := ejecutarHerramientaFase2(bloque.Name, bloque.Input, proyectoDir)
					resultados = append(resultados, Fase2ToolResult{
						Type:      "tool_result",
						ToolUseID: bloque.ID,
						Content:   resultado,
					})
				}
			}
			mensajes = append(mensajes, Fase2Msg{Role: "user", Content: resultados})
		}
	}

	return nil, fmt.Errorf("el agente no terminó en %d pasos", maxPasos)
}

func main() {
	var codigo, proyectoDir string

	if len(os.Args) > 1 {
		contenido, err := os.ReadFile(os.Args[1])
		if err != nil {
			fmt.Fprintln(os.Stderr, "Error al leer archivo:", err)
			os.Exit(1)
		}
		codigo = string(contenido)
		proyectoDir = "."
		if len(os.Args) >= 3 {
			proyectoDir = os.Args[2]
		}
	} else {
		codigo = `
def divide(a, b):
    return a / b  # ZeroDivisionError no manejado
`
		proyectoDir = "."
	}

	resultado, err := agenteRevisionFase2(codigo, proyectoDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}

	output, _ := json.MarshalIndent(resultado, "", "  ")
	fmt.Println(string(output))
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
