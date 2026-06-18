// Naive RAG con TF-IDF cosine similarity — sin dependencias externas salvo @anthropic-ai/sdk
// Indexa un corpus hardcodeado, recupera top-3 chunks, genera respuesta con Claude Haiku.

// Cómo ejecutar: make ts SCRIPT=typescript/11-rag/rag_ingenuo.ts


import Anthropic from "@anthropic-ai/sdk";

const CORPUS: string[] = [
  "Los modelos de lenguaje transformers usan mecanismo de atención para procesar texto.",
  "El contexto de un LLM es la ventana de tokens que puede procesar en una sola inferencia.",
  "RAG combina recuperación de documentos con generación del LLM para reducir alucinaciones.",
  "El chunking divide documentos largos en fragmentos manejables para el vector store.",
  "La similitud coseno mide el ángulo entre dos vectores en el espacio de embeddings.",
  "Los embeddings mapean texto a vectores numéricos en un espacio semántico continuo.",
  "El reranking reordena los candidatos recuperados usando un modelo más preciso.",
  "BM25 es una función de recuperación basada en TF-IDF mejorada para búsqueda exacta.",
];

type Vector = Map<string, number>;

interface IndexEntry {
  chunk: string;
  vec: Vector;
}

function tokenizar(texto: string): string[] {
  return texto.toLowerCase().split(/\s+/).filter((t) => t.length > 0);
}

function tfidfVector(
  tokens: string[],
  df: Map<string, number>,
  nDocs: number
): Vector {
  const tf = new Map<string, number>();
  for (const t of tokens) {
    tf.set(t, (tf.get(t) ?? 0) + 1);
  }
  const total = tokens.length || 1;
  const vec: Vector = new Map();
  for (const [term, count] of tf) {
    const tfScore = count / total;
    const idfScore = Math.log((nDocs + 1) / ((df.get(term) ?? 0) + 1));
    vec.set(term, tfScore * idfScore);
  }
  return vec;
}

function cosineSim(v1: Vector, v2: Vector): number {
  let dot = 0;
  for (const [term, score] of v2) {
    dot += (v1.get(term) ?? 0) * score;
  }
  const norm = (v: Vector) =>
    Math.sqrt([...v.values()].reduce((s, x) => s + x * x, 0));
  const n1 = norm(v1);
  const n2 = norm(v2);
  if (n1 === 0 || n2 === 0) return 0;
  return dot / (n1 * n2);
}

function indexar(corpus: string[]): { index: IndexEntry[]; df: Map<string, number> } {
  const tokenized = corpus.map(tokenizar);
  const nDocs = corpus.length;

  const df = new Map<string, number>();
  for (const tokens of tokenized) {
    for (const term of new Set(tokens)) {
      df.set(term, (df.get(term) ?? 0) + 1);
    }
  }

  const index: IndexEntry[] = corpus.map((chunk, i) => ({
    chunk,
    vec: tfidfVector(tokenized[i], df, nDocs),
  }));

  return { index, df };
}

function buscar(
  query: string,
  index: IndexEntry[],
  df: Map<string, number>,
  k = 3
): string[] {
  const nDocs = index.length;
  const qTokens = tokenizar(query);
  const qVec = tfidfVector(qTokens, df, nDocs);

  const scores = index.map(({ chunk, vec }) => ({
    chunk,
    score: cosineSim(qVec, vec),
  }));
  scores.sort((a, b) => b.score - a.score);
  return scores.slice(0, k).map((s) => s.chunk);
}

async function ragIngenuo(
  query: string,
  index: IndexEntry[],
  df: Map<string, number>,
  client: Anthropic
): Promise<string> {
  const topChunks = buscar(query, index, df, 3);
  const contexto = topChunks.map((c) => `- ${c}`).join("\n");

  const message = await client.messages.create({
    model: process.env["SMALL_MODEL"] ?? "claude-haiku-4-5-20251001",
    max_tokens: 300,
    system:
      "Responde usando solo el contexto proporcionado. Si la respuesta no está en el contexto, dilo explícitamente.",
    messages: [
      {
        role: "user",
        content: `Contexto:\n${contexto}\n\nPregunta: ${query}`,
      },
    ],
  });

  const block = message.content[0];
  if (block.type !== "text") throw new Error("Respuesta inesperada del LLM");
  return block.text;
}

async function main(): Promise<void> {
  const client = new Anthropic({ apiKey: process.env.ANTHROPIC_API_KEY });
  const { index, df } = indexar(CORPUS);

  const query = "¿Qué es RAG y para qué sirve?";
  console.log(`Query: ${query}\n`);

  const top = buscar(query, index, df, 3);
  console.log("Chunks recuperados:");
  top.forEach((chunk, i) => console.log(`  ${i + 1}. ${chunk}`));
  console.log();

  const respuesta = await ragIngenuo(query, index, df, client);
  console.log(`Respuesta:\n${respuesta}`);
}

main().catch(console.error);
