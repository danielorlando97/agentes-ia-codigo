// Cómo ejecutar: make ts SCRIPT=typescript/14-observabilidad/trajectory.ts
import Anthropic from "@anthropic-ai/sdk";

const MODEL = process.env["MODEL"] ?? "claude-haiku-4-5-20251001";
const cliente = new Anthropic();

interface Paso {
  herramienta: string;
  params: Record<string, unknown>;
  resultado?: unknown;
}

function pasoToString(p: Paso): string {
  const sortedParams = Object.entries(p.params)
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([k, v]) => `${k}: ${JSON.stringify(v)}`)
    .join(", ");
  return `${p.herramienta}({${sortedParams}})`;
}

interface ResultadoTrayectoria {
  trajectoryPrecision: number;
  trajectoryRecall: number;
  trajectoryExactMatch: boolean;
  stepEfficiency: number;
  nPasosAgente: number;
  nPasosGt: number;
  primerErrorHerramienta: { step: number; agente: string; gt: string } | null;
}

function evaluarTrayectoria(
  trayectoriaAgente: Paso[],
  groundTruth: Paso[]
): ResultadoTrayectoria {
  const pasosGt = new Set(groundTruth.map(pasoToString));
  const pasosAgenteStr = trayectoriaAgente.map(pasoToString);

  const tp = pasosAgenteStr.filter((p) => pasosGt.has(p)).length;
  const precision = trayectoriaAgente.length > 0 ? tp / trayectoriaAgente.length : 0;
  const recall = groundTruth.length > 0 ? tp / groundTruth.length : 0;
  const stepEfficiency =
    trayectoriaAgente.length > 0 ? groundTruth.length / trayectoriaAgente.length : 0;
  const exactMatch =
    JSON.stringify(pasosAgenteStr) === JSON.stringify(groundTruth.map(pasoToString));

  let primerError: { step: number; agente: string; gt: string } | null = null;
  const minLen = Math.min(trayectoriaAgente.length, groundTruth.length);
  for (let i = 0; i < minLen; i++) {
    if (trayectoriaAgente[i].herramienta !== groundTruth[i].herramienta) {
      primerError = {
        step: i,
        agente: trayectoriaAgente[i].herramienta,
        gt: groundTruth[i].herramienta,
      };
      break;
    }
  }

  return {
    trajectoryPrecision: Math.round(precision * 1000) / 1000,
    trajectoryRecall: Math.round(recall * 1000) / 1000,
    trajectoryExactMatch: exactMatch,
    stepEfficiency: Math.round(stepEfficiency * 1000) / 1000,
    nPasosAgente: trayectoriaAgente.length,
    nPasosGt: groundTruth.length,
    primerErrorHerramienta: primerError,
  };
}

async function evaluarTrayectoriaConJuez(
  trayectoria: Paso[],
  objetivoTarea: string
): Promise<Record<string, unknown>> {
  const trayStr = trayectoria
    .map(
      (p, i) =>
        `Step ${i + 1}: ${p.herramienta}(${JSON.stringify(p.params)}) → ${String(p.resultado ?? "").slice(0, 100)}`
    )
    .join("\n");

  const prompt = `Evalúa si la siguiente secuencia de pasos es eficiente y correcta para el objetivo dado.

OBJETIVO: ${objetivoTarea}

PASOS EJECUTADOS:
${trayStr}

Responde en JSON con este formato exacto:
{"es_correcta": <true/false>, "es_eficiente": <true/false>, "pasos_innecesarios": [<indices base-1>], "pasos_faltantes": [<descripción>], "puntuacion": <1-10>, "razon": "<explicación breve>"}`;

  const resp = await cliente.messages.create({
    model: MODEL,
    max_tokens: 512,
    messages: [{ role: "user", content: prompt }],
  });
  const texto = (resp.content.find((b) => b.type === "text") as Anthropic.TextBlock | undefined)?.text ?? "{}";

  try {
    return JSON.parse(texto);
  } catch {
    return { error: "parse fallido", raw: texto.slice(0, 200) };
  }
}

const GROUND_TRUTH: Record<string, Paso[]> = {
  precio_cobre: [
    { herramienta: "search_web", params: { query: "precio cobre USD libra hoy" } },
    { herramienta: "parse_number", params: { texto: "$resultado_anterior" } },
  ],
  crear_issue: [
    { herramienta: "get_repo_info", params: { repo: "mi-proyecto" } },
    {
      herramienta: "create_issue",
      params: { title: "Bug encontrado", body: "Descripción del bug", labels: ["bug"] },
    },
  ],
};

(async () => {
  console.log("=== Evaluación de trayectoria ===\n");

  const gt = GROUND_TRUTH.precio_cobre;
  const trayCorrecta: Paso[] = [
    { herramienta: "search_web", params: { query: "precio cobre USD libra hoy" }, resultado: "$4.23/lb" },
    { herramienta: "parse_number", params: { texto: "$4.23/lb" }, resultado: 4.23 },
  ];
  const res = evaluarTrayectoria(trayCorrecta, gt);
  console.log("Trayectoria correcta:");
  console.log(`  Precision: ${res.trajectoryPrecision} | Recall: ${res.trajectoryRecall}`);
  console.log(`  Exact match: ${res.trajectoryExactMatch} | Efficiency: ${res.stepEfficiency}`);

  const trayIneficiente: Paso[] = [
    { herramienta: "search_web", params: { query: "precio cobre" } },
    { herramienta: "search_web", params: { query: "precio cobre USD" } },
    { herramienta: "search_web", params: { query: "precio cobre USD libra hoy" }, resultado: "$4.23/lb" },
    { herramienta: "parse_number", params: { texto: "$4.23/lb" }, resultado: 4.23 },
  ];
  const res2 = evaluarTrayectoria(trayIneficiente, gt);
  console.log("\nTrayectoria ineficiente (3 búsquedas en lugar de 1):");
  console.log(`  Precision: ${res2.trajectoryPrecision} | Recall: ${res2.trajectoryRecall}`);
  console.log(`  Exact match: ${res2.trajectoryExactMatch} | Efficiency: ${res2.stepEfficiency}`);
  console.log(`  → Un agente así cuesta ${(1 / res2.stepEfficiency).toFixed(1)}× más que el óptimo`);

  console.log("\nEvaluación con LLM-as-judge (sin ground truth):");
  const veredicto = await evaluarTrayectoriaConJuez(
    trayIneficiente,
    "Obtener el precio actual del cobre en USD/libra"
  );
  console.log(`  Es correcta: ${veredicto.es_correcta}`);
  console.log(`  Es eficiente: ${veredicto.es_eficiente}`);
  console.log(`  Puntuación: ${veredicto.puntuacion}/10`);
  console.log(`  Razón: ${String(veredicto.razon ?? "").slice(0, 200)}`);
})();
