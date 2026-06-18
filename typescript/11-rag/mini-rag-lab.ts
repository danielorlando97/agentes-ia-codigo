// Cómo ejecutar: make ts SCRIPT=typescript/11-rag/mini-rag-lab.ts
/**
 * Mini-proyecto: El RAG lab (TypeScript).
 *
 * Pipeline RAG con TF-IDF / BM25 / reranking simulado — sin API key.
 *
 * Uso:
 *   npx ts-node mini-rag-lab.ts
 *   npx ts-node mini-rag-lab.ts --tecnica bm25
 *   npx ts-node mini-rag-lab.ts --tecnica todos --query "¿qué es un agente?"
 *   npx ts-node mini-rag-lab.ts --top-k 5
 */

const CORPUS_EJEMPLO = `
Un agente de IA es un sistema que percibe su entorno mediante herramientas y actúa para alcanzar objetivos. A diferencia de un chatbot simple, un agente puede planificar, ejecutar acciones y adaptarse a resultados inesperados.

Los agentes modernos se construyen sobre modelos de lenguaje (LLM). El LLM actúa como motor de razonamiento: decide qué hacer a continuación basándose en el contexto acumulado y las herramientas disponibles.

Las herramientas son funciones que el agente puede invocar: buscar en internet, leer archivos, ejecutar código, consultar bases de datos. Cada herramienta tiene un nombre, una descripción y un esquema de parámetros en JSON.

El loop ReAct (Reason-Act-Observe) es el patrón más común: el modelo razona sobre la situación, decide una acción, ejecuta la herramienta, observa el resultado, y repite hasta completar la tarea.

La memoria de un agente tiene varias capas: la ventana de contexto inmediata (short-term), el historial de la sesión (episódica), y una base de conocimiento recuperable (semántica). Cada capa tiene características distintas de capacidad, latencia y costo.

RAG (Retrieval-Augmented Generation) combina recuperación de documentos relevantes con generación del modelo. En lugar de depender solo del conocimiento interno del LLM, RAG busca fragmentos pertinentes en un corpus y los incluye en el contexto.

El chunking divide el corpus en fragmentos manejables antes de indexarlos. La estrategia de chunking afecta la calidad del retrieval: chunks muy pequeños pierden contexto; chunks muy grandes diluyen la señal de relevancia.

TF-IDF (Term Frequency - Inverse Document Frequency) es una técnica clásica de recuperación de información. Pondera los términos según su frecuencia en el documento y su rareza en el corpus. Los términos frecuentes en pocos documentos tienen peso alto.

BM25 es una mejora sobre TF-IDF que normaliza por longitud de documento y aplica saturación a la frecuencia de término. En benchmarks estándar supera a TF-IDF vanilla en 5-15% de precisión para queries cortas.

El reranking es un segundo paso de ordenación sobre los candidatos del primer retrieval. Un reranker evalúa la relevancia entre query y fragmento juntos, en lugar de comparar embeddings por separado.

Los embeddings vectoriales representan texto como vectores de alta dimensión (768-3072 dims típicamente). La similitud coseno entre dos vectores mide qué tan semánticamente cercanos son sus textos originales.

Los índices vectoriales (FAISS, Chroma, Pinecone) permiten búsqueda de k-vecinos más cercanos en millones de vectores en milisegundos. FAISS usa HNSW o IVF para búsqueda aproximada con alta recall.

El naive RAG falla en cuatro escenarios: queries ambiguas que necesitan reformulación, fragmentos sin contexto suficiente, ranking por similitud diferente a ranking por utilidad, y alucinación a pesar del contexto recuperado.

Graph RAG construye un grafo de entidades y relaciones sobre el corpus. Permite responder preguntas de síntesis global. El costo de indexado puede superar los 100 dólares para corpus grandes.

El agente puede usar retrieval como herramienta en lugar de como pipeline fijo. Esto le permite decidir si buscar, qué buscar, y refinar la query. El costo en tokens es 3-4 veces mayor que el pipeline fijo.
`.trim();

type Chunk = { id: number; texto: string; tokens: number };
type ChunkScore = Chunk & { score: number };
type Indice = { corpusTokens: string[][]; idf: Map<string, number>; avgLen: number };

// ── chunking ──────────────────────────────────────────────────────────────────

function chunkingParrafos(texto: string): Chunk[] {
  return texto
    .split(/\n{2,}/)
    .map((p) => p.trim())
    .filter((p) => p.length > 0)
    .map((p, i) => ({ id: i, texto: p, tokens: Math.floor(p.length / 4) }));
}

