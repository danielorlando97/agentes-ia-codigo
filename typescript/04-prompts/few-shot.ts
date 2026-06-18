// Comparación 0-shot vs 3-shot vs 6-shot en clasificación de sentimiento.
//
// Demuestra:
// - Cómo el número de ejemplos afecta accuracy y consistencia del formato
// - Majority label bias: con ejemplos desbalanceados, el modelo predice la clase dominante
// - Tokens consumidos por cada variante

// Cómo ejecutar: make ts SCRIPT=typescript/04-prompts/few-shot.ts

import Anthropic from "@anthropic-ai/sdk";

const MODEL = process.env["MODEL"] ?? "claude-sonnet-4-6";

// ─── 1. Dataset de prueba ────────────────────────────────────────────────────

interface Review {
  text: string;
  label: "positivo" | "negativo" | "neutro";
}

const REVIEWS: Review[] = [
  {
    text: "El producto llegó perfectamente empaquetado y funciona exactamente como se describe. Muy satisfecho.",
    label: "positivo",
  },
  {
    text: "Terrible experiencia. El artículo llegó roto y el servicio de atención al cliente no respondió.",
    label: "negativo",
  },
  {
    text: "El producto cumple lo básico. No es el mejor que he usado pero tampoco es malo. Precio razonable.",
    label: "neutro",
  },
  {
    text: "¡Increíble calidad! Superó todas mis expectativas. Lo recomendaría sin dudar.",
    label: "positivo",
  },
  {
    text: "Llegó tarde y el embalaje estaba dañado. El producto funciona pero la experiencia de compra fue mala.",
    label: "negativo",
  },
];

// ─── 2. Ejemplos few-shot ────────────────────────────────────────────────────

const EXAMPLES_3_SHOT: Review[] = [
  { text: "Excelente producto, muy buena calidad y entrega rápida.", label: "positivo" },
  { text: "No me gustó nada. Tuve que devolverlo al día siguiente.", label: "negativo" },
  { text: "Hace lo que promete. Ni más ni menos.", label: "neutro" },
];

const EXAMPLES_6_SHOT: Review[] = [
  ...EXAMPLES_3_SHOT,
  { text: "Mejor compra del año, totalmente recomendado para todos.", label: "positivo" },
  { text: "Producto defectuoso. Una pérdida de dinero total.", label: "negativo" },
  { text: "Está bien para lo que cuesta. No hay mucho que decir.", label: "neutro" },
];

// ─── 3. Construcción de prompts ──────────────────────────────────────────────

function buildExamplesBlock(examples: Review[]): string {
  const lines = ["<examples>"];
  for (const ex of examples) {
    lines.push("  <example>");
    lines.push(`    <texto>${ex.text}</texto>`);
    lines.push(`    <sentimiento>${ex.label}</sentimiento>`);
    lines.push("  </example>");
  }
  lines.push("</examples>");
  return lines.join("\n");
}

function buildSystemPrompt(examples: Review[]): string {
  const base =
    "Clasifica el sentimiento de reseñas de producto como: positivo, negativo o neutro.\n" +
    "Responde SOLO con una de estas tres palabras: positivo, negativo, neutro.\n" +
    "Sin explicaciones adicionales.\n";
  if (examples.length === 0) return base;
  return base + "\n" + buildExamplesBlock(examples);
}

// ─── 4. Clasificación ────────────────────────────────────────────────────────

interface ClassificationResult {
  text: string;
  labelReal: string;
  prediccion: string;
  correcto: boolean;
  formatoValido: boolean;
  tokensInput: number;
  tokensOutput: number;
}

async function classifyReviews(
  client: Anthropic,
  systemPrompt: string,
  reviews: Review[]
): Promise<ClassificationResult[]> {
  const results: ClassificationResult[] = [];
  for (const review of reviews) {
    const response = await client.messages.create({
      model: MODEL,
      max_tokens: 20,
      system: systemPrompt,
      messages: [{ role: "user", content: review.text }],
    });

    const rawOutput = (response.content[0] as Anthropic.TextBlock).text.trim().toLowerCase();
    const match = rawOutput.match(/\b(positivo|negativo|neutro)\b/);
    const predicted = match ? match[1] : rawOutput;
    const validLabels = ["positivo", "negativo", "neutro"];

    results.push({
      text: review.text.slice(0, 50) + "...",
      labelReal: review.label,
      prediccion: predicted,
      correcto: predicted === review.label,
      formatoValido: validLabels.includes(predicted),
      tokensInput: response.usage.input_tokens,
      tokensOutput: response.usage.output_tokens,
    });
  }
  return results;
}

