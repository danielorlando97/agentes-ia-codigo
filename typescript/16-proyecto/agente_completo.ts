// Pipeline del agente de revisión de código — equivalente lógico al de Python.

// Cómo ejecutar: make ts SCRIPT=typescript/16-proyecto/agente_completo.ts

import Anthropic from "@anthropic-ai/sdk";
import * as fs from "fs";
import * as path from "path";
import * as crypto from "crypto";
import { execSync } from "child_process";
import * as os from "os";

const SYSTEM_PROMPT = `Eres un agente de revisión de código Python.
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
}`;

const HERRAMIENTAS: Anthropic.Tool[] = [
  {
    name: "read_file",
    description: "Lee el contenido de un archivo del proyecto",
    input_schema: {
      type: "object",
      properties: {
        path: {
          type: "string",
          description: "Ruta relativa al directorio del proyecto",
        },
      },
      required: ["path"],
    },
  },
  {
    name: "run_code",
    description:
      "Ejecuta un fragmento de código Python en sandbox y devuelve stdout/stderr",
    input_schema: {
      type: "object",
      properties: {
        code: { type: "string" },
        timeout: { type: "number", default: 10 },
      },
      required: ["code"],
    },
  },
  {
    name: "search_docs",
    description: "Busca en la documentación técnica del equipo",
    input_schema: {
      type: "object",
      properties: { query: { type: "string" } },
      required: ["query"],
    },
  },
  {
    name: "write_report",
    description: "Escribe el informe final de revisión en disco",
    input_schema: {
      type: "object",
      properties: {
        content: { type: "string" },
        filename: { type: "string" },
      },
      required: ["content", "filename"],
    },
  },
];

interface HallazgoEsperado {
  linea?: number | null;
  severidad: "critical" | "high" | "medium" | "low";
  tipo: "bug" | "estilo" | "rendimiento" | "seguridad";
  descripcion: string;
  sugerencia: string;
}

interface RevisionOutput {
  hallazgos: HallazgoEsperado[];
  resumen: string;
  _meta?: { pasos: number; tokens: { input: number; output: number } };
  _cached?: boolean;
}

function ejecutarHerramienta(
  nombre: string,
  params: Record<string, unknown>,
  proyectoDir: string
): string {
  if (nombre === "read_file") {
    const rutaRelativa = params.path as string;
    const rutaAbs = path.resolve(proyectoDir, rutaRelativa);
    if (!rutaAbs.startsWith(path.resolve(proyectoDir))) {
      return "Error: ruta fuera del directorio del proyecto";
    }
    try {
      return fs.readFileSync(rutaAbs, "utf-8");
    } catch {
      return `Error: archivo '${rutaRelativa}' no encontrado`;
    }
  }

  if (nombre === "run_code") {
    const codigo = params.code as string;
    const timeout = ((params.timeout as number) || 10) * 1000;
    const tmpdir = fs.mkdtempSync(path.join(os.tmpdir(), "agente-"));
    const script = path.join(tmpdir, "script.py");
    try {
      fs.writeFileSync(script, codigo);
      const resultado = execSync(`python3 ${script}`, {
        timeout,
        cwd: tmpdir,
        env: { PATH: "/usr/local/bin:/usr/bin:/bin", HOME: tmpdir },
        encoding: "utf-8",
      });
      return JSON.stringify({ stdout: resultado || "(vacío)", stderr: "" });
    } catch (err: unknown) {
      const error = err as { stdout?: string; stderr?: string; message?: string };
      if (error.stderr?.includes("Timeout")) {
        return "Error: timeout de ejecución";
      }
      return JSON.stringify({
        stdout: error.stdout || "(vacío)",
        stderr: error.stderr || error.message || "error desconocido",
      });
    } finally {
      fs.rmSync(tmpdir, { recursive: true, force: true });
    }
  }

  if (nombre === "search_docs") {
    return `[Documentación para '${params.query}': ver /docs/ del proyecto]`;
  }

  if (nombre === "write_report") {
    const reportsDir = path.join(proyectoDir, "reports");
    fs.mkdirSync(reportsDir, { recursive: true });
    const ruta = path.join(reportsDir, params.filename as string);
    fs.writeFileSync(ruta, params.content as string);
    return `Informe escrito en ${ruta}`;
  }

  return `Error: herramienta '${nombre}' desconocida`;
}

