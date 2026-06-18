// Fase 2: añade las 4 herramientas al loop ReAct de la Fase 1.
// El modelo decide cuándo usarlas; el código ejecuta y devuelve resultados.

// Cómo ejecutar: make ts SCRIPT=typescript/16-proyecto/fase2_herramientas.ts

import Anthropic from "@anthropic-ai/sdk";
import * as fs from "fs";
import * as path from "path";
import * as os from "os";
import { execSync } from "child_process";

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
    description: "Ejecuta un fragmento de código Python y devuelve stdout/stderr",
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
    description: "Busca en la documentación técnica interna del equipo",
    input_schema: {
      type: "object",
      properties: { query: { type: "string" } },
      required: ["query"],
    },
  },
  {
    name: "write_report",
    description: "Escribe el informe final de revisión",
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

export interface Hallazgo {
  linea?: number | null;
  severidad: "critical" | "high" | "medium" | "low";
  tipo: "bug" | "estilo" | "rendimiento" | "seguridad";
  descripcion: string;
  sugerencia: string;
}

export interface RevisionOutput {
  hallazgos: Hallazgo[];
  resumen: string;
}

export function ejecutarHerramienta(
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
      const resultado = execSync(`python3 "${script}"`, {
        timeout,
        cwd: tmpdir,
        env: { PATH: "/usr/local/bin:/usr/bin:/bin", HOME: tmpdir },
        encoding: "utf-8",
      });
      return resultado || "(sin output)";
    } catch (err: unknown) {
      const error = err as { stdout?: string; stderr?: string; message?: string };
      if ((error.message || "").includes("ETIMEDOUT") || (error.message || "").includes("timeout")) {
        return "Error: timeout de ejecución";
      }
      const stderr = error.stderr || "";
      const stdout = error.stdout || "";
      return (stdout + (stderr ? `\nSTDERR: ${stderr}` : "")).trim() || "(sin output)";
    } finally {
      fs.rmSync(tmpdir, { recursive: true, force: true });
    }
  }

  if (nombre === "search_docs") {
    return `[Documentación para '${params.query}': ver estándares del equipo en /docs/]`;
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
  const inicio = texto.indexOf("{");
  const fin = texto.lastIndexOf("}") + 1;
  if (inicio === -1 || fin === 0) {
    throw new Error(`No se encontró JSON en output: ${texto.slice(0, 300)}`);
  }
  return JSON.parse(texto.slice(inicio, fin)) as RevisionOutput;
}

export async function agenteRevision(
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

  for (let paso = 0; paso < MAX_PASOS; paso++) {
    const respuesta = await cliente.messages.create({
      model: process.env["MODEL"] ?? "claude-sonnet-4-6",
      max_tokens: 4096,
      system: SYSTEM_PROMPT,
      tools: HERRAMIENTAS,
      messages: mensajes,
    });

    if (respuesta.stop_reason === "end_turn") {
      const bloque = respuesta.content.find((b) => b.type === "text");
      if (!bloque || bloque.type !== "text") {
        throw new Error("Respuesta sin bloque de texto");
      }
      return extraerJson(bloque.text);
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

async function main() {
  const args = process.argv.slice(2);
  let codigo: string;
  let proyectoDir: string;

  if (args.length > 0) {
    codigo = fs.readFileSync(args[0], "utf-8");
    proyectoDir = args[1] || process.cwd();
  } else {
    codigo = `
def divide(a, b):
    return a / b  # ZeroDivisionError no manejado
`;
    proyectoDir = process.cwd();
  }

  const resultado = await agenteRevision(codigo, proyectoDir);
  console.log(JSON.stringify(resultado, null, 2));
}

main().catch((err) => {
  console.error("Error:", err.message);
  process.exit(1);
});
