// Comparación directa vs CoT explícito vs zero-shot CoT en problema aritmético multi-paso.
//
// Demuestra:
// - Variante 1: prompt directo — el modelo responde sin razonar
// - Variante 2: CoT explícito — el prompt describe los pasos intermedios a seguir
// - Variante 3: zero-shot CoT — trigger phrase "piensa paso a paso"
// - Métricas: accuracy, tokens de output (proxy de razonamiento), latencia

// Cómo ejecutar: make ts SCRIPT=typescript/04-prompts/chain-of-thought.ts

import Anthropic from "@anthropic-ai/sdk";

const MODEL = process.env["MODEL"] ?? "claude-sonnet-4-6";

// ─── 1. Problemas con trampa ──────────────────────────────────────────────────

interface Problem {
  question: string;
  answerExact: string;
  explanation: string;
}

const PROBLEMS: Problem[] = [
  {
    question:
      "Una tienda vende manzanas a 3 por €1. " +
      "Juan compra 12 manzanas y paga con un billete de €10. " +
      "¿Cuánto cambio recibe? " +
      "Nota: la tienda tiene una oferta especial hoy: si compras más de 10 manzanas, " +
      "obtienes un 20% de descuento en el total.",
    answerExact: "6.80",
    explanation:
      "12 manzanas a 3/€1 = €4.00 base. Descuento 20%: €4.00 × 0.80 = €3.20. Cambio: €10.00 − €3.20 = €6.80",
  },
  {
    question:
      "Un tren parte de Madrid a las 8:00 y llega a Barcelona a las 10:30. " +
      "Otro tren parte de Barcelona a las 9:00 y llega a Madrid a las 11:30. " +
      "Los trenes viajan en sentidos opuestos por la misma vía. " +
      "¿A qué hora se cruzan si Madrid y Barcelona están a 600 km?",
    answerExact: "9:45",
    explanation:
      "Tren A: 240 km/h. A las 9:00 lleva 240 km recorridos. Quedan 360 km entre ellos. " +
      "Se acercan a 480 km/h. 360/480 = 0.75 h = 45 min → se cruzan a las 9:45.",
  },
  {
    question:
      "Una pizzería vende pizzas pequeñas por €8 y grandes por €14. " +
      "Ayer vendió 15 pizzas en total y ganó €162. " +
      "¿Cuántas pizzas grandes vendió?",
    answerExact: "7",
    explanation:
      "g + p = 15; 14g + 8p = 162 → 14g + 8(15-g) = 162 → 6g = 42 → g = 7.",
  },
];

// ─── 2. System prompts ────────────────────────────────────────────────────────

const SYSTEM_DIRECT =
  "Resuelve el siguiente problema matemático. " +
  "Responde solo con el número o valor final, sin explicaciones.";

const SYSTEM_COT_EXPLICIT =
  "Resuelve el siguiente problema matemático siguiendo estos pasos:\n" +
  "1. Identifica los datos conocidos\n" +
  "2. Escribe la ecuación o proceso necesario\n" +
  "3. Realiza el cálculo paso a paso\n" +
  "4. Verifica el resultado\n" +
  "5. Da la respuesta final claramente indicada\n" +
  "Muestra cada paso explícitamente.";

const SYSTEM_ZERO_SHOT_COT =
  "Resuelve el siguiente problema matemático. " +
  "Piensa paso a paso antes de responder. " +
  "Muestra tu razonamiento completo y da la respuesta final al final.";

// ─── 3. Resolución con métricas ───────────────────────────────────────────────

interface SolveResult {
  variant: string;
  output: string;
  tokensInput: number;
  tokensOutput: number;
  latencyMs: number;
}

async function solveProblem(
  client: Anthropic,
  system: string,
  question: string,
  variantName: string
): Promise<SolveResult> {
  const t0 = performance.now();
  const response = await client.messages.create({
    model: MODEL,
    max_tokens: 800,
    system,
    messages: [{ role: "user", content: question }],
  });
  const latencyMs = performance.now() - t0;

  return {
    variant: variantName,
    output: (response.content[0] as Anthropic.TextBlock).text.trim(),
    tokensInput: response.usage.input_tokens,
    tokensOutput: response.usage.output_tokens,
    latencyMs,
  };
}

