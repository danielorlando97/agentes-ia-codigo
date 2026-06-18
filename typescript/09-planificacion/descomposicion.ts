// Cómo ejecutar: make ts SCRIPT=typescript/09-planificacion/descomposicion.ts
/**
 * Descomposición de tareas con DAG explícito y ejecución paralela.
 *
 * El planificador LLM genera subtareas con dependencias;
 * el executor las resuelve en oleadas paralelas con Promise.all().
 *
 * Requiere: npm install @anthropic-ai/sdk
 */
import Anthropic from "@anthropic-ai/sdk";

const MODEL_PLANNER = process.env["PLANNER_MODEL"] ?? "claude-sonnet-4-6";
const MODEL_EXECUTOR = process.env["EXECUTOR_MODEL"] ?? "claude-haiku-4-5-20251001";

const PROMPT_PLANIFICADOR = (tarea: string) => `\
Descompón la siguiente tarea en subtareas atómicas.
Responde ÚNICAMENTE con un array JSON válido, sin texto adicional.
Cada elemento debe tener:
  "id": string único (S1, S2, ...),
  "objetivo": string de una oración con el objetivo de la subtarea,
  "deps": array de IDs que deben completarse primero ([] si ninguna)

Regla: maximiza las subtareas con deps=[] (ejecutables en paralelo desde el inicio).
Tarea: ${tarea}`;

const PROMPT_EXECUTOR = (contexto: string, objetivo: string) => `\
Contexto de subtareas ya completadas:
${contexto}

Ejecuta esta subtarea y devuelve el resultado como texto conciso (máx 150 palabras):
${objetivo}`;

const PROMPT_SINTESIS = (tarea: string, resultados: string) => `\
Tarea original: ${tarea}

Resultados de cada subtarea:
${resultados}

Genera la respuesta final integrando todos los resultados. Sé conciso.`;

interface Subtarea {
  id: string;
  objetivo: string;
  deps: string[];
}

function parsearPlan(texto: string): Subtarea[] {
  const m = texto.match(/\[[\s\S]*\]/);
  if (!m) throw new Error(`No se encontró array JSON:\n${texto.slice(0, 300)}`);
  const datos = JSON.parse(m[0]);
  return datos.map((item: Record<string, unknown>) => ({
    id: String(item.id),
    objetivo: String(item.objetivo),
    deps: (Array.isArray(item.deps) ? item.deps : []).map(String),
  }));
}

function validarPlan(plan: Subtarea[]): void {
  const ids = new Set(plan.map((s) => s.id));
  for (const s of plan) {
    for (const dep of s.deps) {
      if (!ids.has(dep))
        throw new Error(`Subtarea ${s.id} depende de '${dep}' que no existe`);
    }
  }
}

async function ejecutarSubtarea(
  subtarea: Subtarea,
  resultados: Map<string, string>,
  client: Anthropic
): Promise<[string, string]> {
  const contexto =
    resultados.size > 0
      ? [...resultados.entries()].map(([k, v]) => `[${k}] ${v}`).join("\n")
      : "(ninguno)";

  const resp = await client.messages.create({
    model: MODEL_EXECUTOR,
    max_tokens: 300,
    messages: [
      {
        role: "user",
        content: PROMPT_EXECUTOR(contexto, subtarea.objetivo),
      },
    ],
  });

  const resultado =
    resp.content[0].type === "text" ? resp.content[0].text.trim() : "";
  return [subtarea.id, resultado];
}

async function ejecutarDag(
  plan: Subtarea[],
  client: Anthropic
): Promise<Map<string, string>> {
  const resultados = new Map<string, string>();
  const completadas = new Set<string>();
  let pendientes = [...plan];

  while (pendientes.length > 0) {
    const ejecutables = pendientes.filter((s) =>
      s.deps.every((d) => completadas.has(d))
    );

    if (!ejecutables.length) {
      const bloqueadas = pendientes.map((s) => s.id);
      throw new Error(`Plan bloqueado — sin ejecutables: ${bloqueadas}`);
    }

    console.log(`  [oleada] paralelo: ${ejecutables.map((s) => s.id)}`);

    const nuevos = await Promise.all(
      ejecutables.map((s) => ejecutarSubtarea(s, resultados, client))
    );

    for (const [sid, resultado] of nuevos) {
      resultados.set(sid, resultado);
      completadas.add(sid);
      console.log(`    ${sid} ✓ ${resultado.slice(0, 60)}...`);
    }

    pendientes = pendientes.filter((s) => !completadas.has(s.id));
  }

  return resultados;
}

async function descomponerYEjecutar(
  tarea: string,
  client: Anthropic
): Promise<string> {
  // 1. Planificar
  const planResp = await client.messages.create({
    model: MODEL_PLANNER,
    max_tokens: 800,
    messages: [{ role: "user", content: PROMPT_PLANIFICADOR(tarea) }],
  });
  const planTexto =
    planResp.content[0].type === "text" ? planResp.content[0].text : "";
  const plan = parsearPlan(planTexto);
  validarPlan(plan);

  console.log(`Plan generado (${plan.length} subtareas):`);
  for (const s of plan) {
    const depsStr = s.deps.length ? ` [deps: ${s.deps}]` : " [sin deps]";
    console.log(`  ${s.id}: ${s.objetivo.slice(0, 60)}${depsStr}`);
  }

  // 2. Ejecutar DAG
  console.log("\nEjecutando DAG:");
  const resultados = await ejecutarDag(plan, client);

  // 3. Sintetizar
  const resultadosStr = [...resultados.entries()]
    .map(([k, v]) => `[${k}] ${v}`)
    .join("\n");

  const finalResp = await client.messages.create({
    model: MODEL_PLANNER,
    max_tokens: 600,
    messages: [{ role: "user", content: PROMPT_SINTESIS(tarea, resultadosStr) }],
  });

  return finalResp.content[0].type === "text"
    ? finalResp.content[0].text.trim()
    : "";
}

// Demo
(async () => {
  const client = new Anthropic();
  const tarea =
    "Escribe un breve análisis comparativo de Python vs TypeScript para " +
    "desarrollo de agentes IA: (1) ecosistema de librerías, " +
    "(2) rendimiento async, (3) tipado y mantenibilidad.";

  console.log(`Tarea: ${tarea}\n`);
  const resultado = await descomponerYEjecutar(tarea, client);
  console.log(`\n=== Resultado final ===\n${resultado}`);
})();
