// Fase 3: añade memoria episódica SQLite a la Fase 2.
// Antes de revisar, consulta si ya existe una revisión cacheada del mismo código.

// Cómo ejecutar: make go FILE=go/16-proyecto/fase3_memoria.go

package main

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)
var (
	model  = envOr("MODEL", "claude-sonnet-4-6")
	apiURL = envBaseURL()
)

const systemPromptFase3 = `Eres un agente de revisión de código Python.
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

// ---- Tipos ----

type Fase3Req struct {
	Model     string        `json:"model"`
	MaxTokens int           `json:"max_tokens"`
	System    string        `json:"system"`
	Tools     []Fase3Tool   `json:"tools"`
	Messages  []Fase3Msg    `json:"messages"`
}

type Fase3Tool struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema interface{} `json:"input_schema"`
}

type Fase3Msg struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"`
}

type Fase3Block struct {
	Type  string          `json:"type"`
	Text  string          `json:"text,omitempty"`
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
}

type Fase3ToolResult struct {
	Type      string `json:"type"`
	ToolUseID string `json:"tool_use_id"`
	Content   string `json:"content"`
}

type Fase3Resp struct {
	Content    []Fase3Block `json:"content"`
	StopReason string       `json:"stop_reason"`
}

// ---- SQLite ----

