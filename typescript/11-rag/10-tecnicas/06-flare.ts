// FLARE: Forward-Looking Active Retrieval Augmented Generation (Jiang et al., 2023).
//
// FLARE genera texto en segmentos y, cuando la probabilidad de un token cae bajo
// un umbral, usa el texto tentativo como query de búsqueda y regenera el segmento
// con el contexto recuperado. Sin fine-tuning — solo logprobs nativos de la API.
//
// IMPORTANTE: Claude y Gemini no exponen logprobs — este archivo usa OpenAI.
// Compatible con: OpenAI API, modelos locales vía Ollama, HuggingFace.
//
// Cómo ejecutar:
//   export OPENAI_API_KEY=sk-...
//   make ts SCRIPT=typescript/11-rag/10-tecnicas/06-flare.ts

const MODEL      = process.env.MODEL           ?? "gpt-4o-mini";
const UMBRAL     = parseFloat(process.env.FLARE_UMBRAL  ?? "0.2");
const MAX_ITER   = parseInt(process.env.FLARE_MAX_ITER ?? "6", 10);
const OPENAI_KEY = process.env.OPENAI_API_KEY ?? "";
const OPENAI_BASE = (process.env.OPENAI_BASE_URL ?? "https://api.openai.com").replace(/\/$/, "");

if (!OPENAI_KEY) {
  console.error("OPENAI_API_KEY no está definido en el entorno.");
  process.exit(1);
}

// ── Corpus y retriever mock ────────────────────────────────────────────────

const CORPUS = [
  "RAG combina recuperación de documentos con generación del LLM para reducir alucinaciones.",
  "Self-RAG fine-tunea el modelo para emitir tokens especiales que controlan el retrieval.",
  "FLARE activa el retrieval cuando la probabilidad de un token cae bajo un umbral configurable.",
  "BM25 es una función de recuperación léxica basada en frecuencia de término e IDF.",
  "Advanced RAG usa BM25 + búsqueda semántica + RRF para mejorar el recall.",
  "La ventana de contexto de Claude 3 llega a 200 000 tokens.",
  "Los modelos de lenguaje tienden a alucinar hechos fuera de su distribución de entrenamiento.",
  "GraphRAG construye un grafo de entidades sobre el corpus antes de recuperar.",
  "Los logprobs permiten medir la incertidumbre del modelo token a token.",
  "FLARE-direct usa la tentativa de generación como query sin reformulación adicional.",
];

function tokenizar(texto: string): string[] {
  return texto.toLowerCase().split(/\s+/);
}

function bm25TopK(query: string, k = 2): string[] {
  const tokenized = CORPUS.map(tokenizar);
  const n = CORPUS.length;
  const df: Record<string, number> = {};
  for (const tokens of tokenized)
    for (const t of new Set(tokens))
      df[t] = (df[t] ?? 0) + 1;
  const avgdl = tokenized.reduce((s, t) => s + t.length, 0) / n;
  const k1 = 1.5, b = 0.75;
  const qTokens = tokenizar(query);
  const scores = CORPUS.map((doc, i) => {
    const tokens = tokenized[i];
    const tf: Record<string, number> = {};
    for (const t of tokens) tf[t] = (tf[t] ?? 0) + 1;
    const dl = tokens.length;
    let total = 0;
    for (const term of qTokens) {
      if (!(term in df)) continue;
      const idf = Math.log((n - df[term] + 0.5) / (df[term] + 0.5) + 1);
      const freq = tf[term] ?? 0;
      total += idf * (freq * (k1 + 1)) / (freq + k1 * (1 - b + b * dl / avgdl));
    }
    return { doc, score: total };
  });
  return scores
    .filter(x => x.score > 0)
    .sort((a, b) => b.score - a.score)
    .slice(0, k)
    .map(x => x.doc);
}

// ── Generación con logprobs ────────────────────────────────────────────────

interface TokenLogprob {
  token: string;
  logprob: number;
}

interface GenResult {
  texto: string;
  minLogprob: number;
}

async function generarConLogprobs(prompt: string, maxTokens = 80): Promise<GenResult> {
  const resp = await fetch(`${OPENAI_BASE}/v1/chat/completions`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      "Authorization": `Bearer ${OPENAI_KEY}`,
    },
    body: JSON.stringify({
      model: MODEL,
      messages: [{ role: "user", content: prompt }],
      max_tokens: maxTokens,
      logprobs: true,
      temperature: 0,
    }),
  });

  if (!resp.ok) {
    const err = await resp.text();
    throw new Error(`OpenAI API error: ${resp.status} ${err}`);
  }

  const data = await resp.json() as {
    choices: Array<{
      message: { content: string };
      logprobs?: { content?: TokenLogprob[] };
    }>;
  };

  const texto = data.choices[0].message.content ?? "";
  const tokens = data.choices[0].logprobs?.content ?? [];
  const minLogprob = tokens.length > 0
    ? Math.min(...tokens.map(t => t.logprob))
    : 0;

  return { texto, minLogprob };
}

// ── FLARE-direct ──────────────────────────────────────────────────────────

async function flare(query: string): Promise<string> {
  const segmentosAceptados: string[] = [];
  let contextoActual: string[] = [];

  for (let i = 1; i <= MAX_ITER; i++) {
    const contextoStr = contextoActual.join("\n");
    const previoStr   = segmentosAceptados.join(" ");

    const prompt =
      `Responde en español de forma factual y concisa. ` +
      (contextoStr ? `Contexto recuperado: ${contextoStr}\n` : "") +
      (previoStr   ? `Respuesta parcial hasta ahora: ${previoStr}\n` : "") +
      `Pregunta: ${query}\n` +
      `Continúa la respuesta (máximo 2 oraciones):`;

    const { texto: tentativa, minLogprob } = await generarConLogprobs(prompt);
    const preview = tentativa.slice(0, 60).replace(/\n/g, " ");
    console.log(`  [${i}] confianza mín: ${minLogprob.toFixed(3)}  |  tentativa: ${JSON.stringify(preview)}`);

    let segmento = tentativa;

    if (minLogprob < UMBRAL) {
      const chunks = bm25TopK(tentativa);
      if (chunks.length > 0) {
        contextoActual = chunks;
        console.log(`       → retrieval activado (${chunks.length} chunks). Regenerando...`);
        const promptRegen =
          `Responde en español de forma factual y concisa. ` +
          `Contexto: ${chunks.join("\n")}\n` +
          (previoStr ? `Respuesta parcial: ${previoStr}\n` : "") +
          `Pregunta: ${query}\n` +
          `Continúa la respuesta (máximo 2 oraciones):`;
        const { texto } = await generarConLogprobs(promptRegen);
        segmento = texto;
      }
    }

    segmentosAceptados.push(segmento.trim());

    const termina = /[.!?]/.test(segmento);
    if (termina && segmentosAceptados.join(" ").split(/\s+/).length > 20) break;
  }

  return segmentosAceptados.join(" ");
}

// ── Demo ──────────────────────────────────────────────────────────────────

async function main(): Promise<void> {
  const preguntas = [
    "¿Qué es FLARE y cómo se diferencia de Self-RAG?",
    "¿Cuándo se activa el retrieval en FLARE y qué hace el modelo con los chunks?",
  ];

  for (const pregunta of preguntas) {
    console.log(`\nPregunta: ${pregunta}`);
    console.log(`Umbral logprob: ${UMBRAL} | Modelo: ${MODEL}\n`);
    const respuesta = await flare(pregunta);
    console.log(`\nRespuesta final: ${respuesta}\n`);
    console.log("-".repeat(70));
  }
}

main().catch(console.error);
