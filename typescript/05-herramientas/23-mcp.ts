// Cliente MCP mínimo.
//
// Implementa el protocolo MCP (Model Context Protocol) via stdio
// para conectar a un servidor MCP, listar sus herramientas y ejecutar tool calls.
//
// El protocolo es JSON-RPC 2.0. El flujo completo:
//   1. initialize (negociar capabilities)
//   2. tools/list (descubrir herramientas disponibles)
//   3. tools/call (ejecutar una herramienta)
//
// Este ejemplo usa un servidor MCP in-process simulado via stdio
// para demostrar el protocolo sin dependencias externas.
// En producción, se usa @modelcontextprotocol/sdk con un servidor real.

// Cómo ejecutar: make ts SCRIPT=typescript/05-herramientas/23-mcp.ts

import Anthropic from "@anthropic-ai/sdk";
import { spawn, ChildProcess } from "child_process";
import { createInterface } from "readline";

const MODEL = process.env["MODEL"] ?? "claude-sonnet-4-6";

// --- Tipos JSON-RPC 2.0 ---

interface JsonRpcRequest {
  jsonrpc: "2.0";
  id: number;
  method: string;
  params?: Record<string, unknown>;
}

interface JsonRpcResponse {
  jsonrpc: "2.0";
  id: number;
  result?: unknown;
  error?: { code: number; message: string };
}

interface McpTool {
  name: string;
  description: string;
  inputSchema: Record<string, unknown>;
}

// --- Cliente MCP mínimo via stdio ---

class McpStdioClient {
  private proc: ChildProcess;
  private rl: ReturnType<typeof createInterface>;
  private pending: Map<number, (resp: JsonRpcResponse) => void> = new Map();
  private nextId = 1;

  constructor(command: string, args: string[]) {
    this.proc = spawn(command, args, {
      stdio: ["pipe", "pipe", "inherit"],
    });

    this.rl = createInterface({ input: this.proc.stdout! });
    this.rl.on("line", (line) => {
      if (!line.trim()) return;
      try {
        const msg = JSON.parse(line) as JsonRpcResponse;
        const resolve = this.pending.get(msg.id);
        if (resolve) {
          this.pending.delete(msg.id);
          resolve(msg);
        }
      } catch {
        // ignorar líneas que no son JSON (e.g. logs del servidor)
      }
    });
  }

  private send(method: string, params?: Record<string, unknown>): Promise<unknown> {
    const id = this.nextId++;
    const req: JsonRpcRequest = { jsonrpc: "2.0", id, method, params };
    return new Promise((resolve, reject) => {
      this.pending.set(id, (resp) => {
        if (resp.error) reject(new Error(resp.error.message));
        else resolve(resp.result);
      });
      this.proc.stdin!.write(JSON.stringify(req) + "\n");
    });
  }

  async initialize(): Promise<void> {
    await this.send("initialize", {
      protocolVersion: "2025-03-26",
      capabilities: { tools: {} },
      clientInfo: { name: "agente-libro", version: "1.0" },
    });
    // Notificación (no espera respuesta)
    this.proc.stdin!.write(
      JSON.stringify({ jsonrpc: "2.0", method: "notifications/initialized" }) + "\n"
    );
  }

  async listTools(): Promise<McpTool[]> {
    const result = await this.send("tools/list", {}) as { tools: McpTool[] };
    return result.tools ?? [];
  }

  async callTool(name: string, args: Record<string, unknown>): Promise<unknown> {
    return this.send("tools/call", { name, arguments: args });
  }

  close(): void {
    this.proc.kill();
    this.rl.close();
  }
}

// --- Servidor MCP simulado (in-process via stdio) ---
// En un script separado para poder lanzarlo como subprocess

