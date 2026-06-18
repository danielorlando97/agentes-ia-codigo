// Cómo ejecutar: make ts SCRIPT=typescript/09-planificacion/supervisor_worker.ts
/**
 * Patrón Supervisor/Worker: despacho paralelo con workers especializados.
 *
 * El supervisor genera un plan de subtareas independientes; cada worker
 * recibe su propia tarea con contexto aislado y system prompt especializado.
 * Todos corren en paralelo con Promise.all() — sin DAG.
 *
 * Requiere: npm install @anthropic-ai/sdk
 */
import Anthropic from "@anthropic-ai/sdk";

const MODEL_SUPERVISOR = process.env["MODEL"] ?? "claude-sonnet-4-6";
const MODEL_WORKER = process.env["SMALL_MODEL"] ?? "claude-haiku-4-5-20251001";

const WORKER_SYSTEMS: Record<string, string> = {
  analista:
    "Eres un analista técnico. Responde con datos concretos y estructura clara.",
  investigador:
    "Eres un investigador especializado. Cita benchmarks y referencias cuando existan.",
  arquitecto:
    "Eres un arquitecto de software. Enfócate en decisiones de diseño y tradeoffs reales.",
  critico:
    "Eres un crítico técnico. Señala limitaciones, casos borde y riesgos concretos.",
};

const DEFAULT_SYSTEM =
  "Eres un asistente técnico especializado. Responde de forma concisa y estructurada.";

const PROMPT_PLAN = (tarea: string) => `\
Descompón la siguiente tarea en subtareas independientes que puedan ejecutarse en paralelo.
Responde ÚNICAMENTE con un array JSON válido, sin texto adicional.
Cada elemento debe tener:
  "id": string único (W1, W2, ...),
  "descripcion": objetivo concreto para el worker (una oración + criterios de output),
  "tipo_worker": uno de ["analista", "investigador", "arquitecto", "critico"]

Tarea: ${tarea}`;

const PROMPT_SINTESIS = (tarea: string, resultados: string) => `\
Tarea original: ${tarea}

Resultados de los workers:
${resultados}

Sintetiza una respuesta final integrando todos los resultados. Sé conciso y directo.`;

interface Subtarea {
  id: string;
  descripcion: string;
  tipo_worker: string;
}

function parsearPlan(texto: string): Subtarea[] {
  const m = texto.match(/\[[\s\S]*\]/);
  if (!m) throw new Error(`No se encontró array JSON:\n${texto.slice(0, 300)}`);
  const datos = JSON.parse(m[0]) as Record<string, unknown>[];
  return datos.map((item) => ({
    id: String(item.id),
    descripcion: String(item.descripcion),
    tipo_worker: String(item.tipo_worker ?? "analista"),
  }));
}

async function ejecutarWorker(
  subtarea: Subtarea,
  client: Anthropic
): Promise<[string, string]> {
  const system = WORKER_SYSTEMS[subtarea.tipo_worker] ?? DEFAULT_SYSTEM;
  const resp = await client.messages.create({
    model: MODEL_WORKER,
    max_tokens: 300,
    system,
    messages: [{ role: "user", content: subtarea.descripcion }],
  });
  const resultado =
    resp.content[0].type === "text" ? resp.content[0].text.trim() : "";
  return [subtarea.id, resultado];
}

async function supervisorWorker(
  tarea: string,
  client: Anthropic
): Promise<string> {
  // 1. Supervisor genera plan
  const planResp = await client.messages.create({
    model: MODEL_SUPERVISOR,
    max_tokens: 600,
    messages: [{ role: "user", content: PROMPT_PLAN(tarea) }],
  });
  const planTexto =
    planResp.content[0].type === "text" ? planResp.content[0].text : "";
  const plan = parsearPlan(planTexto);

  console.log(`Plan (${plan.length} workers):`);
  for (const s of plan) {
    console.log(`  ${s.id} [${s.tipo_worker}]: ${s.descripcion.slice(0, 65)}`);
  }

  // 2. Todos los workers en paralelo — contexto aislado por worker
  console.log("\nDispatcheando workers en paralelo...");
  const pares = await Promise.all(
    plan.map((s) => ejecutarWorker(s, client))
  );
  const resultados = new Map(pares);

  for (const [wid, r] of resultados) {
    console.log(`  ${wid} ✓ ${r.slice(0, 60)}...`);
  }

  // 3. Supervisor consolida
  const resultadosStr = [...resultados.entries()]
    .map(([wid, r]) => `[${wid}] ${r}`)
    .join("\n");
  const finalResp = await client.messages.create({
    model: MODEL_SUPERVISOR,
    max_tokens: 500,
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
    "Evalúa si Python o TypeScript es mejor para construir agentes IA en 2025: " +
    "considera ecosistema de librerías, rendimiento async, tipado y facilidad de debugging.";

  console.log(`Tarea: ${tarea}\n`);
  const resultado = await supervisorWorker(tarea, client);
  console.log(`\n=== Síntesis final ===\n${resultado}`);
})();
