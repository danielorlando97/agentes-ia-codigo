// Integración con Letta via REST API: agente que gestiona su propia memoria.
//
// En Letta, el LLM controla explícitamente qué guardar y qué recuperar
// mediante tool calls de memoria nativas (core_memory_replace,
// archival_memory_insert, archival_memory_search). El agente decide
// cuándo acceder a la memoria — no hay extracción automática.
//
// Requiere:
//   export LETTA_API_KEY=...   (para cloud Letta)
//   # o letta server --port 8283  (para local — base_url = http://localhost:8283)
//
// Cómo ejecutar:
//   export LETTA_API_KEY=tu-clave
//   make ts SCRIPT=typescript/06-memoria/20-implementaciones/letta_integration.ts
//
// Qué esperar:
//   El agente gestiona su propia memoria: decide qué guardar y qué recuperar
//   usando herramientas de memoria nativas. Crea el agente, envía dos turnos
//   y muestra el estado final de core_memory.

const LETTA_BASE = process.env.LETTA_BASE_URL ?? "https://api.letta.ai";
const LETTA_API_KEY = process.env.LETTA_API_KEY ?? "";
const MODEL = process.env.MODEL ?? "claude-sonnet-4-6";

interface LettaHeaders {
  "Content-Type": string;
  Authorization?: string;
}

function lettaHeaders(): LettaHeaders {
  const h: LettaHeaders = { "Content-Type": "application/json" };
  if (LETTA_API_KEY) h.Authorization = `Bearer ${LETTA_API_KEY}`;
  return h;
}

async function crearAgente(): Promise<string> {
  const res = await fetch(`${LETTA_BASE}/v1/agents/`, {
    method: "POST",
    headers: lettaHeaders(),
    body: JSON.stringify({
      name: "asistente-tecnico-ts",
      model: MODEL,
      embedding: "letta-free",
      memory: {
        memory_blocks: [
          { label: "human", value: "El usuario es un desarrollador de software.", limit: 5000 },
          {
            label: "persona",
            value:
              "Eres un asistente técnico que recuerda las preferencias del usuario y las usa para dar respuestas personalizadas.",
            limit: 5000,
          },
        ],
      },
    }),
  });

  if (!res.ok) {
    const txt = await res.text();
    throw new Error(`Letta API error ${res.status}: ${txt.slice(0, 200)}`);
  }
  const data = (await res.json()) as { id: string };
  return data.id;
}

async function enviarMensaje(agentId: string, mensaje: string): Promise<string> {
  const res = await fetch(`${LETTA_BASE}/v1/agents/${agentId}/messages`, {
    method: "POST",
    headers: lettaHeaders(),
    body: JSON.stringify({
      messages: [{ role: "user", content: mensaje }],
    }),
  });

  if (!res.ok) throw new Error(`Error ${res.status}: ${await res.text()}`);
  const data = (await res.json()) as { messages: Array<{ message_type?: string; content?: string }> };

  const textos = data.messages
    .filter((m) => m.message_type === "assistant_message" && m.content)
    .map((m) => m.content!);
  return textos.join(" ") || "[sin respuesta de texto]";
}

async function verMemoriaCore(agentId: string): Promise<{ human: string; persona: string }> {
  const res = await fetch(`${LETTA_BASE}/v1/agents/${agentId}/core-memory`, {
    headers: lettaHeaders(),
  });
  if (!res.ok) throw new Error(`Error ${res.status}`);
  const data = (await res.json()) as { memory: Record<string, { value?: string }> };
  return {
    human: data.memory?.human?.value ?? "",
    persona: data.memory?.persona?.value ?? "",
  };
}

async function eliminarAgente(agentId: string): Promise<void> {
  await fetch(`${LETTA_BASE}/v1/agents/${agentId}`, {
    method: "DELETE",
    headers: lettaHeaders(),
  });
}

// ── main ──────────────────────────────────────────────────────────────────

if (!LETTA_API_KEY) {
  console.error("LETTA_API_KEY no configurada.");
  console.error("Exporta la clave o inicia letta server --port 8283 y configura LETTA_BASE_URL.");
  process.exit(1);
}

console.log("Creando agente Letta...");
const agentId = await crearAgente();
console.log(`Agente creado: ${agentId}\n`);

// Turno 1: el agente aprende la preferencia y la guarda en core_memory
const r1 = await enviarMensaje(agentId, "Prefiero trabajar con Python 3.12 en producción.");
console.log(`Agente: ${r1.slice(0, 150)}\n`);

// Turno 2: el agente usa la memoria para responder
const r2 = await enviarMensaje(agentId, "¿Qué lenguaje debería usar para el nuevo microservicio?");
console.log(`Agente: ${r2.slice(0, 150)}\n`);

// Ver qué tiene en core_memory tras los dos turnos
const memoria = await verMemoriaCore(agentId);
console.log("--- core memory ---");
console.log(`human:   ${memoria.human.slice(0, 200)}`);
console.log(`persona: ${memoria.persona.slice(0, 100)}`);

// Limpiar: borrar el agente de demo
await eliminarAgente(agentId);
console.log("\nAgente eliminado.");
