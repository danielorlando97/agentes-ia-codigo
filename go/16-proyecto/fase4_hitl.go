// Fase 4: añade checkpoint HITL para hallazgos críticos.
// Extiende la Fase 3 con aprobación interactiva antes de emitir el informe.

// Cómo ejecutar: make go FILE=go/16-proyecto/fase4_hitl.go

package main

import (
	"bufio"
	"bytes"
	"crypto/md5"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)
var (
	model  = envOr("MODEL", "claude-sonnet-4-6")
	apiURL = envBaseURL()
)

const systemPromptFase4 = `Eres un agente de revisión de código Python.
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

type F4Tool struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema interface{} `json:"input_schema"`
}
type F4Msg struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"`
}
type F4Block struct {
	Type  string          `json:"type"`
	Text  string          `json:"text,omitempty"`
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
}
type F4ToolResult struct {
	Type      string `json:"type"`
	ToolUseID string `json:"tool_use_id"`
	Content   string `json:"content"`
}
type F4Resp struct {
	Content    []F4Block `json:"content"`
	StopReason string    `json:"stop_reason"`
}

// ---- Herramientas ----

var herramientasFase4 = []F4Tool{
	{Name: "read_file", Description: "Lee el contenido de un archivo del proyecto",
		InputSchema: map[string]interface{}{"type": "object",
			"properties": map[string]interface{}{"path": map[string]interface{}{"type": "string"}},
			"required": []string{"path"}}},
	{Name: "run_code", Description: "Ejecuta código Python y devuelve stdout/stderr",
		InputSchema: map[string]interface{}{"type": "object",
			"properties": map[string]interface{}{
				"code":    map[string]interface{}{"type": "string"},
				"timeout": map[string]interface{}{"type": "integer"},
			}, "required": []string{"code"}}},
	{Name: "search_docs", Description: "Busca en la documentación técnica",
		InputSchema: map[string]interface{}{"type": "object",
			"properties": map[string]interface{}{"query": map[string]interface{}{"type": "string"}},
			"required": []string{"query"}}},
	{Name: "write_report", Description: "Escribe el informe final",
		InputSchema: map[string]interface{}{"type": "object",
			"properties": map[string]interface{}{
				"content":  map[string]interface{}{"type": "string"},
				"filename": map[string]interface{}{"type": "string"},
			}, "required": []string{"content", "filename"}}},
}

func ejecutarHerramientaFase4(nombre string, paramsRaw json.RawMessage, proyectoDir string) string {
	var params map[string]interface{}
	json.Unmarshal(paramsRaw, &params)
	switch nombre {
	case "read_file":
		rutaRel, _ := params["path"].(string)
		rutaAbs, _ := filepath.Abs(filepath.Join(proyectoDir, rutaRel))
		if !strings.HasPrefix(rutaAbs, filepath.Clean(proyectoDir)) {
			return "Error: ruta fuera del directorio"
		}
		contenido, err := os.ReadFile(rutaAbs)
		if err != nil {
			return "Error: archivo no encontrado"
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
		return out
	case "search_docs":
		q, _ := params["query"].(string)
		return fmt.Sprintf("[Documentación para '%s']", q)
	case "write_report":
		content, _ := params["content"].(string)
		filename, _ := params["filename"].(string)
		dir := filepath.Join(proyectoDir, "reports")
		os.MkdirAll(dir, 0755)
		ruta := filepath.Join(dir, filename)
		os.WriteFile(ruta, []byte(content), 0644)
		return fmt.Sprintf("Informe escrito en %s", ruta)
	}
	return fmt.Sprintf("Error: herramienta desconocida '%s'", nombre)
}

// ---- Llamada API ----

func llamarAnthropicFase4(msgs []F4Msg, proyectoDir string) (*F4Resp, error) {
	body, _ := json.Marshal(map[string]interface{}{
		"model":      model,
		"max_tokens": 4096,
		"system":     systemPromptFase4,
		"tools":      herramientasFase4,
		"messages":   msgs,
	})
	req, _ := http.NewRequest("POST", apiURL, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", os.Getenv("ANTHROPIC_API_KEY"))
	req.Header.Set("anthropic-version", "2023-06-01")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, respBody)
	}
	var result F4Resp
	json.Unmarshal(respBody, &result)
	return &result, nil
}

