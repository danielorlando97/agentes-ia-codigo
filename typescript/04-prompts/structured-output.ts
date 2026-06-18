// Extracción de datos estructurados de texto libre usando tres métodos.
//
// Demuestra:
// - Método 1: instrucción libre — "devuelve JSON con campos X, Y, Z"
// - Método 2: JSON schema en el prompt — schema explícito con tipos
// - Método 3: tool_use forzado — constrained decoding via herramienta
// - Métricas: tasa de fallo de parsing, precisión de extracción, tokens consumidos

// Cómo ejecutar: make ts SCRIPT=typescript/04-prompts/structured-output.ts

import Anthropic from "@anthropic-ai/sdk";

const MODEL = process.env["MODEL"] ?? "claude-sonnet-4-6";

// ─── 1. Datos de entrada ──────────────────────────────────────────────────────

interface ReviewInput {
  text: string;
  expected: {
    nombre_producto: string;
    precio: number;
    rating: number;
    aspecto_positivo: string | null;
    aspecto_negativo: string | null;
  };
}

const REVIEWS: ReviewInput[] = [
  {
    text: "Compré el 'Altavoz Bluetooth Pro X200' por 79.99€ el mes pasado. La calidad de sonido es impresionante para su precio. Le doy 4.5 sobre 5 estrellas. El único problema es que la batería dura solo 6 horas, menos de lo prometido.",
    expected: { nombre_producto: "Altavoz Bluetooth Pro X200", precio: 79.99, rating: 4.5, aspecto_positivo: "calidad de sonido", aspecto_negativo: "batería" },
  },
  {
    text: "El 'Ratón Ergonómico ErgoMaster 3000' cuesta 149€ y es una maravilla. Llevo 3 meses usándolo sin ningún problema de muñeca. Puntuación: 5/5. No tiene ningún defecto reseñable.",
    expected: { nombre_producto: "Ratón Ergonómico ErgoMaster 3000", precio: 149.0, rating: 5.0, aspecto_positivo: "sin problemas de muñeca", aspecto_negativo: null },
  },
  {
    text: "El Teclado Mecánico TechType K85 que compré por 89 euros es un fiasco total. Teclas que se atascan, ruido excesivo y el software no funciona en Mac. No doy más de 1.5 de 5. Muy decepcionado.",
    expected: { nombre_producto: "Teclado Mecánico TechType K85", precio: 89.0, rating: 1.5, aspecto_positivo: null, aspecto_negativo: "teclas, ruido, software" },
  },
];

// ─── 2. Método 1: Instrucción libre ───────────────────────────────────────────

const SYSTEM_FREE = `Extrae los datos de la siguiente reseña de producto.
Devuelve SOLO JSON válido con estos campos exactos:
{
  "nombre_producto": "nombre del producto",
  "precio": <número con decimales>,
  "rating": <número entre 1 y 5>,
  "aspecto_positivo": "descripción o null",
  "aspecto_negativo": "descripción o null"
}
Sin texto antes ni después del JSON.`;

interface ExtractionResult {
  method: string;
  rawOutput: string;
  data: Record<string, unknown>;
  parseOk: boolean;
  error: string | null;
  tokensInput: number;
  tokensOutput: number;
}

async function extractFreeInstruction(client: Anthropic, text: string): Promise<ExtractionResult> {
  const response = await client.messages.create({
    model: MODEL,
    max_tokens: 300,
    system: SYSTEM_FREE,
    messages: [{ role: "user", content: text }],
  });

  const output = (response.content[0] as Anthropic.TextBlock).text.trim();
  let data: Record<string, unknown> = {};
  let parseOk = true;
  let error: string | null = null;

  try {
    data = JSON.parse(output);
  } catch (e) {
    parseOk = false;
    error = String(e);
  }

  return {
    method: "1-instruccion-libre",
    rawOutput: output,
    data,
    parseOk,
    error,
    tokensInput: response.usage.input_tokens,
    tokensOutput: response.usage.output_tokens,
  };
}

// ─── 3. Método 2: JSON schema en el prompt ────────────────────────────────────

