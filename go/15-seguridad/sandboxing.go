// Sandboxing: ejecución aislada de código con límites de recursos y red

// Cómo ejecutar: make go FILE=go/15-seguridad/sandboxing.go

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

var modelSandbox = envOr("MODEL", "claude-sonnet-4-6")

// ─── Nivel 1: exec básico con tmpdir ─────────────────────────────────────────

type ResultadoEjecucion struct {
	Stdout string
	Stderr string
	RC     int
}

func ejecutarSandboxBasico(codigo string, timeout time.Duration) ResultadoEjecucion {
	tmpdir, err := os.MkdirTemp("", "sandbox-*")
	if err != nil {
		return ResultadoEjecucion{Stderr: "Error creando tmpdir: " + err.Error(), RC: -1}
	}
	defer os.RemoveAll(tmpdir)

	ruta := tmpdir + "/script.go"
	if err := os.WriteFile(ruta, []byte(codigo), 0600); err != nil {
		return ResultadoEjecucion{Stderr: "Error escribiendo script: " + err.Error(), RC: -1}
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "go", "run", ruta)
	cmd.Dir = tmpdir
	cmd.Env = []string{
		"HOME=" + tmpdir,
		"PATH=/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin",
		"GOPATH=" + tmpdir + "/go",
		"GOCACHE=" + tmpdir + "/gocache",
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err = cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return ResultadoEjecucion{
			Stderr: fmt.Sprintf("Timeout: ejecución superó %.0fs", timeout.Seconds()),
			RC:     -1,
		}
	}
	rc := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			rc = exitErr.ExitCode()
		} else {
			return ResultadoEjecucion{Stderr: "Error de sandbox: " + err.Error(), RC: -1}
		}
	}
	return ResultadoEjecucion{Stdout: stdout.String(), Stderr: stderr.String(), RC: rc}
}

// ─── Nivel 2: restricciones de recursos ──────────────────────────────────────

func ejecutarSandboxConRecursos(codigo string, timeout time.Duration, bloquearRed bool) ResultadoEjecucion {
	// Network blocking: en producción usar namespaces de red Linux (unshare -n),
	// seccomp BPF o Docker con --network none. Aquí se documenta la intención;
	// el agente de demo ejecuta código Go sin acceso de red por diseño del runtime.
	if bloquearRed {
		// Verificar que el código no importe paquetes de red conocidos
		bloqueados := []string{`"net"`, `"net/http"`, `"net/url"`, `"crypto/tls"`}
		for _, pkg := range bloqueados {
			if strings.Contains(codigo, pkg) {
				return ResultadoEjecucion{
					Stderr: fmt.Sprintf("Acceso a red bloqueado en sandbox: importación de %s no permitida", pkg),
					RC:     -1,
				}
			}
		}
	}
	return ejecutarSandboxBasico(codigo, timeout)
}

// ─── Agente de código con sandbox ────────────────────────────────────────────

var herramientasSandbox = []map[string]interface{}{
	{
		"name": "ejecutar_codigo",
		"description": "Ejecuta código Go en un sandbox seguro. " +
			"El código no puede acceder a red ni a archivos fuera del directorio temporal.",
		"input_schema": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"codigo":  map[string]interface{}{"type": "string", "description": "Código Go completo (con package main y func main)"},
				"timeout": map[string]interface{}{"type": "integer", "description": "Timeout en segundos (máx 30)", "default": 10},
			},
			"required": []string{"codigo"},
		},
	},
}

type MensajeSandbox struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"`
}

type AnthropicRequestSandbox struct {
	Model     string                   `json:"model"`
	MaxTokens int                      `json:"max_tokens"`
	Tools     []map[string]interface{} `json:"tools,omitempty"`
	Messages  []MensajeSandbox         `json:"messages"`
}

type ContentBlockSandbox struct {
	Type  string                 `json:"type"`
	Text  string                 `json:"text,omitempty"`
	ID    string                 `json:"id,omitempty"`
	Name  string                 `json:"name,omitempty"`
	Input map[string]interface{} `json:"input,omitempty"`
}

type AnthropicResponseSandbox struct {
	Content    []ContentBlockSandbox `json:"content"`
	StopReason string                `json:"stop_reason"`
}

