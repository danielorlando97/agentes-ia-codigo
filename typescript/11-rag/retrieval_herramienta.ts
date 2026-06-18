// Retrieval como herramienta — el LLM decide cuándo y qué buscar con tool_use.
//
// En lugar de recuperar siempre antes de generar (RAG ingenuo), aquí el LLM
// recibe buscar_documentos como herramienta y decide si llamarla, cuántas veces,
// y con qué query. El agente itera hasta que produce texto final (end_turn)
// o alcanza el límite de seguridad de 5 iteraciones.
//
// TF-IDF cosine idéntico a rag_ingenuo — sin dependencias externas salvo
// @anthropic-ai/sdk.

// Cómo ejecutar: make ts SCRIPT=typescript/11-rag/retrieval_herramienta.ts


import Anthropic from "@anthropic-ai/sdk";

const MODEL = process.env["MODEL"] ?? "claude-haiku-4-5-20251001";
const MAX_ITER = 5;

const CORPUS: string[] = [
  "Los modelos de lenguaje transformers usan mecanismo de atención para procesar texto.",
  "El contexto de un LLM es la ventana de tokens que puede procesar en una sola inferencia.",
  "RAG combina recuperación de documentos con generación del LLM para reducir alucinaciones.",
  "El chunking divide documentos largos en fragmentos manejables para el vector store.",
  "La similitud coseno mide el ángulo entre dos vectores en el espacio de embeddings.",
  "Los embeddings mapean texto a vectores numéricos en un espacio semántico continuo.",
  "El reranking reordena los candidatos recuperados usando un modelo más preciso.",
  "BM25 es una función de recuperación basada en TF-IDF mejorada para búsqueda exacta.",
  "RAG-Anything extiende RAG a corpus multimodal con tablas, imágenes y ecuaciones.",
  "LightRAG construye un grafo de conocimiento con retrieval dual-level para multi-hop.",
];

// ── TF-IDF cosine ─────────────────────────────────────────────────────────────

function tokenizar(texto: string): string[] {
  return texto.toLowerCase().split(/\s+/);
}

function tfidfVector(
  tokens: string[],
  df: Map<string, number>,
  nDocs: number
): Map<string, number> {
  const tf = new Map<string, number>();
  for (const t of tokens) tf.set(t, (tf.get(t) ?? 0) + 1);

  const total = tokens.length || 1;
  const vector = new Map<string, number>();
  for (const [term, count] of tf) {
    const tfScore = count / total;
    const idfScore = Math.log((nDocs + 1) / ((df.get(term) ?? 0) + 1));
    vector.set(term, tfScore * idfScore);
  }
  return vector;
}

function cosineSim(v1: Map<string, number>, v2: Map<string, number>): number {
  let dot = 0;
  for (const [t, w] of v2) dot += (v1.get(t) ?? 0) * w;
  const norm1 = Math.sqrt([...v1.values()].reduce((s, x) => s + x * x, 0));
  const norm2 = Math.sqrt([...v2.values()].reduce((s, x) => s + x * x, 0));
  if (norm1 === 0 || norm2 === 0) return 0;
  return dot / (norm1 * norm2);
}

type IndexEntry = { chunk: string; vec: Map<string, number> };

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

function buscarDocumentos(
  query: string,
  k: number,
  index: IndexEntry[],
  df: Map<string, number>
): string {
  const nDocs = index.length;
  const qTokens = tokenizar(query);
  const qVec = tfidfVector(qTokens, df, nDocs);

  const scores = index.map(({ chunk, vec }) => ({
    chunk,
    score: cosineSim(qVec, vec),
  }));
  scores.sort((a, b) => b.score - a.score);

  return scores
    .slice(0, k)
    .map((s, i) => `${i + 1}. ${s.chunk} (score=${s.score.toFixed(4)})`)
    .join("\n");
}

// ── Definición de la herramienta para la API de Anthropic ─────────────────────

const TOOLS: Anthropic.Tool[] = [
  {
    name: "buscar_documentos",
    description:
      "Busca en la base de conocimiento interna y devuelve los fragmentos más relevantes.",
    input_schema: {
      type: "object",
      properties: {
        query: {
          type: "string",
          description: "Texto a buscar en la base de conocimiento.",
        },
        k: {
          type: "integer",
          description: "Número de fragmentos a recuperar (por defecto 3).",
        },
      },
      required: ["query"],
    },
  },
];

const SYSTEM =
  "Eres un asistente con acceso a una base de conocimiento. " +
  "Usa buscar_documentos cuando necesites información específica. " +
  "Responde directamente si ya tienes suficiente información.";

// ── Agent loop ─────────────────────────────────────────────────────────────────

async function agenteRag(
  pregunta: string,
  index: IndexEntry[],
  df: Map<string, number>
): Promise<string> {
  const client = new Anthropic();
  const messages: Anthropic.MessageParam[] = [{ role: "user", content: pregunta }];

  for (let iteracion = 0; iteracion < MAX_ITER; iteracion++) {
    const response = await client.messages.create({
      model: MODEL,
      max_tokens: 1024,
      system: SYSTEM,
      tools: TOOLS,
      messages,
    });

    console.log(`\n[iter=${iteracion + 1}] stop_reason=${response.stop_reason}`);

    if (response.stop_reason === "end_turn") {
      const textBlock = response.content.find(
        (b): b is Anthropic.TextBlock => b.type === "text"
      );
      return textBlock?.text ?? "[sin texto en la respuesta]";
    }

    if (response.stop_reason === "tool_use") {
      // Añadir la respuesta del asistente (con los tool_use blocks) al historial
      messages.push({ role: "assistant", content: response.content });

      // Ejecutar todas las tool calls y acumular resultados
      const toolResults: Anthropic.ToolResultBlockParam[] = [];

      for (const block of response.content) {
        if (block.type !== "tool_use") continue;

        const input = block.input as { query: string; k?: number };
        const query = input.query;
        const k = input.k ?? 3;

        console.log(`  → buscar_documentos(query=${JSON.stringify(query)}, k=${k})`);
        const resultado = buscarDocumentos(query, k, index, df);
        console.log(`  ← ${resultado.split("\n")[0]}...`);

        toolResults.push({
          type: "tool_result",
          tool_use_id: block.id,
          content: resultado,
        });
      }

      // CRÍTICO: todos los tool_results en un único mensaje user
      messages.push({ role: "user", content: toolResults });
      continue;
    }

    console.log(`  [warn] stop_reason inesperado: ${response.stop_reason}`);
    break;
  }

  return "[límite de iteraciones alcanzado]";
}

// ── Demo ───────────────────────────────────────────────────────────────────────

async function main(): Promise<void> {
  const { index, df } = indexar(CORPUS);

  const pregunta = "¿Qué diferencia hay entre RAG-Anything y LightRAG?";
  console.log(`Pregunta: ${pregunta}`);

  const respuesta = await agenteRag(pregunta, index, df);
  console.log(`\n=== Respuesta final ===\n${respuesta}`);
}

main().catch(console.error);