const SYSTEM_SCHEMA = `Extrae los datos de la reseña de producto. El output debe ser JSON válido que siga este schema:

\`\`\`json-schema
{
  "type": "object",
  "required": ["nombre_producto", "precio", "rating"],
  "properties": {
    "nombre_producto": { "type": "string", "description": "Nombre exacto del producto" },
    "precio": { "type": "number", "description": "Precio en euros" },
    "rating": { "type": "number", "description": "Puntuación del 1.0 al 5.0" },
    "aspecto_positivo": { "type": ["string", "null"], "description": "Principal aspecto positivo o null" },
    "aspecto_negativo": { "type": ["string", "null"], "description": "Principal aspecto negativo o null" }
  }
}
\`\`\`

Responde SOLO con el JSON.`;

async function extractWithSchema(client: Anthropic, text: string): Promise<ExtractionResult> {
  const response = await client.messages.create({
    model: MODEL,
    max_tokens: 300,
    system: SYSTEM_SCHEMA,
    messages: [{ role: "user", content: text }],
  });

  const output = (response.content[0] as Anthropic.TextBlock).text.trim();
  const clean = output.replace(/^```(?:json)?\n?/, "").replace(/\n?```$/, "").trim();

  let data: Record<string, unknown> = {};
  let parseOk = true;
  let error: string | null = null;

  try {
    data = JSON.parse(clean);
  } catch (e) {
    parseOk = false;
    error = String(e);
  }

  return {
    method: "2-json-schema-prompt",
    rawOutput: output,
    data,
    parseOk,
    error,
    tokensInput: response.usage.input_tokens,
    tokensOutput: response.usage.output_tokens,
  };
}

// ─── 4. Método 3: tool_use forzado ────────────────────────────────────────────

const TOOL_DEFINITION: Anthropic.Tool = {
  name: "guardar_reseña",
  description: "Guarda los datos estructurados extraídos de la reseña",
  input_schema: {
    type: "object" as const,
    required: ["nombre_producto", "precio", "rating"],
    properties: {
      nombre_producto: { type: "string", description: "Nombre exacto del producto" },
      precio: { type: "number", description: "Precio en euros" },
      rating: { type: "number", description: "Puntuación del 1.0 al 5.0" },
      aspecto_positivo: { type: "string", description: "Principal aspecto positivo, vacío si no hay" },
      aspecto_negativo: { type: "string", description: "Principal aspecto negativo, vacío si no hay" },
    },
  },
};

async function extractWithTool(client: Anthropic, text: string): Promise<ExtractionResult> {
  const response = await client.messages.create({
    model: MODEL,
    max_tokens: 300,
    tools: [TOOL_DEFINITION],
    tool_choice: { type: "tool", name: "guardar_reseña" },
    messages: [{ role: "user", content: `Extrae los datos de esta reseña:\n\n${text}` }],
  });

  const toolBlock = response.content.find((b): b is Anthropic.ToolUseBlock => b.type === "tool_use");

  if (!toolBlock) {
    return {
      method: "3-tool-use",
      rawOutput: JSON.stringify(response.content),
      data: {},
      parseOk: false,
      error: "No se recibió tool_use block",
      tokensInput: response.usage.input_tokens,
      tokensOutput: response.usage.output_tokens,
    };
  }

  const rawInput = toolBlock.input as Record<string, unknown>;
  // Normalizar: strings vacíos → null
  const normalized: Record<string, unknown> = { ...rawInput };
  for (const field of ["aspecto_positivo", "aspecto_negativo"]) {
    if (normalized[field] === "") normalized[field] = null;
  }

  return {
    method: "3-tool-use",
    rawOutput: JSON.stringify(rawInput, null, 2),
    data: normalized,
    parseOk: true,
    error: null,
    tokensInput: response.usage.input_tokens,
    tokensOutput: response.usage.output_tokens,
  };
}

// ─── 5. Evaluación de precisión ───────────────────────────────────────────────

interface EvalResult {
  fieldChecks: Record<string, boolean>;
  correctFields: number;
  totalFields: number;
}