func inicializarDB(dbPath string) (*sql.DB, error) {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, err
	}
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS revisiones (
		hash_archivo TEXT PRIMARY KEY,
		ruta         TEXT,
		fecha        TEXT,
		hallazgos_json TEXT,
		resumen      TEXT
	)`)
	return db, err
}

func hashCodigo(codigo string) string {
	h := sha256.Sum256([]byte(codigo))
	return fmt.Sprintf("%x", h)
}

func buscarRevisionPrevia(db *sql.DB, codigo string) (map[string]interface{}, bool) {
	h := hashCodigo(codigo)
	row := db.QueryRow(
		"SELECT hallazgos_json, resumen, fecha FROM revisiones WHERE hash_archivo = ?", h,
	)
	var hallazgosJSON, resumen, fecha string
	if err := row.Scan(&hallazgosJSON, &resumen, &fecha); err != nil {
		return nil, false
	}
	var hallazgos interface{}
	json.Unmarshal([]byte(hallazgosJSON), &hallazgos)
	return map[string]interface{}{
		"hallazgos": hallazgos,
		"resumen":   resumen,
		"fecha":     fecha,
		"cached":    true,
	}, true
}

func guardarRevision(db *sql.DB, codigo, ruta string, revision map[string]interface{}) {
	h := hashCodigo(codigo)
	hallazgosBytes, _ := json.Marshal(revision["hallazgos"])
	resumen, _ := revision["resumen"].(string)
	db.Exec(
		`INSERT OR REPLACE INTO revisiones (hash_archivo, ruta, fecha, hallazgos_json, resumen)
		 VALUES (?, ?, datetime('now'), ?, ?)`,
		h, ruta, string(hallazgosBytes), resumen,
	)
}

// ---- Herramientas (inlined desde Fase 2) ----

var herramientasFase3 = []Fase3Tool{
	{Name: "read_file", Description: "Lee el contenido de un archivo del proyecto",
		InputSchema: map[string]interface{}{"type": "object",
			"properties": map[string]interface{}{"path": map[string]interface{}{"type": "string"}},
			"required": []string{"path"}}},
	{Name: "run_code", Description: "Ejecuta un fragmento de código Python y devuelve stdout/stderr",
		InputSchema: map[string]interface{}{"type": "object",
			"properties": map[string]interface{}{
				"code":    map[string]interface{}{"type": "string"},
				"timeout": map[string]interface{}{"type": "integer", "default": 10},
			}, "required": []string{"code"}}},
	{Name: "search_docs", Description: "Busca en la documentación técnica interna",
		InputSchema: map[string]interface{}{"type": "object",
			"properties": map[string]interface{}{"query": map[string]interface{}{"type": "string"}},
			"required": []string{"query"}}},
	{Name: "write_report", Description: "Escribe el informe final de revisión",
		InputSchema: map[string]interface{}{"type": "object",
			"properties": map[string]interface{}{
				"content":  map[string]interface{}{"type": "string"},
				"filename": map[string]interface{}{"type": "string"},
			}, "required": []string{"content", "filename"}}},
}

func ejecutarHerramientaFase3(nombre string, paramsRaw json.RawMessage, proyectoDir string) string {
	var params map[string]interface{}
	json.Unmarshal(paramsRaw, &params)
	switch nombre {
	case "read_file":
		rutaRel, _ := params["path"].(string)
		rutaAbs, _ := filepath.Abs(filepath.Join(proyectoDir, rutaRel))
		if !strings.HasPrefix(rutaAbs, filepath.Clean(proyectoDir)) {
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
			return "Error: timeout"
		case <-done:
		}
		out := strings.TrimSpace(stdout.String())
		if s := strings.TrimSpace(stderr.String()); s != "" {
			out += "\nSTDERR: " + s
		}
		if out == "" {
			return "(sin output)"
		}
		return out
	case "search_docs":
		q, _ := params["query"].(string)
		return fmt.Sprintf("[Documentación para '%s': ver /docs/]", q)
	case "write_report":
		content, _ := params["content"].(string)
		filename, _ := params["filename"].(string)
		dir := filepath.Join(proyectoDir, "reports")
		os.MkdirAll(dir, 0755)
		ruta := filepath.Join(dir, filename)
		os.WriteFile(ruta, []byte(content), 0644)
		return fmt.Sprintf("Informe escrito en %s", ruta)
	}
	return fmt.Sprintf("Error: herramienta '%s' desconocida", nombre)
}

// ---- Llamada API ----

func llamarAnthropicFase3(req Fase3Req) (*Fase3Resp, error) {
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
	var result Fase3Resp
	json.Unmarshal(respBody, &result)
	return &result, nil
}

func extraerJSONFase3(texto string) (map[string]interface{}, error) {
	inicio := strings.Index(texto, "{")
	fin := strings.LastIndex(texto, "}") + 1
	if inicio == -1 || fin == 0 {
		return nil, fmt.Errorf("no se encontró JSON en: %s", texto[:min3(300, len(texto))])
	}
	var result map[string]interface{}
	return result, json.Unmarshal([]byte(texto[inicio:fin]), &result)
}

func min3(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ---- Loop ReAct ----

func agenteRevisionFase3(codigo, proyectoDir string) (map[string]interface{}, error) {
	mensajes := []Fase3Msg{
		{Role: "user", Content: fmt.Sprintf("Revisa este código:\n\n```python\n%s\n```", codigo)},
	}
	for paso := 0; paso < 15; paso++ {
		resp, err := llamarAnthropicFase3(Fase3Req{
			Model:     model,
			MaxTokens: 4096,
			System:    systemPromptFase3,
			Tools:     herramientasFase3,
			Messages:  mensajes,
		})
		if err != nil {
			return nil, err
		}
		if resp.StopReason == "end_turn" {
			for _, b := range resp.Content {
				if b.Type == "text" {
					return extraerJSONFase3(b.Text)
				}
			}
			return nil, fmt.Errorf("respuesta sin texto")
		}
		if resp.StopReason == "tool_use" {
			mensajes = append(mensajes, Fase3Msg{Role: "assistant", Content: resp.Content})
			var resultados []Fase3ToolResult
			for _, b := range resp.Content {
				if b.Type == "tool_use" {
					resultados = append(resultados, Fase3ToolResult{
						Type:      "tool_result",
						ToolUseID: b.ID,
						Content:   ejecutarHerramientaFase3(b.Name, b.Input, proyectoDir),
					})
				}
			}
			mensajes = append(mensajes, Fase3Msg{Role: "user", Content: resultados})
		}
	}
	return nil, fmt.Errorf("el agente no terminó en 15 pasos")
}

// ---- Pipeline con memoria ----

func agenteRevisionConMemoria(codigo, ruta, proyectoDir, dbPath string) (map[string]interface{}, error) {
	db, err := inicializarDB(dbPath)
	if err != nil {
		return nil, fmt.Errorf("error abriendo DB: %w", err)
	}
	defer db.Close()

	if prev, ok := buscarRevisionPrevia(db, codigo); ok {
		prev["nota"] = fmt.Sprintf("Revisión previa del %s", prev["fecha"])
		return prev, nil
	}

	revision, err := agenteRevisionFase3(codigo, proyectoDir)
	if err != nil {
		return nil, err
	}
	guardarRevision(db, codigo, ruta, revision)
	return revision, nil
}

func main() {
	var codigo, ruta, proyectoDir string

	if len(os.Args) > 1 {
		contenido, err := os.ReadFile(os.Args[1])
		if err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			os.Exit(1)
		}
		codigo = string(contenido)
		ruta = os.Args[1]
	} else {
		codigo = `
def procesar(items):
    return [item.value for item in items]  # AttributeError si item no tiene .value
`
		ruta = "test.py"
	}
	proyectoDir = "."
	if len(os.Args) > 2 {
		proyectoDir = os.Args[2]
	}

	revision, err := agenteRevisionConMemoria(codigo, ruta, proyectoDir, "revisiones.db")
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}

	cached, _ := revision["cached"].(bool)
	if cached {
		fmt.Println("[CACHED]")
	} else {
		fmt.Println("[NUEVO]")
	}
	out, _ := json.MarshalIndent(revision, "", "  ")
	fmt.Println(string(out))
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