// ---- Memoria + ReAct ----

func agenteRevisionPipeline(codigo, ruta, proyectoDir, dbPath string) (map[string]interface{}, error) {
	// Fase 3: consultar caché
	db, err := sql.Open("sqlite3", dbPath)
	if err == nil {
		db.Exec(`CREATE TABLE IF NOT EXISTS revisiones (hash_archivo TEXT PRIMARY KEY, ruta TEXT, fecha TEXT, hallazgos_json TEXT, resumen TEXT)`)
		h := fmt.Sprintf("%x", sha256.Sum256([]byte(codigo)))
		row := db.QueryRow("SELECT hallazgos_json, resumen, fecha FROM revisiones WHERE hash_archivo = ?", h)
		var hj, res, fecha string
		if row.Scan(&hj, &res, &fecha) == nil {
			var hallazgos interface{}
			json.Unmarshal([]byte(hj), &hallazgos)
			db.Close()
			return map[string]interface{}{
				"hallazgos": hallazgos, "resumen": res, "fecha": fecha,
				"nota": fmt.Sprintf("Revisión previa del %s", fecha), "cached": true,
			}, nil
		}
	}

	// Fase 2: loop ReAct
	mensajes := []F4Msg{{Role: "user", Content: fmt.Sprintf("Revisa este código:\n\n```python\n%s\n```", codigo)}}
	var revision map[string]interface{}
	for paso := 0; paso < 15; paso++ {
		resp, err := llamarAnthropicFase4(mensajes, proyectoDir)
		if err != nil {
			return nil, err
		}
		if resp.StopReason == "end_turn" {
			for _, b := range resp.Content {
				if b.Type == "text" {
					inicio := strings.Index(b.Text, "{")
					fin := strings.LastIndex(b.Text, "}") + 1
					if inicio != -1 && fin > 0 {
						json.Unmarshal([]byte(b.Text[inicio:fin]), &revision)
						break
					}
				}
			}
			break
		}
		if resp.StopReason == "tool_use" {
			mensajes = append(mensajes, F4Msg{Role: "assistant", Content: resp.Content})
			var resultados []F4ToolResult
			for _, b := range resp.Content {
				if b.Type == "tool_use" {
					resultados = append(resultados, F4ToolResult{
						Type:      "tool_result",
						ToolUseID: b.ID,
						Content:   ejecutarHerramientaFase4(b.Name, b.Input, proyectoDir),
					})
				}
			}
			mensajes = append(mensajes, F4Msg{Role: "user", Content: resultados})
		}
	}
	if revision == nil {
		return nil, fmt.Errorf("no se pudo extraer revisión JSON")
	}

	// Guardar en caché
	if db != nil {
		h := fmt.Sprintf("%x", sha256.Sum256([]byte(codigo)))
		hj, _ := json.Marshal(revision["hallazgos"])
		res, _ := revision["resumen"].(string)
		db.Exec(`INSERT OR REPLACE INTO revisiones VALUES (?, ?, datetime('now'), ?, ?)`, h, ruta, string(hj), res)
		db.Close()
	}
	return revision, nil
}

// ---- HITL ----

func necesitaAprobacion(revision map[string]interface{}) bool {
	hallazgos, _ := revision["hallazgos"].([]interface{})
	for _, h := range hallazgos {
		if hm, ok := h.(map[string]interface{}); ok {
			if sev, _ := hm["severidad"].(string); sev == "critical" {
				return true
			}
		}
	}
	return false
}

