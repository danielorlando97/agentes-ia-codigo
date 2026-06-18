// Loop minimo: LLM + tools + iteracion hasta end_turn.

// Cómo ejecutar: make ts SCRIPT=typescript/01-que-es-un-agente/agente-minimo.ts
// Qué esperar: agente llama tools (get_time + add) y responde con ambos resultados.

import Anthropic from "@anthropic-ai/sdk";

const MODEL = process.env["MODEL"] ?? "claude-sonnet-4-6";
const MAX_ITERATIONS = 10;

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
    description: "Suma dos numeros.",
    input_schema: {
      type: "object",
      properties: { a: { type: "number" }, b: { type: "number" } },
      required: ["a", "b"],
    },
  },
];

function executeTool(name: string, args: Record<string, number>): string {
  if (name === "get_time") {
    const ms = Date.now() + args.utc_offset * 3600 * 1000;
    return new Date(ms).toISOString();
  }
  if (name === "add") return String(args.a + args.b);
  return `Tool '${name}' no existe`;
}

async function runAgent(task: string): Promise<string> {
  const client = new Anthropic();
  const messages: Anthropic.MessageParam[] = [{ role: "user", content: task }];

  for (let i = 0; i < MAX_ITERATIONS; i++) {
    const response = await client.messages.create({
      model: MODEL,
      max_tokens: 1024,
      tools: TOOLS,
      messages,
    });

    if (response.stop_reason === "end_turn" || response.stop_reason === "stop_sequence") {
      return response.content
        .filter((b): b is Anthropic.TextBlock => b.type === "text")
        .map((b) => b.text)
        .join("");
    }

    if (response.stop_reason === "tool_use") {
      const toolResults = response.content
        .filter((b): b is Anthropic.ToolUseBlock => b.type === "tool_use")
        .map((b) => ({
          type: "tool_result" as const,
          tool_use_id: b.id,
          content: executeTool(b.name, b.input as Record<string, number>),
        }));
      messages.push({ role: "assistant", content: response.content });
      messages.push({ role: "user", content: toolResults });
      continue;
    }

    break;
  }

  return "[max iteraciones]";
}

runAgent("Que hora es en Tokio (UTC+9), y cuanto es 47 + 89?").then(console.log);
