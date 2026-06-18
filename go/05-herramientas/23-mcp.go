// Cliente MCP mínimo.
//
// Implementa el protocolo MCP (Model Context Protocol) via stdio
// para conectar a un servidor MCP, listar sus herramientas y ejecutar tool calls.
//
// El protocolo es JSON-RPC 2.0. Flujo:
//   1. initialize (negociar capabilities)
//   2. tools/list (descubrir herramientas disponibles)
//   3. tools/call (ejecutar una herramienta)
//
// Este ejemplo lanza un servidor MCP mínimo en un subproceso Python
// para demostrar el protocolo sin el SDK de Go. En producción se usa
// github.com/modelcontextprotocol/go-sdk.

// Cómo ejecutar: make go FILE=go/05-herramientas/23-mcp.go

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
)

var modelMCP = envOr("MODEL", "claude-sonnet-4-6")
var apiURL = envBaseURL() + "/v1/messages"

// serverScript es un servidor MCP mínimo en Python que se lanza como subproceso.
// En un escenario real, se conectaría al servidor MCP del filesystem de Anthropic
// u otro servidor externo.
const serverScript = `
import sys, json

tools = [
    {
        "name": "echo",
        "description": "Devuelve el texto recibido tal cual.",
        "inputSchema": {
            "type": "object",
            "properties": {"text": {"type": "string"}},
            "required": ["text"]
        }
    },
    {
        "name": "add",
        "description": "Suma dos numeros.",
        "inputSchema": {
            "type": "object",
            "properties": {
                "a": {"type": "number"},
                "b": {"type": "number"}
            },
            "required": ["a", "b"]
        }
    }
]

for line in sys.stdin:
    line = line.strip()
    if not line:
        continue
    try:
        req = json.loads(line)
    except:
        continue

    method = req.get("method", "")
    req_id = req.get("id")

    if method == "initialize":
        resp = {"jsonrpc": "2.0", "id": req_id, "result": {
            "protocolVersion": "2025-03-26",
            "capabilities": {"tools": {}},
            "serverInfo": {"name": "echo-server", "version": "1.0"}
        }}
        sys.stdout.write(json.dumps(resp) + "\n")
        sys.stdout.flush()
    elif method == "notifications/initialized":
        pass
    elif method == "tools/list":
        resp = {"jsonrpc": "2.0", "id": req_id, "result": {"tools": tools}}
        sys.stdout.write(json.dumps(resp) + "\n")
        sys.stdout.flush()
    elif method == "tools/call":
        params = req.get("params", {})
        name = params.get("name", "")
        args = params.get("arguments", {})
        if name == "echo":
            content = [{"type": "text", "text": args.get("text", "")}]
        elif name == "add":
            content = [{"type": "text", "text": str(args.get("a", 0) + args.get("b", 0))}]
        else:
            resp = {"jsonrpc": "2.0", "id": req_id, "error": {"code": -32601, "message": "tool not found: " + name}}
            sys.stdout.write(json.dumps(resp) + "\n")
            sys.stdout.flush()
            continue
        resp = {"jsonrpc": "2.0", "id": req_id, "result": {"content": content, "isError": False}}
        sys.stdout.write(json.dumps(resp) + "\n")
        sys.stdout.flush()
    else:
        resp = {"jsonrpc": "2.0", "id": req_id, "error": {"code": -32601, "message": "method not found"}}
        sys.stdout.write(json.dumps(resp) + "\n")
        sys.stdout.flush()
`

// --- Tipos JSON-RPC 2.0 ---

type jsonRPCRequest struct {
	JSONRPC string                 `json:"jsonrpc"`
	ID      int64                  `json:"id"`
	Method  string                 `json:"method"`
	Params  map[string]interface{} `json:"params,omitempty"`
}

type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type mcpTool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"inputSchema"`
}

// --- Cliente MCP stdio ---

type mcpClient struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	scanner *bufio.Scanner
	nextID  atomic.Int64
	mu      sync.Mutex
	pending map[int64]chan jsonRPCResponse
}

func newMCPClient(command string, args ...string) (*mcpClient, error) {
	cmd := exec.Command(command, args...)
	cmd.Stderr = os.Stderr

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	c := &mcpClient{
		cmd:     cmd,
		stdin:   stdin,
		scanner: bufio.NewScanner(stdout),
		pending: make(map[int64]chan jsonRPCResponse),
	}

	go c.readLoop()
	return c, nil
}

func (c *mcpClient) readLoop() {
	for c.scanner.Scan() {
		line := c.scanner.Text()
		if line == "" {
			continue
		}
		var resp jsonRPCResponse
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			continue
		}
		c.mu.Lock()
		ch, ok := c.pending[resp.ID]
		if ok {
			delete(c.pending, resp.ID)
		}
		c.mu.Unlock()
		if ok {
			ch <- resp
		}
	}
}

func (c *mcpClient) call(method string, params map[string]interface{}) (json.RawMessage, error) {
	id := c.nextID.Add(1)
	req := jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}
	data, _ := json.Marshal(req)

	ch := make(chan jsonRPCResponse, 1)
	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()

	c.stdin.Write(append(data, '\n'))
	resp := <-ch
	if resp.Error != nil {
		return nil, fmt.Errorf("MCP error %d: %s", resp.Error.Code, resp.Error.Message)
	}
	return resp.Result, nil
}

func (c *mcpClient) notify(method string) {
	req := map[string]interface{}{"jsonrpc": "2.0", "method": method}
	data, _ := json.Marshal(req)
	c.stdin.Write(append(data, '\n'))
}

func (c *mcpClient) initialize() error {
	_, err := c.call("initialize", map[string]interface{}{
		"protocolVersion": "2025-03-26",
		"capabilities":    map[string]interface{}{"tools": map[string]interface{}{}},
		"clientInfo":      map[string]interface{}{"name": "agente-libro", "version": "1.0"},
	})
	if err != nil {
		return err
	}
	c.notify("notifications/initialized")
	return nil
}

func (c *mcpClient) listTools() ([]mcpTool, error) {
	result, err := c.call("tools/list", map[string]interface{}{})
	if err != nil {
		return nil, err
	}
	var resp struct {
		Tools []mcpTool `json:"tools"`
	}
	_ = json.Unmarshal(result, &resp)
	return resp.Tools, nil
}

func (c *mcpClient) callTool(name string, args map[string]interface{}) (string, error) {
	result, err := c.call("tools/call", map[string]interface{}{
		"name":      name,
		"arguments": args,
	})
	if err != nil {
		return "", err
	}
	var resp struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	_ = json.Unmarshal(result, &resp)
	out := ""
	for _, c := range resp.Content {
		out += c.Text
	}
	return out, nil
}

func (c *mcpClient) close() {
	c.stdin.Close()
	c.cmd.Wait()
}

// --- Convertir herramientas MCP al formato de Anthropic ---

type anthropicTool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"input_schema"`
}