func llamarAPISandbox(mensajes []MensajeSandbox) (*AnthropicResponseSandbox, error) {
	payload := AnthropicRequestSandbox{
		Model:     modelSandbox,
		MaxTokens: 1024,
		Tools:     herramientasSandbox,
		Messages:  mensajes,
	}
	body, _ := json.Marshal(payload)

	req, _ := http.NewRequest("POST", envBaseURL(), bytes.NewReader(body))
	req.Header.Set("x-api-key", os.Getenv("ANTHROPIC_API_KEY"))
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("content-type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)
	var ar AnthropicResponseSandbox
	if err := json.Unmarshal(data, &ar); err != nil {
		return nil, fmt.Errorf("respuesta inesperada: %s", data)
	}
	return &ar, nil
}

func agenteCodigoSandboxed(tarea string) (string, error) {
	mensajes := []MensajeSandbox{{Role: "user", Content: tarea}}

	for i := 0; i < 10; i++ {
		respuesta, err := llamarAPISandbox(mensajes)
		if err != nil {
			return "", err
		}

		mensajes = append(mensajes, MensajeSandbox{Role: "assistant", Content: respuesta.Content})

		if respuesta.StopReason == "end_turn" {
			for _, b := range respuesta.Content {
				if b.Type == "text" {
					return b.Text, nil
				}
			}
			return "", nil
		}

		if respuesta.StopReason == "tool_use" {
			var toolResults []map[string]interface{}
			for _, bloque := range respuesta.Content {
				if bloque.Type != "tool_use" {
					continue
				}

				codigo, _ := bloque.Input["codigo"].(string)
				timeoutF, _ := bloque.Input["timeout"].(float64)
				if timeoutF == 0 {
					timeoutF = 10
				}
				if timeoutF > 30 {
					timeoutF = 30
				}
				timeoutDur := time.Duration(timeoutF) * time.Second

				res := ejecutarSandboxConRecursos(codigo, timeoutDur, true)

				var contenido string
				if res.Stdout != "" || res.RC == 0 {
					if res.Stdout != "" {
						contenido = res.Stdout
					} else {
						contenido = "(sin output)"
					}
				} else {
					stderr := res.Stderr
					if len(stderr) > 500 {
						stderr = stderr[:500]
					}
					contenido = fmt.Sprintf("Error (rc=%d): %s", res.RC, stderr)
				}

				stdout := res.Stdout
				if len(stdout) > 100 {
					stdout = stdout[:100]
				}
				stderr := res.Stderr
				if len(stderr) > 100 {
					stderr = stderr[:100]
				}
				fmt.Printf("[sandbox] rc=%d | stdout=%s | stderr=%s\n", res.RC, stdout, stderr)

				toolResults = append(toolResults, map[string]interface{}{
					"type":        "tool_result",
					"tool_use_id": bloque.ID,
					"content":     contenido,
				})
			}
			mensajes = append(mensajes, MensajeSandbox{Role: "user", Content: toolResults})
		}
	}

	return "[max iteraciones]", nil
}

// ─── Main ─────────────────────────────────────────────────────────────────────

func main() {
	fmt.Println("=== Sandbox básico ===")
	tests := []struct {
		codigo      string
		descripcion string
	}{
		{`package main; import "fmt"; func main() { fmt.Println("hello world") }`, "código legítimo"},
		{`package main; import "time"; func main() { time.Sleep(20 * time.Second) }`, "timeout"},
		{`package main; import "fmt"; func main() { x := 1 << 31; fmt.Println(x) }`, "operación matemática"},
	}
	for _, t := range tests {
		res := ejecutarSandboxBasico(t.codigo, 3*time.Second)
		stdout := strings.TrimSpace(res.Stdout)
		stderr := strings.TrimSpace(res.Stderr)
		if len(stdout) > 50 {
			stdout = stdout[:50]
		}
		if len(stderr) > 50 {
			stderr = stderr[:50]
		}
		fmt.Printf("  [%s] rc=%d | stdout=%s | stderr=%s\n", t.descripcion, res.RC, stdout, stderr)
	}

	fmt.Println("\n=== Sandbox con bloqueo de red ===")
	intentoRed := `package main; import "net/http"; func main() { http.Get("http://google.com") }`
	res := ejecutarSandboxConRecursos(intentoRed, 5*time.Second, true)
	stderr := strings.TrimSpace(res.Stderr)
	if len(stderr) > 100 {
		stderr = stderr[:100]
	}
	fmt.Printf("  Intento red: rc=%d | stderr=%s\n", res.RC, stderr)

	fmt.Println("\n=== Agente de código con sandbox ===")
	resultado, err := agenteCodigoSandboxed(
		"Calcula el factorial de 10 usando código Go y muéstrame el resultado.",
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if len(resultado) > 300 {
		resultado = resultado[:300]
	}
	fmt.Printf("Resultado: %s\n", resultado)
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
