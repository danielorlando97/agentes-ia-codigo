// Cómo ejecutar: make ts SCRIPT=typescript/08-bucle/plan-execute.ts
/**
 * Plan-and-Execute: separa planificación de ejecución.
 *
 * El planificador genera una lista de pasos con una sola llamada.
 * El executor implementa cada paso con tool_use nativo.
 * Dynamic replanning: si un paso falla, el planificador se invoca de nuevo.
 *
 * Requiere: npm install @anthropic-ai/sdk
 */
import Anthropic from "@anthropic-ai/sdk";

const PLANNER_MODEL = process.env["PLANNER_MODEL"] ?? "claude-sonnet-4-6";
const EXECUTOR_MODEL = process.env["EXECUTOR_MODEL"] ?? "claude-haiku-4-5-20251001";

const PLANNER_PROMPT = `\
Genera una lista numerada de pasos atómicos para completar esta tarea.
Cada paso debe comenzar con un verbo de acción y ser ejecutable de forma independiente.

Tarea: {tarea}
Estado actual: {estado}

Responde solo con la lista numerada, sin explicaciones adicionales.`;

function parsearListaNumerada(texto: string): string[] {
  return [...texto.matchAll(/^\d+[.)]\s+(.+)$/gm)]
    .map((m) => m[1].trim())
    .filter(Boolean);
}

type Tool = Anthropic.Messages.Tool;
type ToolFn = (args: Record<string, unknown>) => string;

async function planificar(
  client: Anthropic,
  tarea: string,
  estado = "Sin ejecución previa"
): Promise<string[]> {
  const resp = await client.messages.create({
    model: PLANNER_MODEL,
    max_tokens: 600,
    messages: [
      {
        role: "user",
        content: PLANNER_PROMPT.replace("{tarea}", tarea).replace(
          "{estado}",
          estado
        ),
      },
    ],
  });
  const text = resp.content[0].type === "text" ? resp.content[0].text : "";
  return parsearListaNumerada(text);
}

async function ejecutarPaso(
  client: Anthropic,
  paso: string,
  contexto: string,
  tools: Tool[],
  toolFns: Record<string, ToolFn>
): Promise<[string, boolean]> {
  const messages: Anthropic.Messages.MessageParam[] = [
    {
      role: "user",
      content: `Contexto previo:\n${contexto}\n\nEjecuta este paso: ${paso}`,
    },
  ];

  while (true) {
    const resp = await client.messages.create({
      model: EXECUTOR_MODEL,
      max_tokens: 512,
      tools,
      messages,
    });

    if (resp.stop_reason === "end_turn") {
      const text = resp.content
        .filter((b) => b.type === "text")
        .map((b) => (b as Anthropic.Messages.TextBlock).text)
        .join("");
      return [text, true];
    }

    if (resp.stop_reason === "tool_use") {
      const results: Anthropic.Messages.ToolResultBlockParam[] = [];
      for (const b of resp.content) {
        if (b.type === "tool_use") {
          const fn = toolFns[b.name];
          const r = fn
            ? fn(b.input as Record<string, unknown>)
            : `[tool '${b.name}' no encontrada]`;
          results.push({ type: "tool_result", tool_use_id: b.id, content: r });
        }
      }
      messages.push({ role: "assistant", content: resp.content });
      messages.push({ role: "user", content: results });
    } else {
      return ["[paso no completado]", false];
    }
  }
}

async function runPlanAndExecute(
  client: Anthropic,
  tarea: string,
  tools: Tool[],
  toolFns: Record<string, ToolFn>,
  maxReplan = 2
): Promise<string> {
  let plan = await planificar(client, tarea);
  console.log(`Plan (${plan.length} pasos):`);
  plan.forEach((p, i) => console.log(`  ${i + 1}. ${p}`));

  const resultados: string[] = [];
  let i = 0;
  let replans = 0;

  while (i < plan.length) {
    const contexto = resultados.map((r, j) => `Paso ${j + 1}: ${r}`).join("\n");
    const [resultado, exito] = await ejecutarPaso(
      client,
      plan[i],
      contexto,
      tools,
      toolFns
    );
    console.log(`\n[paso ${i + 1}/${plan.length}] ${exito ? "✓" : "✗"} ${plan[i].slice(0, 60)}`);

    if (!exito && replans < maxReplan) {
      const estado = `Completados: ${JSON.stringify(resultados)}\nFalló: ${plan[i]}`;
      const nuevo = await planificar(client, tarea, estado);
      if (nuevo.length) {
        plan = [...plan.slice(0, i), ...nuevo];
        replans++;
        console.log(`  → replan #${replans}: ${nuevo.length} pasos nuevos`);
        continue;
      }
    }

    resultados.push(resultado);
    i++;
  }

  const sintesis = await client.messages.create({
    model: PLANNER_MODEL,
    max_tokens: 400,
    messages: [
      {
        role: "user",
        content:
          `Tarea: ${tarea}\n\nResultados:\n${resultados.join("\n")}\n\n` +
          "Resume qué se logró en 2-3 frases.",
      },
    ],
  });
  return sintesis.content[0].type === "text" ? sintesis.content[0].text : "";
}

// Demo
(async () => {
  const client = new Anthropic();

  const tools: Tool[] = [
    {
      name: "calcular",
      description: "Evalúa una expresión matemática simple.",
      input_schema: {
        type: "object" as const,
        properties: { expresion: { type: "string", description: "ej: '15 * 8'" } },
        required: ["expresion"],
      },
    },
  ];

  function calcular({ expresion }: Record<string, unknown>): string {
    try {
      // eslint-disable-next-line no-new-func
      return String(new Function(`return ${expresion}`)());
    } catch (e) {
      return `Error: ${e}`;
    }
  }

  const resultado = await runPlanAndExecute(
    client,
    "Calcula el área de un rectángulo de 15 por 8 metros. " +
      "Luego calcula cuántas baldosas de 0.25 m² se necesitan para cubrirlo.",
    tools,
    { calcular }
  );
  console.log(`\n=== Resultado final ===\n${resultado}`);
})();
