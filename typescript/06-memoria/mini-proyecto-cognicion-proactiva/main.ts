// Mini-proyecto: cognición proactiva con think() + instincts + UrgeQueue.
//
// Un agente con un loop de fondo que detecta patrones en el almacén de memoria
// y encola intenciones proactivas (UrgeSpec) que el LLM puede incorporar en el
// siguiente turno — aunque el usuario no haya preguntado por ellas.
//
// Cómo ejecutar:
//   export ANTHROPIC_API_KEY=...
//   make ts SCRIPT=typescript/06-memoria/mini-proyecto-cognicion-proactiva/main.ts
//
// Qué observar:
//   El terminal muestra [💭 N intención(es)] cuando hay urges activas.
//   El agente las incorpora naturalmente (o las ignora si no encajan).

import Anthropic from "@anthropic-ai/sdk";
import * as readline from "readline";

// ── Parámetros interactivos ────────────────────────────────────────────────

const THINK_INTERVAL_MS   = 5_000;  // frecuencia del loop de fondo (reduce a 2000 para urges más rápidas)
const MAX_URGES_POR_TURNO = 2;      // pon 0 para volver a un agente puramente reactivo
const COOLDOWN_DEFAULT_MS = 60_000; // reduce a 10000 para ver repetición de urges
const HALF_LIFE_MS        = 90_000; // cada 90s una memoria pierde el 50% de fuerza
const UMBRAL_DEBIL        = 0.35;

const MODEL = process.env.MODEL ?? "claude-haiku-4-5-20251001";

// ── Tipos ──────────────────────────────────────────────────────────────────

interface Memoria {
  id: string;
  contenido: string;
  tipo: string;
  fuerza: number;
  ultimoUso: number;
  creado: number;
}

interface UrgeSpec {
  cooldownKey: string;
  priority: number;   // 0.0–1.0
  message: string;
  cooldownMs: number;
}

interface UrgeEntry {
  spec: UrgeSpec;
  expiresAt: number;
}

// ── Almacén en memoria ─────────────────────────────────────────────────────
// Sin dependencia externa de SQLite — Map para demo en TypeScript.

const memorias = new Map<string, Memoria>();
const urgeQueue = new Map<string, UrgeEntry>();
const conversacion: { role: string; content: string; ts: number }[] = [];

function registrarMemoria(contenido: string, tipo = "hecho"): string {
  const id = Math.random().toString(36).slice(2, 10);
  memorias.set(id, {
    id,
    contenido,
    tipo,
    fuerza: 1.0,
    ultimoUso: Date.now(),
    creado: Date.now(),
  });
  return id;
}

function recuperarMemorias(limit = 5): Memoria[] {
  return [...memorias.values()]
    .filter((m) => m.fuerza > 0.1)
    .sort((a, b) => b.fuerza - a.fuerza)
    .slice(0, limit);
}

function aplicarDecay(): void {
  const now = Date.now();
  for (const m of memorias.values()) {
    const deltaMs = now - m.ultimoUso;
    m.fuerza = m.fuerza * Math.exp((-0.693 * deltaMs) / HALF_LIFE_MS);
  }
}

function memoriasDébiles(): Memoria[] {
  return [...memorias.values()]
    .filter((m) => m.fuerza < UMBRAL_DEBIL && m.fuerza > 0.05)
    .sort((a, b) => a.fuerza - b.fuerza)
    .slice(0, 3);
}

function contarMemorias(): number {
  return [...memorias.values()].filter((m) => m.fuerza > 0.1).length;
}

function temasRecientes(desde = Date.now() - 300_000): string[] {
  return conversacion
    .filter((c) => c.role === "user" && c.ts > desde)
    .map((c) => c.content)
    .slice(-5);
}

// ── UrgeQueue ──────────────────────────────────────────────────────────────

function encolarUrge(spec: UrgeSpec): void {
  const existing = urgeQueue.get(spec.cooldownKey);
  const expiresAt = Date.now() + spec.cooldownMs;
  if (!existing || spec.priority > existing.spec.priority) {
    urgeQueue.set(spec.cooldownKey, { spec, expiresAt });
  }
}

function extraerUrges(limit = MAX_URGES_POR_TURNO): string[] {
  const now = Date.now();
  const activas = [...urgeQueue.entries()]
    .filter(([, e]) => e.expiresAt > now)
    .sort(([, a], [, b]) => b.spec.priority - a.spec.priority)
    .slice(0, limit);

  const mensajes = activas.map(([key, e]) => {
    urgeQueue.delete(key);
    return e.spec.message;
  });
  return mensajes;
}

// ── Instincts ──────────────────────────────────────────────────────────────

function instinctMemoriaDebil(): UrgeSpec[] {
  const debiles = memoriasDébiles();
  if (debiles.length === 0) return [];
  const m = debiles[0];
  return [{
    cooldownKey: "memoria_debil",
    priority: 0.7,
    message: `[PROACTIVO] El recuerdo '${m.contenido.slice(0, 60)}' está perdiendo relevancia (fuerza: ${m.fuerza.toFixed(2)}). Menciónalo si el contexto lo permite.`,
    cooldownMs: COOLDOWN_DEFAULT_MS,
  }];
}

