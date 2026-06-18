// Variante V4: loop con budget adaptativo de tokens y tiempo.
//
// Reemplaza max_iterations fijo por topes de tokens consumidos y tiempo de pared.
// Protege contra costes desbocados en producción.

// Cómo ejecutar: make ts SCRIPT=typescript/02-anatomia-minima/loop-budget.ts

import Anthropic from "@anthropic-ai/sdk";

const MODEL = process.env["MODEL"] ?? "claude-sonnet-4-6";
const BUDGET_TOKENS = 200_000; // tope absoluto de tokens por sesión
const BUDGET_SECONDS = 120;    // tope de wall-clock en segundos

const TOOLS: Anthropic.Tool[] = [
  {
    name: "get_time",
    description: "Returns the current time in a timezone (UTC offset in hours).",
    input_schema: {
      type: "object",
      properties: { utc_offset: { type: "number" } },
      required: ["utc_offset"],
    },
  },
  {
    name: "add",
    description: "Sums two numbers.",
    input_schema: {
      type: "object",
      properties: { a: { type: "number" }, b: { type: "number" } },
      required: ["a", "b"],
    },
  },
];

function executeTool(name: string, args: Record<string, number>): string {
  try {
    if (name === "get_time") {
      const ms = Date.now() + args.utc_offset * 3600 * 1000;
      return new Date(ms).toISOString();
    }
    if (name === "add") return String(args.a + args.b);
    return `Tool '${name}' desconocida`;
  } catch (e) {
    return `Error en ${name}: ${e}`;
  }
}

async function runBudgetAgent(task: string): Promise<string> {
  // Loop con budget adaptativo: para antes de agotar tokens o tiempo.
  const client = new Anthropic();
  const messages: Anthropic.MessageParam[] = [{ role: "user", content: task }];

  let consumedTokens = 0;
  const startTime = Date.now(); // milisegundos
  let iteration = 0;

  while (true) {
    iteration++;
    const elapsed = (Date.now() - startTime) / 1000; // segundos

    // Verificar presupuestos ANTES de cada llamada
    if (consumedTokens >= BUDGET_TOKENS) {
      return `[budget agotado: ${consumedTokens} tokens en ${iteration - 1} iteraciones]`;
    }
    if (elapsed >= BUDGET_SECONDS) {
      return `[timeout: ${elapsed.toFixed(1)}s en ${iteration - 1} iteraciones]`;
    }

    const response = await client.messages.create({
      model: MODEL,
      max_tokens: 4096,
      tools: TOOLS,
      messages,
    });

    // Contabilizar tokens de esta llamada
    consumedTokens += response.usage.input_tokens + response.usage.output_tokens;
    console.log(
      `  [iter=${iteration}] stop=${response.stop_reason} ` +
        `tokens=${consumedTokens}/${BUDGET_TOKENS} ` +
        `time=${elapsed.toFixed(1)}s/${BUDGET_SECONDS}s`
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
        .map((b) => ({
          type: "tool_result" as const,
          tool_use_id: b.id,
          content: executeTool(b.name, b.input as Record<string, number>),
        }));

      messages.push({ role: "assistant", content: response.content });
      messages.push({ role: "user", content: toolResults });
      continue;
    }

    // stop_reason inesperado
    break;
  }

  return "[stop_reason inesperado]";
}

(async () => {
  const result = await runBudgetAgent("¿Qué hora es en Tokio (UTC+9) y cuánto es 47 + 89?");
  console.log(`\nRespuesta: ${result}`);
})();
