// Integración de Mem0 como capa de memoria para un agente existente.
//
// Mem0 extrae memorias automáticamente de cada turno conversacional
// mediante un LLM auxiliar. La integración mínima requiere 3 llamadas:
// add() al final de cada turno, search() al inicio, getAll() para contexto completo.
//
// Requiere:
//   export MEM0_API_KEY=...
//   export ANTHROPIC_API_KEY=...
//
// Cómo ejecutar:
//   export MEM0_API_KEY=tu-clave
//   export ANTHROPIC_API_KEY=tu-clave
//   make ts SCRIPT=typescript/06-memoria/20-implementaciones/mem0_integration.ts
//
// Qué esperar:
//   El agente extrae memorias automáticamente de cada turno. Muestra el ciclo
//   add() → search() → getAll() en acción.

import Anthropic from "@anthropic-ai/sdk";

const MEM0_BASE = "https://api.mem0.ai/v1";
const MEM0_API_KEY = process.env.MEM0_API_KEY ?? "";
const MODEL = process.env.MODEL ?? "claude-sonnet-4-6";
const USER_ID = "usuario-demo-ts";

interface Mem0Memory {
  id: string;
  memory: string;
}

function mem0Headers() {
  return {
    Authorization: `Token ${MEM0_API_KEY}`,
    "Content-Type": "application/json",
  };
}

async function mem0Add(messages: Array<{ role: string; content: string }>, userId: string): Promise<void> {
  const res = await fetch(`${MEM0_BASE}/memories/`, {
    method: "POST",
    headers: mem0Headers(),
    body: JSON.stringify({ messages, user_id: userId }),
  });
  if (!res.ok) throw new Error(`Mem0 add error ${res.status}: ${await res.text()}`);
}

async function mem0Search(query: string, userId: string, limit = 5): Promise<Mem0Memory[]> {
  const params = new URLSearchParams({ query, user_id: userId, limit: String(limit) });
  const res = await fetch(`${MEM0_BASE}/memories/search/?${params}`, {
    headers: { Authorization: `Token ${MEM0_API_KEY}` },
  });
  if (!res.ok) throw new Error(`Mem0 search error ${res.status}`);
  return (await res.json()) as Mem0Memory[];
}

async function mem0GetAll(userId: string): Promise<Mem0Memory[]> {
  const params = new URLSearchParams({ user_id: userId });
  const res = await fetch(`${MEM0_BASE}/memories/?${params}`, {
    headers: { Authorization: `Token ${MEM0_API_KEY}` },
  });
  if (!res.ok) throw new Error(`Mem0 getAll error ${res.status}`);
  return (await res.json()) as Mem0Memory[];
}

async function recuperarContexto(query: string): Promise<string> {
  const resultados = await mem0Search(query, USER_ID);
  if (!resultados.length) return "";
  const lineas = resultados.map((r) => `- ${r.memory}`);
  return "## Memoria recuperada\n" + lineas.join("\n");
}

async function turno(
  historial: Array<{ role: string; content: string }>,
  mensaje: string
): Promise<string> {
  const cliente = new Anthropic();

  // 1. Recuperar contexto relevante antes de responder
  const contextoMemoria = await recuperarContexto(mensaje);
  let system = "Eres un asistente técnico.";
  if (contextoMemoria) system += `\n\n${contextoMemoria}`;

  historial.push({ role: "user", content: mensaje });
  const respuestaApi = await cliente.messages.create({
    model: MODEL,
    max_tokens: 1024,
    system,
    messages: historial,
  });
  const respuesta = (respuestaApi.content[0] as { text: string }).text;
  historial.push({ role: "assistant", content: respuesta });

  // 2. Guardar el turno para sesiones futuras (post-turno — fuera del hot path ideal)
  await mem0Add([{ role: "user", content: mensaje }, { role: "assistant", content: respuesta }], USER_ID);
  return respuesta;
}

// ── main ──────────────────────────────────────────────────────────────────

if (!MEM0_API_KEY) {
  console.error("MEM0_API_KEY no configurada. Exporta la clave y reintenta.");
  process.exit(1);
}
if (!process.env.ANTHROPIC_API_KEY) {
  console.error("ANTHROPIC_API_KEY no configurada.");
  process.exit(1);
}

const historial: Array<{ role: string; content: string }> = [];

// Turno 1: el agente aprende la preferencia
const r1 = await turno(historial, "Prefiero trabajar con Python 3.12 en producción.");
console.log(`Agente: ${r1.slice(0, 120)}\n`);

// Turno 2: nueva sesión — historial vacío, pero Mem0 recupera la preferencia
const historialNuevo: Array<{ role: string; content: string }> = [];
const r2 = await turno(historialNuevo, "¿Qué lenguaje debería usar para el nuevo servicio?");
console.log(`Agente (sesión nueva): ${r2.slice(0, 120)}`);

// Ver todas las memorias guardadas
console.log("\n--- memorias almacenadas ---");
for (const m of await mem0GetAll(USER_ID)) {
  console.log(`  ${m.memory}`);
}
