// Variante ReAct: mismo loop que agente-minimo, pero con CoT explicito (Thought antes de Action).

// Cómo ejecutar: make ts SCRIPT=typescript/01-que-es-un-agente/agente-react.ts
// Qué esperar: trace con Thought/Action/Observation antes de la respuesta final.

import Anthropic from "@anthropic-ai/sdk";

const MODEL = process.env["MODEL"] ?? "claude-sonnet-4-6";
const MAX_ITERATIONS = 10;

const SYSTEM =
  "Eres un agente ReAct. Antes de cada llamada a herramienta escribe una linea " +
  "que empiece por 'Thought:' explicando tu razonamiento; luego usa la herramienta. " +
  "Cuando tengas la respuesta final, escribela despues de un 'Final answer:'.";

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

async function runReact(task: string): Promise<string> {
  const client = new Anthropic();
  const messages: Anthropic.MessageParam[] = [{ role: "user", content: task }];
  const trace: string[] = [];

  for (let i = 0; i < MAX_ITERATIONS; i++) {
    const response = await client.messages.create({
      model: MODEL,
      max_tokens: 1024,
      system: SYSTEM,
      tools: TOOLS,
      messages,
    });

    for (const block of response.content) {
      if (block.type === "text" && block.text.trim()) {
        trace.push(block.text.trim());
      } else if (block.type === "tool_use") {
        trace.push(`Action: ${block.name}(${JSON.stringify(block.input)})`);
      }
    }

    if (response.stop_reason === "end_turn" || response.stop_reason === "stop_sequence") {
      return trace.join("\n");
    }

    if (response.stop_reason === "tool_use") {
      const toolResults = response.content
        .filter((b): b is Anthropic.ToolUseBlock => b.type === "tool_use")
        .map((b) => {
          const out = executeTool(b.name, b.input as Record<string, number>);
          trace.push(`Observation: ${out}`);
          return { type: "tool_result" as const, tool_use_id: b.id, content: out };
        });
      messages.push({ role: "assistant", content: response.content });
      messages.push({ role: "user", content: toolResults });
      continue;
    }

    break;
  }

  return [...trace, "[max iteraciones]"].join("\n");
}

runReact("Que hora es en Tokio (UTC+9), y cuanto es 47 + 89?").then(console.log);