function instinctTemasPendientes(): UrgeSpec[] {
  const temas = temasRecientes();
  if (temas.length < 3) return [];
  return [{
    cooldownKey: "temas_pendientes",
    priority: 0.5,
    message: `[PROACTIVO] El usuario ha mencionado ${temas.length} temas distintos en esta sesión. ¿Hay algún hilo que quedó sin resolver?`,
    cooldownMs: COOLDOWN_DEFAULT_MS * 2,
  }];
}

function instinctCargaAlta(): UrgeSpec[] {
  const n = contarMemorias();
  if (n < 4) return [];
  return [{
    cooldownKey: "carga_alta",
    priority: 0.3,
    message: `[PROACTIVO] Tengo ${n} recuerdos activos sobre este usuario. Si pregunta sobre el pasado, tengo contexto relevante disponible.`,
    cooldownMs: COOLDOWN_DEFAULT_MS * 3,
  }];
}

// Comenta un instinct para desactivar ese tipo de proactividad
const INSTINCTS = [
  instinctMemoriaDebil,
  instinctTemasPendientes,
  instinctCargaAlta,
];

// ── BackgroundCognition ────────────────────────────────────────────────────

function iniciarBackgroundCognition(): ReturnType<typeof setInterval> {
  return setInterval(() => {
    try {
      aplicarDecay();
      for (const instinct of INSTINCTS) {
        for (const spec of instinct()) {
          encolarUrge(spec);
        }
      }
    } catch (e) {
      console.error("  [BackgroundCognition error]", e);
    }
  }, THINK_INTERVAL_MS);
}

// ── Loop de chat ───────────────────────────────────────────────────────────

function construirSystem(urges: string[], mems: Memoria[]): string {
  const partes = ["Eres un asistente con memoria persistente y cognición proactiva."];

  if (mems.length > 0) {
    const memTxt = mems.map((m) => `- ${m.contenido} (fuerza: ${m.fuerza.toFixed(2)})`).join("\n");
    partes.push(`\n## Memoria activa\n${memTxt}`);
  }

  if (urges.length > 0) {
    const urgeTxt = urges.map((u) => `- ${u}`).join("\n");
    partes.push(
      `\n## Intenciones proactivas\n${urgeTxt}\n\n` +
      "Incorpora estas intenciones de forma natural si el contexto lo permite. " +
      "Si no encajan con la pregunta del usuario, ignóralas."
    );
  }

  return partes.join("\n");
}

async function chat(): Promise<void> {
  const client = new Anthropic();
  const historial: { role: "user" | "assistant"; content: string }[] = [];
  const rl = readline.createInterface({ input: process.stdin, output: process.stdout });

  const preguntar = (prompt: string): Promise<string> =>
    new Promise((resolve) => rl.question(prompt, resolve));

  console.log(`\nAgente con cognición proactiva listo.`);
  console.log(`  Loop de fondo: cada ${THINK_INTERVAL_MS / 1000}s | Máx ${MAX_URGES_POR_TURNO} urges/turno`);
  console.log("  Escribe 'salir' para terminar.\n");

  while (true) {
    const entrada = (await preguntar("Tú: ")).trim();
    if (!entrada || ["salir", "exit", "quit"].includes(entrada.toLowerCase())) break;

    conversacion.push({ role: "user", content: entrada, ts: Date.now() });

    const urges = extraerUrges();
    if (urges.length > 0) {
      console.log(`\n  [💭 ${urges.length} intención(es) proactiva(s) activa(s)]`);
    }

    const mems = recuperarMemorias();
    const system = construirSystem(urges, mems);

    historial.push({ role: "user", content: entrada });

    const respuesta = await client.messages.create({
      model: MODEL,
      max_tokens: 1024,
      system,
      messages: historial,
    });

    const texto = (respuesta.content[0] as { type: "text"; text: string }).text;
    historial.push({ role: "assistant", content: texto });
    conversacion.push({ role: "assistant", content: texto, ts: Date.now() });

    if (entrada.length > 15) {
      registrarMemoria(`El usuario dijo: ${entrada.slice(0, 80)}`, "episodio");
    }

    console.log(`\nAgente: ${texto}\n`);
  }

  rl.close();
}

// ── Main ───────────────────────────────────────────────────────────────────

async function main(): Promise<void> {
  // Semilla de memorias para hacer el demo inmediatamente interesante
  registrarMemoria("El usuario prefiere respuestas concisas sin relleno", "preferencia");
  registrarMemoria("Proyecto activo: sistema de agentes con memoria distribuida", "proyecto");
  registrarMemoria("Tarea pendiente: revisar el diseño del ciclo de vida", "tarea");
  registrarMemoria("Nota de hace tiempo: el usuario usa TypeScript en producción", "episodio");

  const bgTimer = iniciarBackgroundCognition();
  try {
    await chat();
  } finally {
    clearInterval(bgTimer);
    console.log("[BackgroundCognition detenida]");
  }
}

main().catch(console.error);