function checkAnswer(output: string, answerExact: string): boolean {
  return output.toLowerCase().includes(answerExact.toLowerCase());
}

// ─── 4. Impresión de resultados ───────────────────────────────────────────────

function printProblemResults(problem: Problem, results: SolveResult[]): void {
  console.log(`\n${"═".repeat(72)}`);
  console.log(`  PROBLEMA: ${problem.question.slice(0, 80)}...`);
  console.log(`  Respuesta correcta: ${problem.answerExact}`);
  console.log(`  Lógica: ${problem.explanation}`);
  console.log(`${"─".repeat(72)}`);

  for (const r of results) {
    const correct = checkAnswer(r.output, problem.answerExact);
    const status = correct ? "✓ CORRECTO" : "✗ INCORRECTO";
    console.log(`\n  [${r.variant}] ${status}`);
    console.log(`  Tokens input/output: ${r.tokensInput} / ${r.tokensOutput}`);
    console.log(`  Latencia: ${r.latencyMs.toFixed(0)} ms`);
    const preview = r.output.slice(0, 200) + (r.output.length > 200 ? "..." : "");
    console.log(`  Output: ${preview}`);
  }
}

function printSummary(allResults: SolveResult[][], problems: Problem[]): void {
  const variantNames = allResults[0].map((r) => r.variant);
  const stats: Record<string, { correct: number; tokensOut: number; latency: number }> = {};
  for (const v of variantNames) {
    stats[v] = { correct: 0, tokensOut: 0, latency: 0 };
  }

  for (let i = 0; i < allResults.length; i++) {
    const problem = problems[i];
    for (const r of allResults[i]) {
      if (checkAnswer(r.output, problem.answerExact)) stats[r.variant].correct++;
      stats[r.variant].tokensOut += r.tokensOutput;
      stats[r.variant].latency += r.latencyMs;
    }
  }

  const n = problems.length;
  console.log(`\n${"═".repeat(72)}`);
  console.log("  TABLA COMPARATIVA AGREGADA");
  console.log(`${"═".repeat(72)}`);
  console.log(
    `  ${"Variante".padEnd(30)} ${"Accuracy".padStart(10)} ${"Tokens out".padStart(12)} ${"Latencia".padStart(12)}`
  );
  console.log(`  ${"-".repeat(64)}`);
  for (const [v, s] of Object.entries(stats)) {
    const acc = (s.correct / n) * 100;
    const avgTok = s.tokensOut / n;
    const avgLat = s.latency / n;
    console.log(
      `  ${v.padEnd(30)} ${acc.toFixed(0).padStart(9)}% ${avgTok.toFixed(0).padStart(11)} ${avgLat.toFixed(0).padStart(10)} ms`
    );
  }
  console.log(`\n  Nota: 'Tokens out' es proxy del razonamiento generado.`);
  console.log(`  CoT produce más tokens porque muestra pasos intermedios.`);
}

// ─── 5. Main ──────────────────────────────────────────────────────────────────

async function main(): Promise<void> {
  const client = new Anthropic();

  const variants: [string, string][] = [
    ["Directo (sin CoT)", SYSTEM_DIRECT],
    ["CoT explícito", SYSTEM_COT_EXPLICIT],
    ["Zero-shot CoT", SYSTEM_ZERO_SHOT_COT],
  ];

  const allResults: SolveResult[][] = [];

  for (const problem of PROBLEMS) {
    const problemResults: SolveResult[] = [];
    for (const [name, system] of variants) {
      const result = await solveProblem(client, system, problem.question, name);
      problemResults.push(result);
    }
    printProblemResults(problem, problemResults);
    allResults.push(problemResults);
  }

  printSummary(allResults, PROBLEMS);
}

main().catch(console.error);