function extraerJson(texto: string): RevisionOutput {
  for (let i = 0; i < texto.length; i++) {
    if (texto[i] !== "{") continue;
    let depth = 0;
    for (let j = i; j < texto.length; j++) {
      if (texto[j] === "{") depth++;
      else if (texto[j] === "}") {
        depth--;
        if (depth === 0) {
          try {
            return JSON.parse(texto.slice(i, j + 1)) as RevisionOutput;
          } catch {
            break;
          }
        }
      }
    }
  }
  throw new Error(`No se encontró JSON en output: ${texto.slice(0, 300)}`);
}

async function loopReact(
  codigo: string,
  proyectoDir: string
): Promise<RevisionOutput> {
  const cliente = new Anthropic();
  const mensajes: Anthropic.MessageParam[] = [
    {
      role: "user",
      content: `Revisa este código:\n\n\`\`\`python\n${codigo}\n\`\`\``,
    },
  ];

  const MAX_PASOS = 15;
  const tokensTotal = { input: 0, output: 0 };

  for (let paso = 0; paso < MAX_PASOS; paso++) {
    const respuesta = await cliente.messages.create({
      model: process.env["MODEL"] ?? "claude-sonnet-4-6",
      max_tokens: 4096,
      system: SYSTEM_PROMPT,
      tools: HERRAMIENTAS,
      messages: mensajes,
    });

    tokensTotal.input += respuesta.usage.input_tokens;
    tokensTotal.output += respuesta.usage.output_tokens;

    if (respuesta.stop_reason === "end_turn") {
      const bloque = respuesta.content.find((b) => b.type === "text");
      if (!bloque || bloque.type !== "text") throw new Error("Sin output de texto");
      const revision = extraerJson(bloque.text);
      revision._meta = { pasos: paso + 1, tokens: tokensTotal };
      return revision;
    }

    if (respuesta.stop_reason === "tool_use") {
      mensajes.push({ role: "assistant", content: respuesta.content });
      const resultados: Anthropic.ToolResultBlockParam[] = [];

      for (const bloque of respuesta.content) {
        if (bloque.type === "tool_use") {
          const resultado = ejecutarHerramienta(
            bloque.name,
            bloque.input as Record<string, unknown>,
            proyectoDir
          );
          resultados.push({
            type: "tool_result",
            tool_use_id: bloque.id,
            content: resultado,
          });
        }
      }

      mensajes.push({ role: "user", content: resultados });
    }
  }

  throw new Error(`El agente no terminó en ${MAX_PASOS} pasos`);
}

async function revisar(
  rutaArchivo: string,
  proyectoDir: string
): Promise<RevisionOutput> {
  const codigo = fs.readFileSync(path.join(proyectoDir, rutaArchivo), "utf-8");
  const hashCodigo = crypto
    .createHash("sha256")
    .update(`${rutaArchivo}::${codigo}`)
    .digest("hex");

  // Memoria episódica en archivo JSON (SQLite requería dependencia adicional)
  const dbPath = path.join(proyectoDir, "revisiones.json");
  const db: Record<string, { fecha: string; revision: RevisionOutput }> =
    fs.existsSync(dbPath) ? JSON.parse(fs.readFileSync(dbPath, "utf-8")) : {};

  if (db[hashCodigo]) {
    const cached = db[hashCodigo];
    console.error(`[INFO] Revisión cacheada del ${cached.fecha}`);
    return { ...cached.revision, _cached: true };
  }

  const revision = await loopReact(codigo, proyectoDir);

  db[hashCodigo] = { fecha: new Date().toISOString(), revision };
  fs.writeFileSync(dbPath, JSON.stringify(db, null, 2));

  return revision;
}

// Punto de entrada
const args = process.argv.slice(2);
if (args.length === 0) {
  console.error("Uso: bun agente_completo.ts <ruta_archivo> [directorio]");
  process.exit(1);
}

revisar(args[0], args[1] || process.cwd())
  .then((resultado) => console.log(JSON.stringify(resultado, null, 2)))
  .catch((err) => {
    console.error("Error:", err.message);
    process.exit(1);
  });
