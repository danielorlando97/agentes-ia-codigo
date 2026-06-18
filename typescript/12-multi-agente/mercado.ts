// Patrón Mercado/Subasta: el orquestador descompone la tarea, consulta a los workers
// por bids (confianza + tokens estimados), asigna según utilidad = confianza/tokens.

// Cómo ejecutar: make ts SCRIPT=typescript/12-multi-agente/mercado.ts

import Anthropic from "@anthropic-ai/sdk";

const client = new Anthropic();
const MODEL = process.env["MODEL"] ?? "claude-haiku-4-5-20251001";

interface Worker {
  id: string;
  capabilities: string[];
  currentLoad: number;
  systemPrompt: string;
}

interface Bid {
  workerId: string;
  confidence: number;
  estimatedTokens: number;
}

function utility(bid: Bid): number {
  if (bid.estimatedTokens <= 0) return 0;
  return bid.confidence / bid.estimatedTokens;
}

function makeWorker(id: string, capabilities: string[]): Worker {
  const caps = capabilities.join(", ");
  return {
    id,
    capabilities,
    currentLoad: 0,
    systemPrompt: `Eres el worker ${id}. Tus capacidades son: ${caps}. Ejecuta las tareas que te asignen con precisión.`,
  };
}

async function llamarLLM(system: string, user: string, temperature = 0.0): Promise<string> {
  const resp = await client.messages.create({
    model: MODEL,
    max_tokens: 800,
    system,
    messages: [{ role: "user", content: user }],
    temperature,
  });
  return (resp.content[0] as { type: "text"; text: string }).text.trim();
}

async function orquestadorDescomponer(tarea: string): Promise<Array<{ id: string; descripcion: string; capacidad_requerida: string }>> {
  const system =
    "Eres un orquestador. Descompone la tarea en subtareas concretas. " +
    "Para cada subtarea especifica la capacidad necesaria. " +
    'Responde SOLO con JSON válido: [{"id": "s1", "descripcion": "...", "capacidad_requerida": "..."}, ...]';
  const raw = await llamarLLM(system, `Tarea: ${tarea}`);
  const inicio = raw.indexOf("[");
  const fin = raw.lastIndexOf("]") + 1;
  return JSON.parse(raw.slice(inicio, fin));
}

async function solicitarBid(worker: Worker, subtarea: { id: string; descripcion: string; capacidad_requerida: string }): Promise<Bid> {
  const system =
    `Eres el worker ${worker.id}. ` +
    `Tus capacidades: ${worker.capabilities.join(", ")}. ` +
    `Carga actual: ${worker.currentLoad} tareas. ` +
    "Evalúa si puedes ejecutar la subtarea. " +
    'Responde SOLO con JSON: {"confidence": <0.0-1.0>, "estimated_tokens": <int>}. ' +
    "Si no puedes, confidence debe ser 0.0.";
  try {
    const raw = await llamarLLM(system, `Subtarea: ${subtarea.descripcion}\nCapacidad requerida: ${subtarea.capacidad_requerida}`);
    const inicio = raw.indexOf("{");
    const fin = raw.lastIndexOf("}") + 1;
    const parsed = JSON.parse(raw.slice(inicio, fin));
    return {
      workerId: worker.id,
      confidence: Number(parsed.confidence ?? 0),
      estimatedTokens: Math.max(1, Number(parsed.estimated_tokens ?? 200)),
    };
  } catch {
    return { workerId: worker.id, confidence: 0, estimatedTokens: 1 };
  }
}

async function ejecutarSubtarea(worker: Worker, subtarea: { descripcion: string }, contexto: string): Promise<string> {
  const user = `Subtarea a ejecutar: ${subtarea.descripcion}\n\nContexto disponible:\n${contexto || "(ninguno)"}`;
  return llamarLLM(worker.systemPrompt, user);
}

async function orquestadorSintetizar(tarea: string, resultados: Record<string, string>): Promise<string> {
  const system = "Eres un orquestador. Sintetiza los resultados de las subtareas en una respuesta final coherente.";
  const resumenResultados = Object.entries(resultados)
    .map(([id, r]) => `Subtarea ${id}:\n${r}`)
    .join("\n\n");
  return llamarLLM(system, `Tarea original: ${tarea}\n\nResultados de subtareas:\n${resumenResultados}`);
}

async function mercado(tarea: string, workers: Worker[]): Promise<string> {
  console.log(`[Mercado] Descomponiendo tarea: ${tarea}`);
  const subtareas = await orquestadorDescomponer(tarea);
  console.log(`  → ${subtareas.length} subtareas identificadas`);

  const resultados: Record<string, string> = {};

  for (const subtarea of subtareas) {
    console.log(`\n[Licitación] Subtarea: ${subtarea.descripcion}`);
    const workersDisponibles = workers.filter(w => w.currentLoad < 3);
    const bids = await Promise.all(
      workersDisponibles.map(w => solicitarBid(w, subtarea))
    );

    const bidsFiltrados = bids.filter(b => b.confidence > 0.1);
    if (bidsFiltrados.length === 0) {
      console.log("  ✗ Ningún worker disponible para esta subtarea");
      resultados[subtarea.id] = "[Sin worker disponible]";
      continue;
    }

    const mejor = bidsFiltrados.reduce((a, b) => utility(a) > utility(b) ? a : b);
    const workerAsignado = workers.find(w => w.id === mejor.workerId)!;
    console.log(`  → Asignado a ${workerAsignado.id} (confianza=${mejor.confidence.toFixed(2)}, tokens_est=${mejor.estimatedTokens})`);

    workerAsignado.currentLoad++;
    const contexto = Object.entries(resultados)
      .map(([id, r]) => `${id}: ${r}`)
      .join("\n");
    resultados[subtarea.id] = await ejecutarSubtarea(workerAsignado, subtarea, contexto);
    workerAsignado.currentLoad--;
    console.log(`  ✓ Completada: ${resultados[subtarea.id].slice(0, 60)}...`);
  }

  console.log("\n[Síntesis] Combinando resultados...");
  return orquestadorSintetizar(tarea, resultados);
}

async function main() {
  const workers: Worker[] = [
    makeWorker("W1", ["búsqueda web", "síntesis de información", "redacción"]),
    makeWorker("W2", ["análisis de datos", "estadísticas", "cálculos"]),
    makeWorker("W3", ["búsqueda web", "análisis competitivo", "comparación"]),
  ];

  const tarea = "Investiga y compara las principales características de los tres frameworks web de Python más populares (Django, FastAPI, Flask) para un equipo de startup de 5 personas.";
  console.log(`Tarea: ${tarea}\n`);

  const resultado = await mercado(tarea, workers);
  console.log(`\nResultado final:\n${resultado}`);
}

main().catch(console.error);