function tokenizarTexto(texto: string): string[] {
  return texto
    .toLowerCase()
    .replace(/[^a-záéíóúüñ\s]/g, " ")
    .split(/\s+/)
    .filter((t) => t.length > 2);
}

// ── TF-IDF ────────────────────────────────────────────────────────────────────

function calcularTF(tokens: string[]): Map<string, number> {
  const conteo = new Map<string, number>();
  for (const t of tokens) conteo.set(t, (conteo.get(t) ?? 0) + 1);
  const total = tokens.length;
  const tf = new Map<string, number>();
  for (const [t, c] of conteo) tf.set(t, c / total);
  return tf;
}

function calcularIDF(corpusTokens: string[][]): Map<string, number> {
  const n = corpusTokens.length;
  const df = new Map<string, number>();
  for (const tokens of corpusTokens) {
    for (const t of new Set(tokens)) df.set(t, (df.get(t) ?? 0) + 1);
  }
  const idf = new Map<string, number>();
  for (const [t, d] of df) idf.set(t, Math.log((n + 1) / (d + 1)) + 1);
  return idf;
}

function tfidfScore(queryTokens: string[], chunkTokens: string[], idf: Map<string, number>): number {
  const tf = calcularTF(chunkTokens);
  let score = 0;
  for (const qt of queryTokens) score += (tf.get(qt) ?? 0) * (idf.get(qt) ?? 0);
  return score;
}

// ── BM25 ──────────────────────────────────────────────────────────────────────

function bm25Score(
  queryTokens: string[],
  chunkTokens: string[],
  idf: Map<string, number>,
  avgLen: number,
  k1 = 1.5,
  b = 0.75,
): number {
  const conteo = new Map<string, number>();
  for (const t of chunkTokens) conteo.set(t, (conteo.get(t) ?? 0) + 1);
  const dl = chunkTokens.length;
  let score = 0;
  for (const qt of queryTokens) {
    const tf = conteo.get(qt) ?? 0;
    const idfVal = idf.get(qt) ?? 0;
    const num = tf * (k1 + 1);
    const den = tf + k1 * (1 - b + b * dl / avgLen);
    score += idfVal * (den > 0 ? num / den : 0);
  }
  return score;
}

// ── reranker simulado ─────────────────────────────────────────────────────────

function rerankScore(queryTokens: string[], chunkTexto: string): number {
  const chunkTokens = tokenizarTexto(chunkTexto);
  const chunkSet = new Set(chunkTokens);
  const cobertura = queryTokens.filter((qt) => chunkSet.has(qt)).length / Math.max(queryTokens.length, 1);
  const densidad = queryTokens.reduce((acc, qt) => acc + chunkTokens.filter((t) => t === qt).length, 0) / Math.max(chunkTokens.length, 1);
  const n = chunkTokens.length;
  let longScore: number;
  if (n < 10) longScore = 0.3;
  else if (n < 50) longScore = 0.7;
  else if (n <= 200) longScore = 1.0;
  else longScore = Math.max(0.5, 1.0 - (n - 200) / 400);
  return 0.5 * cobertura + 0.3 * densidad + 0.2 * longScore;
}

// ── pipeline ──────────────────────────────────────────────────────────────────

function construirIndice(chunks: Chunk[]): Indice {
  const corpusTokens = chunks.map((c) => tokenizarTexto(c.texto));
  const idf = calcularIDF(corpusTokens);
  const avgLen = corpusTokens.reduce((acc, t) => acc + t.length, 0) / Math.max(corpusTokens.length, 1);
  return { corpusTokens, idf, avgLen };
}

function recuperar(query: string, chunks: Chunk[], indice: Indice, tecnica: string, topK: number): ChunkScore[] {
  const queryTokens = tokenizarTexto(query);
  const { idf, avgLen, corpusTokens } = indice;

  const scores: [number, number][] = chunks.map((chunk, i) => {
    const ct = corpusTokens[i];
    let score: number;
    if (tecnica === "tfidf") score = tfidfScore(queryTokens, ct, idf);
    else if (tecnica === "bm25" || tecnica === "rerank") score = bm25Score(queryTokens, ct, idf, avgLen);
    else score = 0;
    return [score, i];
  });

  scores.sort((a, b) => b[0] - a[0]);

  if (tecnica === "rerank") {
    const candidatosIdx = scores.slice(0, topK * 3).map(([, i]) => i);
    const rerankScores: [number, number][] = candidatosIdx.map((idx) => [
      rerankScore(queryTokens, chunks[idx].texto),
      idx,
    ]);
    rerankScores.sort((a, b) => b[0] - a[0]);
    return rerankScores.slice(0, topK).map(([s, idx]) => ({ ...chunks[idx], score: s }));
  }

  return scores.slice(0, topK).map(([s, idx]) => ({ ...chunks[idx], score: s }));
}

