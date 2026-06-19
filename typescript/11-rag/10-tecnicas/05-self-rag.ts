// Self-RAG: generación por segmentos con tokens de reflexión.
//
// Self-RAG (Asai et al., 2023) enseña al modelo a evaluar en tiempo de inferencia
// si necesita recuperar y si lo que recuperó es útil — sin pipeline externo.
// Los cuatro tokens de reflexión:
//   Retrieve={yes/no/continue}  — ¿necesito buscar para generar este segmento?
//   ISREL={relevant/irrelevant} — ¿el pasaje recuperado es relevante para la query?
//   ISSUP={fully/partially/no}  — ¿el pasaje apoya la afirmación generada?
//   ISUSE={1..5}                — ¿es útil esta respuesta para el usuario?
//
// Esta implementación simula los tokens de reflexión via prompting con Claude.
// El modelo Self-RAG real es un Llama 7B fine-tuneado (selfrag/selfrag_llama2_7b).
//
// Cómo ejecutar:
//   export ANTHROPIC_API_KEY=sk-ant-...
//   make ts SCRIPT=typescript/11-rag/10-tecnicas/05-self-rag.ts

import Anthropic from "@anthropic-ai/sdk";

const MODEL = process.env.MODEL ?? "claude-haiku-4-5-20251001";
const client = new Anthropic();

// ── Corpus y retriever mock ────────────────────────────────────────────────

const CORPUS = [
  "RAG combina recuperación de documentos con generación del LLM para reducir alucinaciones.",
  "Self-RAG fine-tunea el modelo para generar tokens especiales que evalúan el retrieval.",
  "El token Retrieve indica si el segmento actual necesita información externa.",
  "ISREL evalúa si el pasaje recuperado es relevante para la query original.",
  "ISSUP evalúa si el pasaje apoya la afirmación que el modelo está generando.",
  "ISUSE evalúa la utilidad global de la respuesta en una escala del 1 al 5.",
  "Los modelos de lenguaje large tienden a alucinar hechos no presentes en su preentrenamiento.",
  "BM25 es una función de recuperación léxica que supera a TF-IDF en la mayoría de benchmarks.",
  "El fine-tuning de Self-RAG requiere un corpus de reflexión generado por un critic model.",
  "Advanced RAG usa BM25 + semántico para mejorar el recall en búsqueda híbrida.",
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
    .sort((a, b) => b.score - a.score)
    .slice(0, k)
    .map(x => x.doc);
}

// ── Tokens de reflexión via prompting ─────────────────────────────────────

async function simularRetrieve(query: string, contextoPrevio: string): Promise<string> {
  const resp = await client.messages.create({
    model: MODEL,
    max_tokens: 10,
    system: "Responde únicamente con una de estas opciones: yes | no | continue",
    messages: [{
      role: "user",
      content: `Query: ${query}\nContexto generado hasta ahora: ${JSON.stringify(contextoPrevio)}\n` +
        "¿El siguiente segmento necesita recuperar documentos externos? " +
        "(yes=sí necesita; no=no necesita; continue=ya hay suficiente)",
    }],
  });
  const token = (resp.content[0] as { type: "text"; text: string }).text.trim().toLowerCase();
  return ["yes", "no", "continue"].includes(token) ? token : "no";
}

async function simularIsrel(query: string, pasaje: string): Promise<string> {
  const resp = await client.messages.create({
    model: MODEL,
    max_tokens: 10,
    system: "Responde únicamente con: relevant | irrelevant",
    messages: [{
      role: "user",
      content: `Query: ${query}\nPasaje: ${pasaje}\n¿Es relevante el pasaje para la query?`,
    }],
  });
  const token = (resp.content[0] as { type: "text"; text: string }).text.trim().toLowerCase();
  return token.includes("relevant") ? "relevant" : "irrelevant";
}

async function simularSegmento(query: string, pasaje: string, contextoPrevio: string): Promise<string> {
  const resp = await client.messages.create({
    model: MODEL,
    max_tokens: 150,
    system: "Genera un segmento conciso (1-2 frases) apoyado en el pasaje proporcionado.",
    messages: [{
      role: "user",
      content: `Query: ${query}\nPasaje de referencia: ${pasaje}\n` +
        `Respuesta generada hasta ahora: ${JSON.stringify(contextoPrevio)}\n` +
        "Genera el siguiente segmento de la respuesta:",
    }],
  });
  return (resp.content[0] as { type: "text"; text: string }).text.trim();
}

