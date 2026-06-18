// Cómo ejecutar: make ts SCRIPT=typescript/09-planificacion/replanificacion.ts
/**
 * Replanificación dinámica: detecta divergencia tras cada paso y replantea los restantes.
 *
 * El evaluador LLM juzga si cada resultado permite continuar al paso siguiente.
 * Si no, el replanificador regenera los pasos pendientes sin repetir los ya completados.
 * maxReplans=3 previene el loop infinito documentado en AutoGPT.
 *
 * Requiere: npm install @anthropic-ai/sdk
 */
import Anthropic from "@anthropic-ai/sdk";

const MODEL = process.env["MODEL"] ?? "claude-sonnet-4-6";
const MAX_REPLANS = 3;

const PROMPT_PLAN = (tarea: string) => `\
Genera una lista numerada de pasos atómicos para completar esta tarea.
Cada paso debe comenzar con un verbo y ser ejecutable de forma independiente.
Tarea: ${tarea}
Responde solo con la lista numerada.`;

const PROMPT_EXECUTOR = (contexto: string, paso: string) => `\
Contexto de pasos ya completados:
${contexto}

Ejecuta este paso y devuelve el resultado como texto conciso (máx 100 palabras):
${paso}`;

const PROMPT_EVALUADOR = (paso: string, resultado: string, proximo: string) => `\
Paso ejecutado: ${paso}
Resultado obtenido: ${resultado}
Próximo paso del plan: ${proximo}

¿El resultado permite ejecutar el próximo paso?
Responde SOLO con una de estas palabras: SATISFACE | NO_SATISFACE
Si NO_SATISFACE, añade en la misma línea: | <razón breve>`;

const PROMPT_REPLAN = (
  tarea: string,
  completados: string,
  pasoFallido: string,
  resultadoFallido: string,
  razon: string
) => `\
Tarea original: ${tarea}

Pasos ya completados exitosamente:
${completados}

Paso que falló: ${pasoFallido}
Resultado fallido: ${resultadoFallido}
Razón del fallo: ${razon}

Genera una lista numerada con los pasos RESTANTES para completar la tarea.
No repitas los pasos ya completados. Responde solo con la lista numerada.`;

const PROMPT_SINTESIS = (tarea: string, historial: string) => `\
Tarea original: ${tarea}

Historial de ejecución:
${historial}

Genera la respuesta final integrando todos los resultados.`;

interface EntradaHistorial {
  paso: string;
  resultado: string;
  estado: "OK" | "PARCIAL";
}

function parsearLista(texto: string): string[] {
  return [...texto.matchAll(/^\d+[.)]\s+(.+)$/gm)]
    .map((m) => m[1].trim())
    .filter(Boolean);
}

async function llmCall(
  client: Anthropic,
  prompt: string,
  maxTokens = 400
): Promise<string> {
  const resp = await client.messages.create({
    model: MODEL,
    max_tokens: maxTokens,
    messages: [{ role: "user", content: prompt }],
  });
  return resp.content[0].type === "text" ? resp.content[0].text.trim() : "";
}

async function ejecutarPaso(
  client: Anthropic,
  paso: string,
  historial: EntradaHistorial[]
): Promise<string> {
  const contexto =
    historial.length > 0
      ? historial.map((e) => `[${e.paso.slice(0, 40)}] → ${e.resultado.slice(0, 80)}`).join("\n")
      : "(sin pasos previos)";
  return llmCall(client, PROMPT_EXECUTOR(contexto, paso), 200);
}

async function evaluarDivergencia(
  client: Anthropic,
  paso: string,
  resultado: string,
  proximo: string
): Promise<[boolean, string]> {
  if (!proximo) return [true, ""];
  const resp = await llmCall(
    client,
    PROMPT_EVALUADOR(paso, resultado, proximo),
    60
  );
  const satisface = resp.toUpperCase().startsWith("SATISFACE");
  const razon = resp.includes("|") ? resp.split("|")[1].trim() : "";
  return [satisface, razon];
}

async function replanificar(
  client: Anthropic,
  tarea: string,
  historial: EntradaHistorial[],
  pasoFallido: string,
  resultadoFallido: string,
  razon: string
): Promise<string[]> {
  const completados =
    historial.length > 0
      ? historial.map((e) => `- ${e.paso}`).join("\n")
      : "(ninguno)";
  const resp = await llmCall(
    client,
    PROMPT_REPLAN(tarea, completados, pasoFallido, resultadoFallido, razon),
    400
  );
  return parsearLista(resp);
}

async function planExecuteDynamic(
  tarea: string,
  client: Anthropic
): Promise<string> {
  // 1. Generar plan inicial
  let plan = parsearLista(
    await llmCall(client, PROMPT_PLAN(tarea))
  );
  console.log(`Plan inicial (${plan.length} pasos):`);
  plan.forEach((p, i) => console.log(`  ${i + 1}. ${p.slice(0, 70)}`));

  const historial: EntradaHistorial[] = [];
  let replans = 0;
  let i = 0;

  while (i < plan.length) {
    const paso = plan[i];
    const proximo = i + 1 < plan.length ? plan[i + 1] : "";

    const resultado = await ejecutarPaso(client, paso, historial);
    const [satisface, razon] = await evaluarDivergencia(client, paso, resultado, proximo);

    console.log(`\n[paso ${i + 1}/${plan.length}] ${satisface ? "✓" : "✗"} ${paso.slice(0, 60)}`);

    if (satisface) {
      historial.push({ paso, resultado, estado: "OK" });
      i++;
    } else if (replans < MAX_REPLANS) {
      console.log(`  → Divergencia: ${razon || "(sin razón explícita)"}`);
      const nuevosPasos = await replanificar(
        client, tarea, historial, paso, resultado, razon
      );
      plan = [...historial.map((e) => e.paso), ...nuevosPasos];
      replans++;
      console.log(`  → Replan #${replans}: ${nuevosPasos.length} pasos nuevos desde paso ${i + 1}`);
    } else {
      historial.push({ paso, resultado, estado: "PARCIAL" });
      i++;
    }
  }

  // 2. Síntesis final
  const historialStr = historial
    .map((e) => `[${e.estado}] ${e.paso}: ${e.resultado.slice(0, 100)}`)
    .join("\n");
  return llmCall(client, PROMPT_SINTESIS(tarea, historialStr), 500);
}

// Demo
(async () => {
  const client = new Anthropic();
  const tarea =
    "Calcula cuántos días hay entre el 1 de enero y el 1 de julio de 2025, " +
    "luego calcula cuántas semanas y cuántos meses aproximados representa.";
  console.log(`Tarea: ${tarea}\n`);
  const resultado = await planExecuteDynamic(tarea, client);
  console.log(`\n=== Resultado final ===\n${resultado}`);
})();