// ─── 5. Métricas ─────────────────────────────────────────────────────────────

interface Metrics {
  accuracy: number;
  formatConsistency: number;
  avgInputTokens: number;
  avgOutputTokens: number;
  totalInputTokens: number;
}

function computeMetrics(results: ClassificationResult[]): Metrics {
  const n = results.length;
  return {
    accuracy: results.filter((r) => r.correcto).length / n,
    formatConsistency: results.filter((r) => r.formatoValido).length / n,
    avgInputTokens: results.reduce((s, r) => s + r.tokensInput, 0) / n,
    avgOutputTokens: results.reduce((s, r) => s + r.tokensOutput, 0) / n,
    totalInputTokens: results.reduce((s, r) => s + r.tokensInput, 0),
  };
}

// ─── 6. Impresión de resultados ──────────────────────────────────────────────

function printResultsTable(variantName: string, results: ClassificationResult[], metrics: Metrics): void {
  console.log(`\n${"═".repeat(70)}`);
  console.log(`  ${variantName}`);
  console.log(`${"═".repeat(70)}`);
  console.log(`  ${"Reseña (extracto)".padEnd(40)} ${"Real".padEnd(12)} ${"Predicción".padEnd(12)} OK`);
  console.log(`  ${"-".repeat(66)}`);
  for (const r of results) {
    const ok = r.correcto ? "✓" : "✗";
    console.log(`  ${r.text.padEnd(40)} ${r.labelReal.padEnd(12)} ${r.prediccion.padEnd(12)} ${ok}`);
  }
  console.log(`\n  Accuracy:             ${(metrics.accuracy * 100).toFixed(0)}%`);
  console.log(`  Consistencia formato: ${(metrics.formatConsistency * 100).toFixed(0)}%`);
  console.log(`  Tokens input (prom):  ${metrics.avgInputTokens.toFixed(0)}`);
  console.log(`  Tokens output (prom): ${metrics.avgOutputTokens.toFixed(1)}`);
  console.log(`  Tokens input total:   ${metrics.totalInputTokens}`);
}

function printComparisonTable(allMetrics: Record<string, Metrics>): void {
  console.log(`\n${"═".repeat(70)}`);
  console.log("  TABLA COMPARATIVA");
  console.log(`${"═".repeat(70)}`);
  console.log(
    `  ${"Variante".padEnd(20)} ${"Accuracy".padStart(10)} ${"Formato".padStart(10)} ${"Tokens/call".padStart(12)} ${"Total tokens".padStart(14)}`
  );
  console.log(`  ${"-".repeat(68)}`);
  for (const [name, m] of Object.entries(allMetrics)) {
    console.log(
      `  ${name.padEnd(20)} ${(m.accuracy * 100).toFixed(0).padStart(9)}% ` +
      `${(m.formatConsistency * 100).toFixed(0).padStart(9)}% ` +
      `${m.avgInputTokens.toFixed(0).padStart(11)} ` +
      `${m.totalInputTokens.toString().padStart(13)}`
    );
  }
  console.log(`\n  Nota: 'Tokens/call' = promedio de tokens de input por clasificación`);
}

// ─── 7. Main ──────────────────────────────────────────────────────────────────

async function main(): Promise<void> {
  const client = new Anthropic();

  const variants: [string, Review[]][] = [
    ["0-shot (sin ejemplos)", []],
    ["3-shot (3 ejemplos)", EXAMPLES_3_SHOT],
    ["6-shot (6 ejemplos)", EXAMPLES_6_SHOT],
  ];

  const allMetrics: Record<string, Metrics> = {};

  for (const [name, examples] of variants) {
    const systemPrompt = buildSystemPrompt(examples);
    const results = await classifyReviews(client, systemPrompt, REVIEWS);
    const metrics = computeMetrics(results);
    printResultsTable(name, results, metrics);
    allMetrics[name] = metrics;
  }

  printComparisonTable(allMetrics);

  const base = allMetrics["0-shot (sin ejemplos)"].avgInputTokens;
  const t3 = allMetrics["3-shot (3 ejemplos)"].avgInputTokens;
  const t6 = allMetrics["6-shot (6 ejemplos)"].avgInputTokens;
  console.log(`\n  Overhead de tokens por ejemplos:`);
  console.log(`    3-shot vs 0-shot: +${(t3 - base).toFixed(0)} tokens/llamada`);
  console.log(`    6-shot vs 0-shot: +${(t6 - base).toFixed(0)} tokens/llamada`);
}

main().catch(console.error);