function evaluateExtraction(data: Record<string, unknown>, expected: ReviewInput["expected"]): EvalResult {
  const checks: Record<string, boolean> = {};

  const expName = (expected.nombre_producto || "").toLowerCase();
  const gotName = String(data.nombre_producto || "").toLowerCase();
  checks.nombre_producto = expName.slice(0, 15) !== "" && gotName.includes(expName.slice(0, 15));

  try {
    checks.precio = Math.abs(Number(data.precio) - expected.precio) <= 0.5;
  } catch {
    checks.precio = false;
  }

  try {
    checks.rating = Math.abs(Number(data.rating) - expected.rating) <= 0.5;
  } catch {
    checks.rating = false;
  }

  const correct = Object.values(checks).filter(Boolean).length;
  return { fieldChecks: checks, correctFields: correct, totalFields: Object.keys(checks).length };
}

// ─── 6. Impresión de resultados ───────────────────────────────────────────────

function printReviewComparison(review: ReviewInput, results: ExtractionResult[]): void {
  console.log(`\n${"═".repeat(74)}`);
  console.log(`  RESEÑA: ${review.text.slice(0, 80)}...`);
  console.log(`${"─".repeat(74)}`);

  for (const r of results) {
    console.log(`\n  [${r.method}]`);
    console.log(`  Parsing: ${r.parseOk ? "✓" : "✗"}`);
    if (r.error) console.log(`  Error: ${r.error.slice(0, 80)}`);

    if (r.data && Object.keys(r.data).length > 0) {
      const ev = evaluateExtraction(r.data, review.expected);
      const fieldSymbols = Object.entries(ev.fieldChecks)
        .map(([k, v]) => `${k}: ${v ? "✓" : "✗"}`)
        .join("  ");
      console.log(`  Campos correctos: ${ev.correctFields}/${ev.totalFields}`);
      console.log(`  ${fieldSymbols}`);
      console.log(`  Extraído: nombre=${String(r.data.nombre_producto || "N/A").slice(0, 30)}, precio=${r.data.precio}, rating=${r.data.rating}`);
    }
    console.log(`  Tokens: ${r.tokensInput} input / ${r.tokensOutput} output`);
  }
}

function printSummary(allResults: ExtractionResult[][], reviews: ReviewInput[]): void {
  const methodNames = allResults[0].map((r) => r.method);
  const stats: Record<string, { parseOk: number; correctFields: number; tokensIn: number }> = {};
  for (const m of methodNames) stats[m] = { parseOk: 0, correctFields: 0, tokensIn: 0 };

  const n = reviews.length;
  const nFields = 3;

  for (let i = 0; i < allResults.length; i++) {
    for (const r of allResults[i]) {
      if (r.parseOk) stats[r.method].parseOk++;
      if (r.data && Object.keys(r.data).length > 0) {
        const ev = evaluateExtraction(r.data, reviews[i].expected);
        stats[r.method].correctFields += ev.correctFields;
      }
      stats[r.method].tokensIn += r.tokensInput;
    }
  }

  console.log(`\n${"═".repeat(74)}`);
  console.log("  TABLA COMPARATIVA FINAL");
  console.log(`${"═".repeat(74)}`);
  console.log(`  ${"Método".padEnd(28)} ${"Parse OK".padStart(9)} ${"Precisión".padStart(11)} ${"Tokens/in".padStart(10)}`);
  console.log(`  ${"-".repeat(60)}`);
  for (const [m, s] of Object.entries(stats)) {
    const parseRate = (s.parseOk / n * 100).toFixed(0);
    const precision = (s.correctFields / (n * nFields) * 100).toFixed(0);
    const avgTokens = (s.tokensIn / n).toFixed(0);
    console.log(`  ${m.padEnd(28)} ${parseRate.padStart(8)}% ${precision.padStart(10)}% ${avgTokens.padStart(9)}`);
  }
  console.log(`\n  El Método 3 garantiza estructura válida via tool_use forzado.`);
  console.log(`  Si los tres métodos tienen alta precisión, la instrucción libre es suficiente.`);
}

// ─── 7. Main ──────────────────────────────────────────────────────────────────

async function main(): Promise<void> {
  const client = new Anthropic();

  const allResults: ExtractionResult[][] = [];

  for (const review of REVIEWS) {
    const results = await Promise.all([
      extractFreeInstruction(client, review.text),
      extractWithSchema(client, review.text),
      extractWithTool(client, review.text),
    ]);
    printReviewComparison(review, results);
    allResults.push(results);
  }

  printSummary(allResults, REVIEWS);
}

main().catch(console.error);
