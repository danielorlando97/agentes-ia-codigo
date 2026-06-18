// Hello agent de producción: system prompt + error handling + logging.
// Combina V2 (system prompt), V3 (errores gestionados) y V4 (logging).

// Cómo ejecutar: make ts SCRIPT=typescript/02-anatomia-minima/hello-produccion.ts

import Anthropic from "@anthropic-ai/sdk";

const MODEL = process.env["MODEL"] ?? "claude-sonnet-4-6";
const MAX_ITERATIONS = 20;
const client = new Anthropic();

const SYSTEM = `Eres un asistente de productividad. Responde en español, sé conciso.
Cuando el usuario pida una hora, usa get_time con el offset UTC correcto.
Cuando el usuario pida una suma, usa add.
Si no puedes completar una tarea con las herramientas disponibles, dilo claramente.`;

const TOOLS: Anthropic.Tool[] = [
  {
    name: "get_time",
    description: "Devuelve la hora actual en una zona horaria (offset UTC en horas).",
    input_schema: {
      type: "object",
      properties: { utc_offset: { type: "number" } },
      required: ["utc_offset"],
    },
  },
  {
    name: "add",
    description: "Suma dos números.",
    input_schema: {
      type: "object",
      properties: { a: { type: "number" }, b: { type: "number" } },
      required: ["a", "b"],
    },
  },
];

function executeTool(name: string, args: Record<string, unknown>): string {
  try {
    if (name === "get_time") {
      const offset = Number(args["utc_offset"]);
      if (offset < -12 || offset > 14) return `Error: utc_offset ${offset} fuera de rango [-12, 14]`;
      const now = new Date(Date.now() + offset * 3600_000);
      return now.toISOString();
    }
    if (name === "add") {
      return String(Number(args["a"]) + Number(args["b"]));
    }
    return `Error: herramienta '${name}' desconocida`;
  } catch (e) {
    return `Error en ${name}: ${e}`;
  }
}

async function runAgent(task: string): Promise<string> {
  const messages: Anthropic.MessageParam[] = [{ role: "user", content: task }];

  for (let iter = 0; iter < MAX_ITERATIONS; iter++) {
    const response = await client.messages.create({
      model: MODEL,
      max_tokens: 2048,
      system: SYSTEM,
      tools: TOOLS,
      messages,
    });

    console.debug(
      `iter=${iter + 1}/${MAX_ITERATIONS} stop=${response.stop_reason} ` +
      `tokens=${response.usage.input_tokens}+${response.usage.output_tokens}`
    );

    if (response.stop_reason === "end_turn" || response.stop_reason === "stop_sequence") {
      return response.content
        .filter((b): b is Anthropic.TextBlock => b.type === "text")
        .map((b) => b.text)
        .join("");
    }

    if (response.stop_reason === "tool_use") {
      const toolResults: Anthropic.ToolResultBlockParam[] = response.content
        .filter((b): b is Anthropic.ToolUseBlock => b.type === "tool_use")
        .map((b) => {
          const result = executeTool(b.name, b.input as Record<string, unknown>);
          console.debug(`  → ${b.name}(${JSON.stringify(b.input)}) = ${result}`);
          return { type: "tool_result", tool_use_id: b.id, content: result };
        });

      messages.push({ role: "assistant", content: response.content });
      messages.push({ role: "user", content: toolResults });
      continue;
    }

    console.warn(`stop_reason inesperado: ${response.stop_reason}`);
    break;
  }

  return "[max iteraciones]";
}

const result = await runAgent("¿Qué hora es en Tokio (UTC+9) y cuánto es 47 + 89?");
console.log(`\nRespuesta: ${result}`);