async function simularIssup(pasaje: string, segmento: string): Promise<string> {
  const resp = await client.messages.create({
    model: MODEL,
    max_tokens: 10,
    system: "Responde únicamente con: fully | partially | no",
    messages: [{
      role: "user",
      content: `Pasaje: ${pasaje}\nAfirmación: ${segmento}\n¿El pasaje apoya la afirmación?`,
    }],
  });
  const token = (resp.content[0] as { type: "text"; text: string }).text.trim().toLowerCase();
  if (token.includes("fully")) return "fully";
  if (token.includes("partial")) return "partially";
  return "no";
}

async function simularIsuse(query: string, respuesta: string): Promise<number> {
  const resp = await client.messages.create({
    model: MODEL,
    max_tokens: 5,
    system: "Responde únicamente con un número del 1 al 5.",
    messages: [{
      role: "user",
      content: `Query: ${query}\nRespuesta: ${respuesta}\n¿Cuál es la utilidad? (1=nula, 5=perfecta)`,
    }],
  });
  const txt = (resp.content[0] as { type: "text"; text: string }).text.trim();
  const n = parseInt(txt[0], 10);
  return isNaN(n) ? 3 : n;
}

// ── Pipeline Self-RAG ──────────────────────────────────────────────────────

async function selfRag(query: string, maxSegmentos = 3): Promise<string> {
  console.log(`\nQuery: ${JSON.stringify(query)}`);
  console.log("─".repeat(60));

  let respuestaAcumulada = "";
  const segmentosValidos: string[] = [];

  for (let i = 0; i < maxSegmentos; i++) {
    console.log(`\n[Segmento ${i + 1}]`);

    const retrieve = await simularRetrieve(query, respuestaAcumulada);
    console.log(`  Retrieve=${retrieve}`);

    if (retrieve === "continue") {
      console.log("  → generación suficiente, parando");
      break;
    }

    if (retrieve === "no") {
      const resp = await client.messages.create({
        model: MODEL,
        max_tokens: 100,
        messages: [{
          role: "user",
          content: `Query: ${query}\nContexto previo: ${JSON.stringify(respuestaAcumulada)}\n` +
            "Continúa la respuesta en 1-2 frases:",
        }],
      });
      const segmento = (resp.content[0] as { type: "text"; text: string }).text.trim();
      console.log(`  (sin retrieval) ${segmento.slice(0, 80)}`);
      segmentosValidos.push(segmento);
      respuestaAcumulada += " " + segmento;
      continue;
    }

    const pasajes = bm25TopK(query, 2);
    const pasaje = pasajes[0] ?? "";
    console.log(`  Pasaje: ${pasaje.slice(0, 70)}`);

    const isRel = await simularIsrel(query, pasaje);
    console.log(`  ISREL=${isRel}`);
    if (isRel === "irrelevant") {
      console.log("  → pasaje irrelevante, saltando segmento");
      continue;
    }

    const segmento = await simularSegmento(query, pasaje, respuestaAcumulada);
    console.log(`  Segmento: ${segmento.slice(0, 80)}`);

    const isSup = await simularIssup(pasaje, segmento);
    console.log(`  ISSUP=${isSup}`);
    if (isSup === "no") {
      console.log("  → segmento no apoyado por el pasaje, descartado");
      continue;
    }

    segmentosValidos.push(segmento);
    respuestaAcumulada += " " + segmento;
  }

  const respuestaFinal = segmentosValidos.join(" ").trim();
  const isUse = await simularIsuse(query, respuestaFinal);
  console.log(`\nISUSE=${isUse}/5`);
  return respuestaFinal;
}

// ── Main ──────────────────────────────────────────────────────────────────

async function main(): Promise<void> {
  const queries = [
    "¿Qué es Self-RAG y en qué se diferencia del RAG clásico?",
    "¿Cuándo conviene usar retrieval en la generación?",
  ];
  for (const query of queries) {
    const respuesta = await selfRag(query, 3);
    console.log(`\n=== Respuesta final ===\n${respuesta}\n`);
  }
}

main().catch(console.error);
