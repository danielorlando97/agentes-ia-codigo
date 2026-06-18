# Cliente MCP mínimo.
#
# Implementa el protocolo MCP (Model Context Protocol) via stdio
# para conectar a un servidor MCP, listar sus herramientas y ejecutar tool calls.
#
# El protocolo es JSON-RPC 2.0. El flujo completo:
#   1. initialize (negociar capabilities)
#   2. tools/list (descubrir herramientas disponibles)
#   3. tools/call (ejecutar una herramienta)
#
# Este ejemplo usa un servidor MCP in-process simulado via stdio
# para demostrar el protocolo sin dependencias externas.
#
# Cómo ejecutar:
#   make py SCRIPT=python/05-herramientas/23-mcp.py
#
# Qué esperar:
#   Conexión a un servidor MCP via stdio, listado de herramientas disponibles,
#   y ejecución de un tool call a través del protocolo JSON-RPC 2.0.
#   Si no hay servidor MCP, muestra el protocolo de forma simulada.
#
# Variables de entorno:
#   MODEL — modelo a usar (default: claude-sonnet-4-6)

import json
import asyncio
import tempfile
import os
import anthropic

MODEL = os.environ.get("MODEL", "claude-sonnet-4-6")

# Script del servidor MCP simulado (Python, lanzado como subprocess)
SERVER_SCRIPT = '''
import sys
import json

TOOLS = [
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
        "description": "Suma dos números.",
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
    except Exception:
        continue

    method = req.get("method", "")
    req_id = req.get("id")

    if method == "initialize":
        resp = {
            "jsonrpc": "2.0", "id": req_id,
            "result": {
                "protocolVersion": "2025-03-26",
                "capabilities": {"tools": {}},
                "serverInfo": {"name": "echo-server", "version": "1.0"}
            }
        }
        sys.stdout.write(json.dumps(resp) + "\\n")
        sys.stdout.flush()
        continue

    if method == "notifications/initialized":
        continue

    if method == "tools/list":
        sys.stdout.write(json.dumps({
            "jsonrpc": "2.0", "id": req_id, "result": {"tools": TOOLS}
        }) + "\\n")
        sys.stdout.flush()
        continue

    if method == "tools/call":
        params = req.get("params", {})
        name = params.get("name")
        args = params.get("arguments", {})
        if name == "echo":
            content = [{"type": "text", "text": args.get("text", "")}]
        elif name == "add":
            content = [{"type": "text", "text": str(args.get("a", 0) + args.get("b", 0))}]
        else:
            sys.stdout.write(json.dumps({
                "jsonrpc": "2.0", "id": req_id,
                "error": {"code": -32601, "message": f"Tool not found: {name}"}
            }) + "\\n")
            sys.stdout.flush()
            continue
        sys.stdout.write(json.dumps({
            "jsonrpc": "2.0", "id": req_id,
            "result": {"content": content, "isError": False}
        }) + "\\n")
        sys.stdout.flush()
        continue

    sys.stdout.write(json.dumps({
        "jsonrpc": "2.0", "id": req_id,
        "error": {"code": -32601, "message": f"Method not found: {method}"}
    }) + "\\n")
    sys.stdout.flush()
'''


# --- Cliente MCP mínimo via stdio ---

