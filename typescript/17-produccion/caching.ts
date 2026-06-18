// Tres capas de caching: prompt caching (Anthropic), response caching, embedding caching

// Cómo ejecutar: make ts SCRIPT=typescript/17-produccion/caching.ts

import Anthropic from "@anthropic-ai/sdk";
import crypto from "crypto";

const cliente = new Anthropic();

const GUIA_ESTILO = "Regla 1: usa nombres descriptivos.\nRegla 2: máximo 80 chars por línea.\n".repeat(50);

// ─── Capa 1: Prompt caching ───────────────────────────────────────────────────
const SYSTEM_CON_CACHE: Anthropic.TextBlockParam[] = [
  {
    type: "text",
    text: "Eres un revisor de código experto.\n\n" + GUIA_ESTILO,
    // @ts-expect-error cache_control is valid in the API but not yet in all SDK type defs
    cache_control: { type: "ephemeral" },
  },
];

async function revisarConPromptCache(codigo: string): Promise<Anthropic.Message> {
  const respuesta = await cliente.messages.create({
    model: process.env["MODEL"] ?? "claude-sonnet-4-6",
    max_tokens: 512,
    system: SYSTEM_CON_CACHE as Anthropic.TextBlockParam[],
    messages: [{ role: "user", content: `Revisa este código:\n${codigo}` }],
  });
  const uso = respuesta.usage as Anthropic.Usage & {
    cache_creation_input_tokens?: number;
    cache_read_input_tokens?: number;
  };
  const cacheHit = (uso.cache_read_input_tokens ?? 0) > 0;
  console.log(
    `[cache_prompt] hit=${cacheHit} | creation=${uso.cache_creation_input_tokens ?? 0} | read=${uso.cache_read_input_tokens ?? 0}`
  );
  return respuesta;
}

// ─── Capa 2: Response caching ─────────────────────────────────────────────────
const responseCache = new Map<string, { value: string; ts: number }>();

function cachearRespuesta(ttlSegundos: number = 300) {
  return function <T extends unknown[]>(
    fn: (...args: T) => Promise<string>
  ): (...args: T) => Promise<string> {
    return async (...args: T): Promise<string> => {
      const clave = crypto
        .createHash("sha256")
        .update(JSON.stringify(args))
        .digest("hex");

      const cached = responseCache.get(clave);
      if (cached && Date.now() / 1000 - cached.ts < ttlSegundos) {
        console.log("[cache_response] hit");
        return cached.value;
      }

      const resultado = await fn(...args);
      responseCache.set(clave, { value: resultado, ts: Date.now() / 1000 });
      console.log("[cache_response] miss — respuesta guardada");
      return resultado;
    };
  };
}

const responderFaq = cachearRespuesta(300)(async (pregunta: string): Promise<string> => {
  const respuesta = await cliente.messages.create({
    model: process.env["MODEL"] ?? "claude-sonnet-4-6",
    max_tokens: 256,
    messages: [{ role: "user", content: pregunta }],
  });
  return (respuesta.content[0] as Anthropic.TextBlock).text;
});

// ─── Capa 3: Semantic caching (por similitud de embedding) ────────────────────
function embeddingStub(texto: string): number[] {
  let h = 0;
  for (let i = 0; i < texto.length; i++) h = (h * 31 + texto.charCodeAt(i)) | 0;
  const val = (Math.abs(h) % 100) / 100.0;
  return Array(10).fill(val);
}

function similitudCoseno(a: number[], b: number[]): number {
  const dot = a.reduce((s, x, i) => s + x * b[i], 0);
  const normA = Math.sqrt(a.reduce((s, x) => s + x * x, 0));
  const normB = Math.sqrt(b.reduce((s, x) => s + x * x, 0));
  if (normA === 0 || normB === 0) return 0;
  return dot / (normA * normB);
}

const semanticCache: Array<{ emb: number[]; query: string; respuesta: string }> = [];
const UMBRAL_SIMILITUD = 0.95;

async function responderSemantico(pregunta: string): Promise<string> {
  const emb = embeddingStub(pregunta);

  for (const entry of semanticCache) {
    const sim = similitudCoseno(emb, entry.emb);
    if (sim >= UMBRAL_SIMILITUD) {
      console.log(`[cache_semantic] hit (similitud=${sim.toFixed(3)}, query original='${entry.query}')`);
      return entry.respuesta;
    }
  }

  const respuesta = await cliente.messages.create({
    model: process.env["MODEL"] ?? "claude-sonnet-4-6",
    max_tokens: 256,
    messages: [{ role: "user", content: pregunta }],
  });
  const texto = (respuesta.content[0] as Anthropic.TextBlock).text;
  semanticCache.push({ emb, query: pregunta, respuesta: texto });
  console.log("[cache_semantic] miss — respuesta guardada");
  return texto;
}

async function main(): Promise<void> {
  console.log("=== Prompt caching ===");
  const codigo = "def f(x):\n    return x*2";
  await revisarConPromptCache(codigo);
  await revisarConPromptCache(codigo);

  console.log("\n=== Response caching ===");
  await responderFaq("¿Cuál es la política de devoluciones?");
  await responderFaq("¿Cuál es la política de devoluciones?");

  console.log("\n=== Semantic caching ===");
  await responderSemantico("¿Qué hace filter_context?");
  await responderSemantico("¿Qué hace filter_context?");
}

main().catch(console.error);