// ── presentación ──────────────────────────────────────────────────────────────

function truncar(texto: string, n: number): string {
  return texto.length > n ? texto.slice(0, n - 1) + "…" : texto;
}

function imprimirResultados(query: string, tecnica: string, fragmentos: ChunkScore[]): void {
  console.log(`\n  Técnica: ${tecnica.toUpperCase()}   |   query: "${query}"`);
  console.log(`  ${"-".repeat(56)}`);
  for (let i = 0; i < fragmentos.length; i++) {
    const f = fragmentos[i];
    console.log(`  [${i + 1}] score=${f.score.toFixed(4)}  tokens=${f.tokens}`);
    console.log(`      ${truncar(f.texto, 70)}`);
  }
  console.log();
}

function compararTecnicas(query: string, chunks: Chunk[], indice: Indice, topK: number): void {
  console.log(`\n${"=".repeat(64)}`);
  console.log(`  RAG LAB — Comparativa de técnicas de retrieval`);
  console.log(`  Query: "${query}"`);
  console.log(`  Corpus: ${chunks.length} fragmentos  |  top-k: ${topK}`);
  console.log("=".repeat(64));

  const tecnicas = ["tfidf", "bm25", "rerank"];
  const todosResultados = new Map<string, ChunkScore[]>();
  for (const t of tecnicas) {
    const res = recuperar(query, chunks, indice, t, topK);
    todosResultados.set(t, res);
    imprimirResultados(query, t, res);
  }

  console.log(`  ${"─".repeat(56)}`);
  console.log(`  Acuerdo entre técnicas (fragmentos recuperados por múltiples)`);
  console.log(`  ${"─".repeat(56)}`);
  const conteo = new Map<number, number>();
  for (const res of todosResultados.values()) {
    for (const f of res) conteo.set(f.id, (conteo.get(f.id) ?? 0) + 1);
  }
  const acuerdos = [...conteo.entries()].filter(([, c]) => c > 1).sort((a, b) => b[1] - a[1]);
  if (acuerdos.length === 0) {
    console.log("  (No hay acuerdo entre técnicas para esta query)");
  } else {
    for (const [fid, count] of acuerdos) {
      console.log(`  [${fid}] ×${count} técnicas: ${truncar(chunks[fid].texto, 55)}`);
    }
  }
}

function modoUnico(query: string, chunks: Chunk[], indice: Indice, tecnica: string, topK: number): void {
  const fragmentos = recuperar(query, chunks, indice, tecnica, topK);
  console.log(`\n${"=".repeat(64)}`);
  console.log(`  RAG LAB — ${tecnica.toUpperCase()}`);
  console.log(`  Query: "${query}"`);
  console.log(`  Corpus: ${chunks.length} fragmentos  |  top-k: ${topK}`);
  console.log("=".repeat(64));
  imprimirResultados(query, tecnica, fragmentos);
  const nTok = fragmentos.slice(0, 3).reduce((acc, f) => acc + f.tokens, 0);
  console.log(`  Respuesta: [Simulada — contexto de ${fragmentos.length} fragmentos, ~${nTok} tokens]`);
}

// ── main ──────────────────────────────────────────────────────────────────────

function main(): void {
  const args = process.argv.slice(2);
  const getArg = (flag: string, def: string) => {
    const i = args.indexOf(flag);
    return i >= 0 && args[i + 1] ? args[i + 1] : def;
  };

  const tecnica = getArg("--tecnica", "todos");
  const query = getArg("--query", "¿qué es un agente de IA?");
  const topK = parseInt(getArg("--top-k", "3"));

  const texto = CORPUS_EJEMPLO;
  console.log("[Usando corpus de ejemplo sobre agentes IA]\n");

  const chunks = chunkingParrafos(texto);
  const indice = construirIndice(chunks);
  const totalTokens = chunks.reduce((acc, c) => acc + c.tokens, 0);

  console.log(`[Corpus: ${chunks.length} fragmentos, ${totalTokens} tokens totales]`);
  console.log(`[Vocabulario: ${indice.idf.size} términos únicos]`);

  if (tecnica === "todos") {
    compararTecnicas(query, chunks, indice, topK);
  } else {
    modoUnico(query, chunks, indice, tecnica, topK);
  }
}

main();
