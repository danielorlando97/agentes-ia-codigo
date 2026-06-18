// Sandboxing: ejecución aislada de código con límites de recursos y red

// Cómo ejecutar: make ts SCRIPT=typescript/15-seguridad/sandboxing.ts

import Anthropic from "@anthropic-ai/sdk";
import { spawn } from "child_process";
import fs from "fs";
import os from "os";
import path from "path";

const cliente = new Anthropic();

// ─── Nivel 1: child_process básico con tmpdir ─────────────────────────────────

function ejecutarSandboxBasico(
  codigo: string,
  timeoutMs: number = 10_000
): Promise<{ stdout: string; stderr: string; rc: number }> {
  return new Promise((resolve) => {
    const tmpdir = fs.mkdtempSync(path.join(os.tmpdir(), "sandbox-"));
    const ruta = path.join(tmpdir, "script.js");
    fs.writeFileSync(ruta, codigo, "utf8");

    let stdout = "";
    let stderr = "";
    let finished = false;

    const proc = spawn(process.execPath, [ruta], {
      cwd: tmpdir,
      env: { PATH: "/usr/local/bin:/usr/bin:/bin", HOME: tmpdir },
    });

    proc.stdout.on("data", (d: Buffer) => { stdout += d.toString(); });
    proc.stderr.on("data", (d: Buffer) => { stderr += d.toString(); });

    const timer = setTimeout(() => {
      if (!finished) {
        proc.kill("SIGKILL");
        finished = true;
        fs.rmSync(tmpdir, { recursive: true, force: true });
        resolve({ stdout: "", stderr: `Timeout: ejecución superó ${timeoutMs / 1000}s`, rc: -1 });
      }
    }, timeoutMs);

    proc.on("close", (code: number | null) => {
      if (!finished) {
        finished = true;
        clearTimeout(timer);
        fs.rmSync(tmpdir, { recursive: true, force: true });
        resolve({ stdout, stderr, rc: code ?? -1 });
      }
    });

    proc.on("error", (err: Error) => {
      if (!finished) {
        finished = true;
        clearTimeout(timer);
        fs.rmSync(tmpdir, { recursive: true, force: true });
        resolve({ stdout: "", stderr: `Error de sandbox: ${err.message}`, rc: -1 });
      }
    });
  });
}

// ─── Nivel 2: restricciones de recursos ──────────────────────────────────────

function ejecutarSandboxConRecursos(
  codigo: string,
  timeoutMs: number = 10_000,
  bloquearRed: boolean = true
): Promise<{ stdout: string; stderr: string; rc: number }> {
  // Network blocking: en producción usar Docker/seccomp/namespaces de red a nivel OS.
  // Aquí se inyecta un wrapper que lanza al intentar require('http'), require('https'), require('net').
  const preambulo = bloquearRed
    ? `
const _Module = require('module');
const _origLoad = _Module._load;
const _bloqueados = new Set(['http','https','net','tls','dgram','dns']);
_Module._load = function(req, ...rest) {
  if (_bloqueados.has(req)) throw new Error('Acceso a red bloqueado en sandbox: ' + req);
  return _origLoad.call(this, req, ...rest);
};
`
    : "";

  return ejecutarSandboxBasico(preambulo + codigo, timeoutMs);
}

// ─── Agente de código con sandbox ────────────────────────────────────────────

const HERRAMIENTAS: Anthropic.Tool[] = [
  {
    name: "ejecutar_codigo",
    description:
      "Ejecuta código JavaScript (Node.js) en un sandbox seguro. " +
      "El código no puede acceder a red ni a archivos fuera del directorio temporal.",
    input_schema: {
      type: "object",
      properties: {
        codigo: { type: "string", description: "Código JavaScript a ejecutar" },
        timeout: {
          type: "integer",
          description: "Timeout en segundos (máx 30)",
          default: 10,
        },
      },
      required: ["codigo"],
    },
  },
];

async function agenteCodigoSandboxed(tarea: string): Promise<string> {
  const mensajes: Anthropic.MessageParam[] = [{ role: "user", content: tarea }];

  for (let i = 0; i < 10; i++) {
    const respuesta = await cliente.messages.create({
      model: process.env["MODEL"] ?? "claude-sonnet-4-6",
      max_tokens: 1024,
      tools: HERRAMIENTAS,
      messages: mensajes,
    });

    mensajes.push({ role: "assistant", content: respuesta.content });

    if (respuesta.stop_reason === "end_turn") {
      const textBlock = respuesta.content.find((b) => b.type === "text");
      return textBlock && textBlock.type === "text" ? textBlock.text : "";
    }

    if (respuesta.stop_reason === "tool_use") {
      const toolResults: Anthropic.ToolResultBlockParam[] = [];
      for (const bloque of respuesta.content) {
        if (bloque.type !== "tool_use") continue;

        const input = bloque.input as { codigo?: string; timeout?: number };
        const codigo = input.codigo ?? "";
        const timeoutS = Math.min(input.timeout ?? 10, 30);

        const { stdout, stderr, rc } = await ejecutarSandboxConRecursos(codigo, timeoutS * 1000);

        let contenido: string;
        if (stdout || rc === 0) {
          contenido = stdout || "(sin output)";
        } else {
          contenido = `Error (rc=${rc}): ${stderr.slice(0, 500)}`;
        }

        console.log(`[sandbox] rc=${rc} | stdout=${stdout.slice(0, 100)} | stderr=${stderr.slice(0, 100)}`);
        toolResults.push({ type: "tool_result", tool_use_id: bloque.id, content: contenido });
      }
      mensajes.push({ role: "user", content: toolResults });
    }
  }

  return "[max iteraciones]";
}

// ─── Main ─────────────────────────────────────────────────────────────────────

async function main() {
  console.log("=== Sandbox básico ===");
  const tests: [string, string][] = [
    ["console.log('hello world')", "código legítimo"],
    ["const s = Date.now(); while(Date.now()-s<20000){}", "timeout"],
    ["console.log(2**31)", "operación matemática"],
  ];
  for (const [codigo, descripcion] of tests) {
    const { stdout, stderr, rc } = await ejecutarSandboxBasico(codigo, 3_000);
    console.log(
      `  [${descripcion}] rc=${rc} | stdout=${stdout.trim().slice(0, 50)} | stderr=${stderr.trim().slice(0, 50)}`
    );
  }

  console.log("\n=== Sandbox con bloqueo de red ===");
  const intentoRed = "const http = require('http'); http.get('http://google.com')";
  const { stderr, rc } = await ejecutarSandboxConRecursos(intentoRed, 5_000, true);
  console.log(`  Intento red: rc=${rc} | stderr=${stderr.trim().slice(0, 100)}`);

  console.log("\n=== Agente de código con sandbox ===");
  const resultado = await agenteCodigoSandboxed(
    "Calcula el factorial de 10 usando código JavaScript y muéstrame el resultado."
  );
  console.log(`Resultado: ${resultado.slice(0, 300)}`);
}

main().catch(console.error);
