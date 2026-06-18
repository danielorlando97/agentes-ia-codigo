// Pipeline del agente de revisión de código — equivalente lógico al de Python y TypeScript.

// Cómo ejecutar: make go FILE=go/16-proyecto/agente_completo.go

package main

import (
	"bytes"
	"crypto/sha256"
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

const systemPrompt = `Eres un agente de revisión de código Python.
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

type AnthropicRequest struct {
	Model     string      `json:"model"`
	MaxTokens int         `json:"max_tokens"`
	System    string      `json:"system"`
	Tools     []Tool      `json:"tools"`
	Messages  []Message   `json:"messages"`
}

type Tool struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema interface{} `json:"input_schema"`
}

type Message struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"`
}

type ContentBlock struct {
	Type  string          `json:"type"`
	Text  string          `json:"text,omitempty"`
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
}

type ToolResult struct {
	Type      string `json:"type"`
	ToolUseID string `json:"tool_use_id"`
	Content   string `json:"content"`
}

type AnthropicResponse struct {
	Content    []ContentBlock `json:"content"`
	StopReason string         `json:"stop_reason"`
	Usage      struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

// ---- Herramientas ----

var herramientas = []Tool{
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
		Description: "Ejecuta un fragmento de código Python en sandbox y devuelve stdout/stderr",
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
		Description: "Busca en la documentación técnica del equipo",
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
		Description: "Escribe el informe final de revisión en disco",
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

func ejecutarHerramienta(nombre string, paramsRaw json.RawMessage, proyectoDir string) string {
	var params map[string]interface{}
	json.Unmarshal(paramsRaw, &params)

	switch nombre {
	case "read_file":
		rutaRel, _ := params["path"].(string)
		rutaAbs, err := filepath.Abs(filepath.Join(proyectoDir, rutaRel))
		if err != nil || !strings.HasPrefix(rutaAbs, filepath.Clean(proyectoDir)) {
			return "Error: ruta fuera del directorio del proyecto"
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
		cmd.Start()
		go func() { done <- cmd.Wait() }()

		select {
		case <-time.After(time.Duration(timeoutSec) * time.Second):
			cmd.Process.Kill()
			return "Error: timeout de ejecución"
		case <-done:
		}

		result := map[string]string{
			"stdout": strings.TrimSpace(stdout.String()),
			"stderr": strings.TrimSpace(stderr.String()),
		}
		if result["stdout"] == "" {
			result["stdout"] = "(vacío)"
		}
		j, _ := json.Marshal(result)
		return string(j)

	case "search_docs":
		query, _ := params["query"].(string)
		return fmt.Sprintf("[Documentación para '%s': ver /docs/ del proyecto]", query)

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

func llamarAnthropic(req AnthropicRequest) (*AnthropicResponse, error) {
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

	var result AnthropicResponse
	json.Unmarshal(respBody, &result)
	return &result, nil
}

// ---- Loop ReAct ----

func extraerJSON(texto string) (map[string]interface{}, error) {
	for i, c := range texto {
		if c != '{' {
			continue
		}
		dec := json.NewDecoder(strings.NewReader(texto[i:]))
		var result map[string]interface{}
		if err := dec.Decode(&result); err == nil {
			return result, nil
		}
	}
	return nil, fmt.Errorf("no se encontró JSON en output: %s", texto[:min(300, len(texto))])
}

func loopReact(codigo, proyectoDir string) (map[string]interface{}, error) {
	mensajes := []Message{
		{Role: "user", Content: fmt.Sprintf("Revisa este código:\n\n```python\n%s\n```", codigo)},
	}

	const maxPasos = 15

	for paso := 0; paso < maxPasos; paso++ {
		req := AnthropicRequest{
			Model:     model,
			MaxTokens: 4096,
			System:    systemPrompt,
			Tools:     herramientas,
			Messages:  mensajes,
		}

		resp, err := llamarAnthropic(req)
		if err != nil {
			return nil, err
		}

		if resp.StopReason == "end_turn" {
			for _, bloque := range resp.Content {
				if bloque.Type == "text" {
					return extraerJSON(bloque.Text)
				}
			}
			return nil, fmt.Errorf("respuesta sin bloque de texto")
		}

		if resp.StopReason == "tool_use" {
			// Añadir respuesta del asistente
			mensajes = append(mensajes, Message{Role: "assistant", Content: resp.Content})

			// Ejecutar herramientas y recoger resultados
			var resultados []ToolResult
			for _, bloque := range resp.Content {
				if bloque.Type == "tool_use" {
					resultado := ejecutarHerramienta(bloque.Name, bloque.Input, proyectoDir)
					resultados = append(resultados, ToolResult{
						Type:      "tool_result",
						ToolUseID: bloque.ID,
						Content:   resultado,
					})
				}
			}
			mensajes = append(mensajes, Message{Role: "user", Content: resultados})
		}
	}

	return nil, fmt.Errorf("el agente no terminó en %d pasos", maxPasos)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ---- Memoria episódica ----

type EntradaDB struct {
	Fecha    string                 `json:"fecha"`
	Revision map[string]interface{} `json:"revision"`
}

func cargarDB(dbPath string) map[string]EntradaDB {
	data, err := os.ReadFile(dbPath)
	if err != nil {
		return map[string]EntradaDB{}
	}
	var db map[string]EntradaDB
	json.Unmarshal(data, &db)
	return db
}

func guardarDB(dbPath string, db map[string]EntradaDB) {
	data, _ := json.MarshalIndent(db, "", "  ")
	os.WriteFile(dbPath, data, 0644)
}

func hashCodigo(ruta, codigo string) string {
	h := sha256.Sum256([]byte(ruta + "::" + codigo))
	return fmt.Sprintf("%x", h)
}

// ---- Pipeline principal ----

func revisar(rutaArchivo, proyectoDir string) (map[string]interface{}, error) {
	contenido, err := os.ReadFile(filepath.Join(proyectoDir, rutaArchivo))
	if err != nil {
		return nil, fmt.Errorf("no se pudo leer '%s': %w", rutaArchivo, err)
	}
	codigo := string(contenido)
	hash := hashCodigo(rutaArchivo, codigo)

	dbPath := filepath.Join(proyectoDir, "revisiones.json")
	db := cargarDB(dbPath)

	if entrada, ok := db[hash]; ok {
		fmt.Fprintf(os.Stderr, "[INFO] Revisión cacheada del %s\n", entrada.Fecha)
		entrada.Revision["_cached"] = true
		return entrada.Revision, nil
	}

	revision, err := loopReact(codigo, proyectoDir)
	if err != nil {
		return nil, err
	}

	db[hash] = EntradaDB{
		Fecha:    time.Now().Format(time.RFC3339),
		Revision: revision,
	}
	guardarDB(dbPath, db)

	return revision, nil
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "Uso: go run agente_completo.go <ruta_archivo> [directorio]")
		os.Exit(1)
	}

	rutaArchivo := os.Args[1]
	proyectoDir := "."
	if len(os.Args) >= 3 {
		proyectoDir = os.Args[2]
	}

	resultado, err := revisar(rutaArchivo, proyectoDir)
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