class McpStdioClient:
    def __init__(self, command: str, args: list[str]):
        self._command = command
        self._args = args
        self._proc = None
        self._next_id = 1
        self._pending: dict[int, asyncio.Future] = {}
        self._reader_task = None

    async def start(self):
        self._proc = await asyncio.create_subprocess_exec(
            self._command, *self._args,
            stdin=asyncio.subprocess.PIPE,
            stdout=asyncio.subprocess.PIPE,
            stderr=None,
        )
        self._reader_task = asyncio.create_task(self._read_loop())

    async def _read_loop(self):
        while True:
            line = await self._proc.stdout.readline()
            if not line:
                break
            try:
                msg = json.loads(line.decode())
                req_id = msg.get("id")
                fut = self._pending.pop(req_id, None)
                if fut and not fut.done():
                    fut.set_result(msg)
            except Exception:
                pass  # ignorar líneas que no son JSON

    async def _send(self, method: str, params: dict | None = None) -> object:
        req_id = self._next_id
        self._next_id += 1
        req = {"jsonrpc": "2.0", "id": req_id, "method": method}
        if params is not None:
            req["params"] = params

        loop = asyncio.get_event_loop()
        fut = loop.create_future()
        self._pending[req_id] = fut

        self._proc.stdin.write((json.dumps(req) + "\n").encode())
        await self._proc.stdin.drain()

        resp = await fut
        if "error" in resp:
            raise RuntimeError(resp["error"]["message"])
        return resp.get("result")

    async def initialize(self):
        await self._send("initialize", {
            "protocolVersion": "2025-03-26",
            "capabilities": {"tools": {}},
            "clientInfo": {"name": "agente-libro", "version": "1.0"},
        })
        # Notificación (no espera respuesta)
        notif = json.dumps({"jsonrpc": "2.0", "method": "notifications/initialized"}) + "\n"
        self._proc.stdin.write(notif.encode())
        await self._proc.stdin.drain()

    async def list_tools(self) -> list[dict]:
        result = await self._send("tools/list", {})
        return result.get("tools", [])

    async def call_tool(self, name: str, arguments: dict) -> object:
        return await self._send("tools/call", {"name": name, "arguments": arguments})

    async def close(self):
        if self._reader_task:
            self._reader_task.cancel()
        if self._proc:
            self._proc.kill()
            await self._proc.wait()


# --- Convertir herramientas MCP al formato de Anthropic ---

def mcp_tool_to_anthropic(tool: dict) -> dict:
    return {
        "name": tool["name"],
        "description": tool["description"],
        "input_schema": tool["inputSchema"],
    }


# --- Demo completa ---

async def main():
    print("=== Cliente MCP mínimo ===\n")

    # Escribir el script del servidor en un archivo temporal
    with tempfile.NamedTemporaryFile(mode="w", suffix=".py", delete=False) as f:
        f.write(SERVER_SCRIPT)
        tmp_path = f.name

    mcp = McpStdioClient("python3", [tmp_path])

    try:
        await mcp.start()

        # 1. Inicializar conexión
        print("1. Inicializando conexión MCP...")
        await mcp.initialize()
        print("   OK — handshake completado\n")

        # 2. Listar herramientas disponibles
        print("2. Descubriendo herramientas (tools/list)...")
        tools = await mcp.list_tools()
        print(f"   {len(tools)} herramientas disponibles:")
        for tool in tools:
            print(f"   - {tool['name']}: {tool['description']}")
        print()

        # 3. Ejecutar una tool call directa via MCP
        print("3. Llamada directa: add(17, 25)")
        add_result = await mcp.call_tool("add", {"a": 17, "b": 25})
        text = add_result["content"][0]["text"]
        print(f"   Resultado: {text}\n")

        # 4. Usar las herramientas MCP con el modelo
        print("4. Agente usando herramientas MCP con Claude...")
        client = anthropic.Anthropic()
        anthropic_tools = [mcp_tool_to_anthropic(t) for t in tools]

        messages = [
            {
                "role": "user",
                "content": "Suma 42 + 58 y luego repite el texto 'Hola MCP'.",
            }
        ]

        response = client.messages.create(
            model=MODEL,
            max_tokens=1024,
            tools=anthropic_tools,
            messages=messages,
        )

        # Loop hasta end_turn
        while response.stop_reason == "tool_use":
            tool_blocks = [b for b in response.content if b.type == "tool_use"]
            tool_results = []

            for block in tool_blocks:
                print(f"   → {block.name}({json.dumps(block.input)})")
                result = await mcp.call_tool(block.name, block.input)
                text = "".join(c["text"] for c in result["content"])
                print(f"   ← {text}")
                tool_results.append({
                    "type": "tool_result",
                    "tool_use_id": block.id,
                    "content": text,
                })

            messages.append({"role": "assistant", "content": response.content})
            messages.append({"role": "user", "content": tool_results})

            response = client.messages.create(
                model=MODEL,
                max_tokens=1024,
                tools=anthropic_tools,
                messages=messages,
            )

        final_text = "".join(b.text for b in response.content if b.type == "text")
        print(f"\nRespuesta final: {final_text}")

    finally:
        await mcp.close()
        os.unlink(tmp_path)


if __name__ == "__main__":
    asyncio.run(main())
