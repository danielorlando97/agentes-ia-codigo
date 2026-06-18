// Construir un system prompt modular y medir el efecto del prompt caching.
//
// Demuestra:
// - System prompt con 5 bloques: identidad, instrucciones, herramientas, restricciones, ejemplos
// - Bloque estático con cache_control para los bloques que no cambian entre requests
// - Bloque dinámico sin cache para fecha y estado de sesión
// - Medir tokens cacheados vs no cacheados
// - Calcular ahorro de tokens en un batch de 10 requests con el mismo system prompt

// Cómo ejecutar: make ts SCRIPT=typescript/04-prompts/anatomia-system-prompt.ts

import Anthropic from "@anthropic-ai/sdk";

const MODEL = process.env["MODEL"] ?? "claude-sonnet-4-6";

// ─── 1. Bloques estáticos del system prompt ───────────────────────────────────

const BLOQUE_IDENTIDAD = `Eres TechBot, el asistente de soporte técnico de TechStore.
Tu única función es resolver dudas sobre los productos y servicios de TechStore.
Eres directo, preciso y siempre confirmas si has entendido bien la pregunta antes de responder.`;

const BLOQUE_INSTRUCCIONES = `<instrucciones>
  Antes de responder, verifica que la pregunta está dentro de tu dominio (productos TechStore).
  Si la pregunta es sobre facturación, deriva al equipo de ventas sin dar precios.
  Si la pregunta es técnica, intenta resolverla en máximo 3 pasos.
  Termina siempre con: ¿Te ha sido útil esta respuesta?
</instrucciones>`;

const BLOQUE_HERRAMIENTAS = `<herramientas_disponibles>
  - buscar_producto(nombre): busca información de un producto en el catálogo
  - consultar_estado_pedido(id_pedido): devuelve el estado de un pedido
  - crear_ticket_soporte(descripcion, prioridad): abre un ticket en el sistema
  Nota: no tienes acceso a información de precios ni a cuentas de usuario.
</herramientas_disponibles>`;

const BLOQUE_RESTRICCIONES = `<restricciones>
  NUNCA inventes precios. Si se pregunta por un precio, di: "No tengo ese dato. Contacta con ventas."
  NUNCA compartas información personal de otros clientes.
  NUNCA ejecutes acciones destructivas (cancelar pedidos, eliminar datos).
  Solo responde preguntas sobre TechStore. Fuera de dominio: redirige amablemente.
</restricciones>`;

const BLOQUE_EJEMPLOS = `<ejemplos>
  <ejemplo>
    <usuario>Mi pedido #12345 no ha llegado</usuario>
    <asistente>Entendido. Consultaré el estado de tu pedido. ¿Tienes el número de seguimiento del transportista? ¿Te ha sido útil esta respuesta?</asistente>
  </ejemplo>
  <ejemplo>
    <usuario>¿Cuánto cuesta el Laptop ProX?</usuario>
    <asistente>No tengo acceso a información de precios actualizada. Por favor contacta con nuestro equipo de ventas en ventas@techstore.com. ¿Te ha sido útil esta respuesta?</asistente>
  </ejemplo>
</ejemplos>`;

// ─── 2. Construcción del system prompt modular ───────────────────────────────

type SystemBlock = {
  type: "text";
  text: string;
  cache_control?: { type: "ephemeral" };
};

function buildSystemPromptCached(dynamicInfo: string): SystemBlock[] {
  const staticContent = [
    BLOQUE_IDENTIDAD,
    BLOQUE_INSTRUCCIONES,
    BLOQUE_HERRAMIENTAS,
    BLOQUE_RESTRICCIONES,
    BLOQUE_EJEMPLOS,
  ].join("\n\n");

  return [
    {
      type: "text",
      text: staticContent,
      cache_control: { type: "ephemeral" },
    },
    {
      type: "text",
      text: dynamicInfo,
      // Sin cache_control: siempre paga costo completo
    },
  ];
}

function buildSystemPromptNoCache(dynamicInfo: string): string {
  return [
    BLOQUE_IDENTIDAD,
    BLOQUE_INSTRUCCIONES,
    BLOQUE_HERRAMIENTAS,
    BLOQUE_RESTRICCIONES,
    BLOQUE_EJEMPLOS,
    dynamicInfo,
  ].join("\n\n");
}