const SERVER_SCRIPT = `
const readline = require("readline");

const rl = readline.createInterface({ input: process.stdin });
let initialized = false;

const TOOLS = [
  {
    name: "echo",
    description: "Devuelve el texto recibido tal cual.",
    inputSchema: {
      type: "object",
      properties: { text: { type: "string" } },
      required: ["text"]
    }
  },
  {
    name: "add",
    description: "Suma dos números.",
    inputSchema: {
      type: "object",
      properties: {
        a: { type: "number" },
        b: { type: "number" }
      },
      required: ["a", "b"]
    }
  }
];

rl.on("line", (line) => {
  if (!line.trim()) return;
  let req;
  try { req = JSON.parse(line); } catch { return; }

  if (req.method === "initialize") {
    initialized = true;
    const resp = {
      jsonrpc: "2.0", id: req.id,
      result: {
        protocolVersion: "2025-03-26",
        capabilities: { tools: {} },
        serverInfo: { name: "echo-server", version: "1.0" }
      }
    };
    process.stdout.write(JSON.stringify(resp) + "\\n");
    return;
  }

  if (req.method === "notifications/initialized") return;

  if (req.method === "tools/list") {
    process.stdout.write(JSON.stringify({
      jsonrpc: "2.0", id: req.id, result: { tools: TOOLS }
    }) + "\\n");
    return;
  }

  if (req.method === "tools/call") {
    const { name, arguments: args } = req.params;
    let content;
    if (name === "echo") {
      content = [{ type: "text", text: args.text }];
    } else if (name === "add") {
      content = [{ type: "text", text: String(args.a + args.b) }];
    } else {
      process.stdout.write(JSON.stringify({
        jsonrpc: "2.0", id: req.id,
        error: { code: -32601, message: "Tool not found: " + name }
      }) + "\\n");
      return;
    }
    process.stdout.write(JSON.stringify({
      jsonrpc: "2.0", id: req.id,
      result: { content, isError: false }
    }) + "\\n");
    return;
  }

  process.stdout.write(JSON.stringify({
    jsonrpc: "2.0", id: req.id,
    error: { code: -32601, message: "Method not found: " + req.method }
  }) + "\\n");
});
`;

// --- Convertir herramientas MCP al formato de Anthropic ---

function mcpToolToAnthropicTool(tool: McpTool): Anthropic.Tool {
  return {
    name: tool.name,
    description: tool.description,
    input_schema: tool.inputSchema as Anthropic.Tool["input_schema"],
  };
}

// --- Demo completa ---

async function main() {
  console.log("=== Cliente MCP mínimo ===\n");

  // Lanzar el servidor MCP simulado como subprocess
  const fs = await import("fs");
  const os = await import("os");
  const path = await import("path");

  const tmpFile = path.join(os.tmpdir(), "mcp-echo-server.js");
  fs.writeFileSync(tmpFile, SERVER_SCRIPT);

  const mcp = new McpStdioClient("node", [tmpFile]);

  try {
    // 1. Inicializar conexión
    console.log("1. Inicializando conexión MCP...");
    await mcp.initialize();
    console.log("   OK — handshake completado\n");

    // 2. Listar herramientas disponibles
    console.log("2. Descubriendo herramientas (tools/list)...");
    const tools = await mcp.listTools();
    console.log(`   ${tools.length} herramientas disponibles:`);
    for (const tool of tools) {
      console.log(`   - ${tool.name}: ${tool.description}`);
    }
    console.log();

    // 3. Ejecutar una tool call directa via MCP
    console.log("3. Llamada directa: add(17, 25)");
    const addResult = await mcp.callTool("add", { a: 17, b: 25 }) as {
      content: Array<{ type: string; text: string }>;
    };
    console.log(`   Resultado: ${addResult.content[0]?.text}\n`);

    // 4. Usar las herramientas MCP con el modelo
    console.log("4. Agente usando herramientas MCP con Claude...");
    const client = new Anthropic();
    const anthropicTools = tools.map(mcpToolToAnthropicTool);

    const messages: Anthropic.MessageParam[] = [
      {
        role: "user",
        content: "Suma 42 + 58 y luego repite el texto 'Hola MCP'.",
      },
    ];

    let response = await client.messages.create({
      model: MODEL,
      max_tokens: 1024,
      tools: anthropicTools,
      messages,
    });

    // Loop hasta end_turn
    while (response.stop_reason === "tool_use") {
      const toolUseBlocks = response.content.filter(
        (b): b is Anthropic.ToolUseBlock => b.type === "tool_use"
      );

      const toolResults: Anthropic.ToolResultBlockParam[] = [];

      for (const block of toolUseBlocks) {
        console.log(
          `   → ${block.name}(${JSON.stringify(block.input)})`
        );
        const result = await mcp.callTool(
          block.name,
          block.input as Record<string, unknown>
        ) as { content: Array<{ type: string; text: string }> };

        const text = result.content.map((c) => c.text).join("");
        console.log(`   ← ${text}`);

        toolResults.push({
          type: "tool_result",
          tool_use_id: block.id,
          content: text,
        });
      }

      messages.push({ role: "assistant", content: response.content });
      messages.push({ role: "user", content: toolResults });

      response = await client.messages.create({
        model: MODEL,
        max_tokens: 1024,
        tools: anthropicTools,
        messages,
      });
    }

    const finalText = response.content
      .filter((b): b is Anthropic.TextBlock => b.type === "text")
      .map((b) => b.text)
      .join("");

    console.log(`\nRespuesta final: ${finalText}`);
  } finally {
    mcp.close();
    // Limpiar archivo temporal
    try {
      const fs = await import("fs");
      const os = await import("os");
      const path = await import("path");
      fs.unlinkSync(path.join(os.tmpdir(), "mcp-echo-server.js"));
    } catch {
      // ignorar
    }
  }
}

main().catch(console.error);
