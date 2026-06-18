// Variante V5: loop con compactación de contexto.
//
// Cuando el historial se acerca al límite de la ventana, un paso intermedio
// comprime los mensajes antiguos en un resumen. Permite sesiones de horas
// sin agotar el contexto.

// Cómo ejecutar: make ts SCRIPT=typescript/02-anatomia-minima/loop-compactacion.ts

import Anthropic from "@anthropic-ai/sdk";

const MODEL = process.env["MODEL"] ?? "claude-sonnet-4-6";
const COMPACT_MODEL = process.env["COMPACT_MODEL"] ?? "claude-haiku-4-5-20251001"; // modelo barato para compactar
const CONTEXT_THRESHOLD = 40_000; // tokens; umbral conservador para este ejemplo
const MAX_ITERATIONS = 50;

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

/** Estimación rápida: ~4 chars por token. */
function estimateTokens(messages: Anthropic.MessageParam[]): number {
  return Math.floor(JSON.stringify(messages).length / 4);
}

/** Comprime el historial intermedio en un resumen.
 *
 * Conserva los primeros 2 mensajes (tarea original) y los últimos 6 (estado reciente).
 * El intermedio se resume en una llamada al modelo barato.
 */
async function compact(
  client: Anthropic,
  messages: Anthropic.MessageParam[]
): Promise<Anthropic.MessageParam[]> {
  if (messages.length <= 8) return messages;

  const first = messages.slice(0, 2);      // tarea original — siempre conservada
  const recent = messages.slice(-6);       // estado reciente — siempre conservado
  const toCompress = messages.slice(2, -6);

  if (toCompress.length === 0) return messages;

  console.log(`  [compactación] comprimiendo ${toCompress.length} mensajes intermedios...`);

  // Truncar la serialización a 15000 chars para no sobrepasar el contexto del modelo barato
  const historialTruncado = JSON.stringify(toCompress).slice(0, 15000);

  const summaryResponse = await client.messages.create({
    model: COMPACT_MODEL,
    max_tokens: 1500,
    messages: [
      {
        role: "user",
        content:
          "Resume este historial de un agente. Preserva exactamente:\n" +
          "- Cada herramienta llamada y su resultado\n" +
          "- Cada archivo leído o modificado\n" +
          "- Cada decisión tomada y por qué\n" +
          "- El estado actual de la tarea\n\n" +
          `Historial: ${historialTruncado}`,
      },
    ],
  });

  const summaryText = summaryResponse.content
    .filter((b): b is Anthropic.TextBlock => b.type === "text")
    .map((b) => b.text)
    .join("");

  const compressed: Anthropic.MessageParam = {
    role: "user",
    content: `[HISTORIAL COMPRIMIDO]\n${summaryText}\n[FIN]`,
  };

  return [...first, compressed, ...recent];
}

async function runCompactAgent(task: string): Promise<string> {
  // Loop con compactación automática cuando el contexto crece.
  const client = new Anthropic();
  let messages: Anthropic.MessageParam[] = [{ role: "user", content: task }];

  for (let iteration = 0; iteration < MAX_ITERATIONS; iteration++) {
    // Compactar si el contexto supera el umbral
    const currentTokens = estimateTokens(messages);
    if (currentTokens > CONTEXT_THRESHOLD) {
      messages = await compact(client, messages);
      console.log(
        `  [iter=${iteration + 1}] contexto compactado → ~${estimateTokens(messages)} tokens`
      );
    } else {
      console.log(`  [iter=${iteration + 1}] contexto ~${currentTokens} tokens`);
    }

    const response = await client.messages.create({
      model: MODEL,
      max_tokens: 4096,
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

    break;
  }

  return "[max iteraciones]";
}

(async () => {
  const result = await runCompactAgent("¿Qué hora es en Tokio (UTC+9) y cuánto es 47 + 89?");
  console.log(`\nRespuesta: ${result}`);
})();