// ─── 3. Batch de requests ─────────────────────────────────────────────────────

const QUESTIONS = [
  "¿Dónde puedo ver el estado de mi pedido #45678?",
  "El adaptador HDMI que compré no funciona con mi televisor Samsung.",
  "¿Tienen garantía extendida para laptops?",
  "Necesito abrir un ticket porque recibí el producto equivocado.",
  "¿Cómo puedo devolver un artículo defectuoso?",
  "La aplicación de TechStore no me deja iniciar sesión.",
  "¿Tienen repuestos para el teclado MechType K85?",
  "Mi factura del mes pasado tiene un error de importe.",
  "¿Cuánto tiempo tarda el envío estándar?",
  "El mouse inalámbrico pierde conexión constantemente.",
];

interface RequestResult {
  questionIdx: number;
  question: string;
  inputTokens: number;
  outputTokens: number;
  cacheCreationTokens: number;
  cacheReadTokens: number;
}

async function runBatchCached(client: Anthropic, questions: string[]): Promise<RequestResult[]> {
  const results: RequestResult[] = [];
  for (let i = 0; i < questions.length; i++) {
    const question = questions[i];
    const now = new Date().toISOString().replace("T", " ").slice(0, 19);
    const dynamicInfo = `Fecha y hora: ${now}\nID de sesión: session-demo-${String(i + 1).padStart(4, "0")}`;

    const system = buildSystemPromptCached(dynamicInfo);
    const response = await client.messages.create({
      model: MODEL,
      max_tokens: 200,
      system: system as Anthropic.MessageParam["content"],
      messages: [{ role: "user", content: question }],
    });

    const usage = response.usage;
    const cacheCreate = (usage as Record<string, number>).cache_creation_input_tokens ?? 0;
    const cacheRead = (usage as Record<string, number>).cache_read_input_tokens ?? 0;

    results.push({
      questionIdx: i + 1,
      question: question.slice(0, 50),
      inputTokens: usage.input_tokens,
      outputTokens: usage.output_tokens,
      cacheCreationTokens: cacheCreate,
      cacheReadTokens: cacheRead,
    });

    console.log(
      `  Request ${String(i + 1).padStart(2)}/${questions.length}: ` +
      `input=${String(usage.input_tokens).padStart(5)}, ` +
      `cache_write=${String(cacheCreate).padStart(5)}, ` +
      `cache_read=${String(cacheRead).padStart(5)}`
    );
  }
  return results;
}

async function runBatchNoCache(client: Anthropic, questions: string[]): Promise<RequestResult[]> {
  const results: RequestResult[] = [];
  for (let i = 0; i < questions.length; i++) {
    const question = questions[i];
    const now = new Date().toISOString().replace("T", " ").slice(0, 19);
    const dynamicInfo = `Fecha y hora: ${now}\nID de sesión: session-demo-${String(i + 1).padStart(4, "0")}`;

    const system = buildSystemPromptNoCache(dynamicInfo);
    const response = await client.messages.create({
      model: MODEL,
      max_tokens: 200,
      system,
      messages: [{ role: "user", content: question }],
    });

    results.push({
      questionIdx: i + 1,
      question: question.slice(0, 50),
      inputTokens: response.usage.input_tokens,
      outputTokens: response.usage.output_tokens,
      cacheCreationTokens: 0,
      cacheReadTokens: 0,
    });
  }
  return results;
}

// ─── 4. Análisis de resultados ────────────────────────────────────────────────