func mcpToAnthropicTool(t mcpTool) anthropicTool {
	return anthropicTool{
		Name:        t.Name,
		Description: t.Description,
		InputSchema: t.InputSchema,
	}
}

// --- HTTP client para Anthropic ---

type msgMCP struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type blockMCP struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   string          `json:"content,omitempty"`
}

type apiRespMCP struct {
	Content    []blockMCP `json:"content"`
	StopReason string     `json:"stop_reason"`
}

func callAnthropicMCP(payload map[string]interface{}) (*apiRespMCP, error) {
	body, _ := json.Marshal(payload)
	req, _ := http.NewRequestWithContext(context.Background(), "POST", apiURL, bytes.NewReader(body))
	req.Header.Set("x-api-key", os.Getenv("ANTHROPIC_API_KEY"))
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("content-type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	var r apiRespMCP
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("parse: %s — %w", string(data), err)
	}
	return &r, nil
}

// --- Demo ---

func main() {
	fmt.Println("=== Cliente MCP mínimo ===\n")

	// Escribir el servidor Python a un archivo temporal
	tmpFile, err := os.CreateTemp("", "mcp-server-*.py")
	if err != nil {
		panic(err)
	}
	defer os.Remove(tmpFile.Name())
	tmpFile.WriteString(serverScript)
	tmpFile.Close()

	// 1. Conectar al servidor MCP
	fmt.Println("1. Iniciando servidor MCP (Python subprocess)...")
	mcp, err := newMCPClient("python3", tmpFile.Name())
	if err != nil {
		panic(err)
	}
	defer mcp.close()

	// 2. Inicializar
	fmt.Println("2. Inicializando conexión MCP...")
	if err := mcp.initialize(); err != nil {
		panic(err)
	}
	fmt.Println("   OK — handshake completado\n")

	// 3. Listar herramientas
	fmt.Println("3. Descubriendo herramientas (tools/list)...")
	tools, err := mcp.listTools()
	if err != nil {
		panic(err)
	}
	fmt.Printf("   %d herramientas disponibles:\n", len(tools))
	for _, t := range tools {
		fmt.Printf("   - %s: %s\n", t.Name, t.Description)
	}
	fmt.Println()

	// 4. Llamada directa via MCP
	fmt.Println("4. Llamada directa: add(17, 25)")
	result, err := mcp.callTool("add", map[string]interface{}{"a": 17, "b": 25})
	if err != nil {
		panic(err)
	}
	fmt.Printf("   Resultado: %s\n\n", result)

	// 5. Usar las herramientas MCP con Claude
	fmt.Println("5. Agente usando herramientas MCP con Claude...")
	anthropicTools := make([]anthropicTool, len(tools))
	for i, t := range tools {
		anthropicTools[i] = mcpToAnthropicTool(t)
	}

	taskJSON, _ := json.Marshal("Suma 42 + 58 y luego repite el texto 'Hola MCP'.")
	messages := []msgMCP{{Role: "user", Content: taskJSON}}

	for {
		resp, err := callAnthropicMCP(map[string]interface{}{
			"model":      modelMCP,
			"max_tokens": 1024,
			"tools":      anthropicTools,
			"messages":   messages,
		})
		if err != nil {
			panic(err)
		}

		if resp.StopReason == "end_turn" {
			for _, b := range resp.Content {
				if b.Type == "text" {
					fmt.Printf("\nRespuesta final: %s\n", b.Text)
				}
			}
			break
		}

		if resp.StopReason == "tool_use" {
			type toolResult struct {
				Type      string `json:"type"`
				ToolUseID string `json:"tool_use_id"`
				Content   string `json:"content"`
			}

			var results []toolResult
			for _, b := range resp.Content {
				if b.Type != "tool_use" {
					continue
				}
				var args map[string]interface{}
				_ = json.Unmarshal(b.Input, &args)
				fmt.Printf("   → %s(%v)\n", b.Name, args)

				text, err := mcp.callTool(b.Name, args)
				if err != nil {
					text = fmt.Sprintf("Error: %v", err)
				}
				fmt.Printf("   ← %s\n", text)

				results = append(results, toolResult{
					Type:      "tool_result",
					ToolUseID: b.ID,
					Content:   text,
				})
			}

			asstJSON, _ := json.Marshal(resp.Content)
			userJSON, _ := json.Marshal(results)
			messages = append(messages,
				msgMCP{Role: "assistant", Content: asstJSON},
				msgMCP{Role: "user", Content: userJSON},
			)
		}
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