func solicituarAprobacionCLI(revision map[string]interface{}, scanner *bufio.Scanner) (map[string]interface{}, bool) {
	hallazgos, _ := revision["hallazgos"].([]interface{})
	var criticos []map[string]interface{}
	for _, h := range hallazgos {
		if hm, ok := h.(map[string]interface{}); ok {
			if sev, _ := hm["severidad"].(string); sev == "critical" {
				criticos = append(criticos, hm)
			}
		}
	}

	fmt.Printf("\n=== REVISIÓN REQUIERE APROBACIÓN (%d crítico(s)) ===\n", len(criticos))
	for i, h := range criticos {
		linea := "?"
		if l, ok := h["linea"]; ok && l != nil {
			linea = fmt.Sprintf("%v", l)
		}
		fmt.Printf("%d. Línea %s: %s\n", i+1, linea, h["descripcion"])
		fmt.Printf("   Sugerencia: %s\n\n", h["sugerencia"])
	}
	fmt.Println("Opciones: [a]probar / [m]odificar / [d]escartar críticos")

	fmt.Print("\nElige [a/m/d]: ")
	scanner.Scan()
	opcion := strings.TrimSpace(strings.ToLower(scanner.Text()))

	switch opcion {
	case "a":
		return revision, true
	case "m":
		fmt.Printf("Número de hallazgo a modificar (1-%d): ", len(criticos))
		scanner.Scan()
		idx, err := strconv.Atoi(strings.TrimSpace(scanner.Text()))
		if err != nil || idx < 1 || idx > len(criticos) {
			fmt.Println("Índice inválido — aprobando sin cambios")
			return revision, true
		}
		fmt.Print("Nueva descripción: ")
		scanner.Scan()
		criticos[idx-1]["descripcion"] = scanner.Text()
		return revision, true
	case "d":
		fmt.Print("Justificación: ")
		scanner.Scan()
		justificacion := scanner.Text()
		var restantes []interface{}
		for _, h := range hallazgos {
			if hm, ok := h.(map[string]interface{}); ok {
				if sev, _ := hm["severidad"].(string); sev != "critical" {
					restantes = append(restantes, hm)
				}
			}
		}
		revision["hallazgos"] = restantes
		revision["hitl_descarte"] = justificacion
		return revision, true
	}
	return revision, false
}

// ---- Pipeline completo ----

func pipelineRevisionCompleto(codigo, ruta, proyectoDir string) (map[string]interface{}, error) {
	revision, err := agenteRevisionPipeline(codigo, ruta, proyectoDir, "revisiones.db")
	if err != nil {
		return nil, err
	}

	if necesitaAprobacion(revision) {
		scanner := bufio.NewScanner(os.Stdin)
		revision, ok := solicituarAprobacionCLI(revision, scanner)
		if !ok {
			return map[string]interface{}{"estado": "rechazado", "revision": nil}, nil
		}
		h := md5.Sum([]byte(ruta))
		nombre := fmt.Sprintf("revision_%x.json", h[:4])
		ejecutarHerramientaFase4("write_report", mustMarshal(map[string]interface{}{
			"content": toJSON(revision), "filename": nombre,
		}), proyectoDir)
		return revision, nil
	}

	h := md5.Sum([]byte(ruta))
	nombre := fmt.Sprintf("revision_%x.json", h[:4])
	ejecutarHerramientaFase4("write_report", mustMarshal(map[string]interface{}{
		"content": toJSON(revision), "filename": nombre,
	}), proyectoDir)
	return revision, nil
}

func mustMarshal(v interface{}) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

func toJSON(v interface{}) string {
	b, _ := json.MarshalIndent(v, "", "  ")
	return string(b)
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
import subprocess
def ejecutar_comando(cmd_usuario):
    # CRÍTICO: inyección de comandos
    resultado = subprocess.run(cmd_usuario, shell=True, capture_output=True, text=True)
    return resultado.stdout
`
		ruta = "test.py"
	}
	proyectoDir = "."
	if len(os.Args) > 2 {
		proyectoDir = os.Args[2]
	}

	revision, err := pipelineRevisionCompleto(codigo, ruta, proyectoDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
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