function analyzeSavings(cachedResults: RequestResult[], noCacheResults: RequestResult[]): void {
  const totalInputCached = cachedResults.reduce((s, r) => s + r.inputTokens, 0);
  const totalInputNoCache = noCacheResults.reduce((s, r) => s + r.inputTokens, 0);
  const totalCacheWrites = cachedResults.reduce((s, r) => s + r.cacheCreationTokens, 0);
  const totalCacheReads = cachedResults.reduce((s, r) => s + r.cacheReadTokens, 0);

  // Precios claude-sonnet-4-6: $3/MTok input, $3.75/MTok cache write, $0.30/MTok cache read
  const costNoCache = (totalInputNoCache / 1_000_000) * 3.0;
  const costCached =
    (totalCacheWrites / 1_000_000) * 3.75 +
    (totalCacheReads / 1_000_000) * 0.30 +
    ((totalInputCached - totalCacheReads) / 1_000_000) * 3.0;

  const n = cachedResults.length;

  console.log(`\n${"═".repeat(68)}`);
  console.log("  ANÁLISIS DE TOKENS Y AHORRO POR CACHING");
  console.log(`${"═".repeat(68)}`);
  console.log(`\n  Batch: ${n} requests con el mismo system prompt estático`);
  console.log(`\n  ${"Métrica".padEnd(45)} ${"Sin cache".padStart(10)} ${"Con cache".padStart(10)}`);
  console.log(`  ${"-".repeat(67)}`);
  console.log(`  ${"Tokens input totales".padEnd(45)} ${totalInputNoCache.toLocaleString().padStart(10)} ${totalInputCached.toLocaleString().padStart(10)}`);
  console.log(`  ${"Tokens cache_creation (escritura)".padEnd(45)} ${"—".padStart(10)} ${totalCacheWrites.toLocaleString().padStart(10)}`);
  console.log(`  ${"Tokens cache_read (lectura)".padEnd(45)} ${"—".padStart(10)} ${totalCacheReads.toLocaleString().padStart(10)}`);
  console.log(`  ${"Tokens input promedio por request".padEnd(45)} ${(totalInputNoCache / n).toFixed(0).padStart(10)} ${(totalInputCached / n).toFixed(0).padStart(10)}`);

  console.log(`\n  ${"Costo estimado del batch (USD)".padEnd(45)} ${"$" + costNoCache.toFixed(4).padStart(9)} ${"$" + costCached.toFixed(4).padStart(9)}`);

  if (costNoCache > 0) {
    const savingsPct = (1 - costCached / costNoCache) * 100;
    const savingsAbs = costNoCache - costCached;
    console.log(`\n  Ahorro: $${savingsAbs.toFixed(4)} USD (${savingsPct.toFixed(1)}%)`);
  }

  console.log(`\n  Desglose por request (con cache):`);
  console.log(`  ${"Req".padStart(4)}  ${"Input".padStart(7)}  ${"Cache write".padStart(12)}  ${"Cache read".padStart(11)}`);
  console.log(`  ${"-".repeat(40)}`);
  for (const r of cachedResults) {
    console.log(
      `  ${String(r.questionIdx).padStart(4)}  ${String(r.inputTokens).padStart(7)}  ` +
      `${String(r.cacheCreationTokens).padStart(12)}  ${String(r.cacheReadTokens).padStart(11)}`
    );
  }

  console.log(`\n  Notas:`);
  console.log(`  - Request 1: paga cache_creation (escribir el cache por primera vez)`);
  console.log(`  - Requests 2+: pagan cache_read (~10% del precio de input estándar)`);
  console.log(`  - TTL del cache: 5 minutos. Se renueva en cada hit.`);
  console.log(`  - Solo el bloque estático se cachea; el bloque dinámico paga costo completo.`);
}

// ─── 5. Main ──────────────────────────────────────────────────────────────────

async function main(): Promise<void> {
  const client = new Anthropic();

  const staticContent = [BLOQUE_IDENTIDAD, BLOQUE_INSTRUCCIONES, BLOQUE_HERRAMIENTAS, BLOQUE_RESTRICCIONES, BLOQUE_EJEMPLOS].join("\n\n");
  console.log(`Bloque estático: ${staticContent.length} chars (~${Math.floor(staticContent.length / 4)} tokens estimados)`);

  console.log(`\n${"═".repeat(68)}`);
  console.log("  BATCH CON CACHING (10 requests)");
  console.log(`${"═".repeat(68)}`);
  const cachedResults = await runBatchCached(client, QUESTIONS);

  console.log(`\n${"═".repeat(68)}`);
  console.log("  BATCH SIN CACHING (10 requests) — para comparación");
  console.log(`${"═".repeat(68)}`);
  const noCacheResults = await runBatchNoCache(client, QUESTIONS);

  analyzeSavings(cachedResults, noCacheResults);
}

main().catch(console.error);
