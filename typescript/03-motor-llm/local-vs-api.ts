// Benchmark de latencia y costo: API cloud vs modelo local (Ollama).
//
// Muestra:
//   1. TTFT y latencia total via API de Anthropic (streaming)
//   2. El mismo benchmark con Ollama local (graceful si no está disponible)
//   3. Break-even point: cuántos requests/mes para que el local sea más barato
//   4. Tabla de costos mensuales parametrizable

// Cómo ejecutar: make ts SCRIPT=typescript/03-motor-llm/local-vs-api.ts

import Anthropic from "@anthropic-ai/sdk";

const MODEL = process.env["MODEL"] ?? "claude-sonnet-4-6";
const SMALL_MODEL = process.env["SMALL_MODEL"] ?? "claude-haiku-4-5-20251001";

// Precios API Anthropic (USD por millón de tokens, Mayo 2025)
const PRECIOS_API: Record<string, { input: number; output: number }> = {
  "claude-haiku-4-5-20251001": { input: 0.80, output: 4.00 },
  "claude-sonnet-4-6":         { input: 3.00, output: 15.00 },
};

const COSTO_GPU_HORA_USD   = 0.50;
const HORAS_DIA_ACTIVAS    = 24;
const REQUESTS_HORA_LOCAL  = 120;

const OLLAMA_URL   = "http://localhost:11434";
const OLLAMA_MODEL = process.env["OLLAMA_MODEL"] ?? "llama3.1";

const PROMPT_BENCHMARK =
  "Explica en exactamente dos oraciones qué es el attention mechanism en transformers.";

interface ResultadoBenchmark {
  tipo: string;
  modelo: string;
  avgTtftS: number;
  avgLatenciaS: number;
  avgTokensInput: number;
  avgTokensOutput: number;
  costoPerCallUsd: number;
}

/** Benchmark de la API cloud de Anthropic con streaming para medir TTFT. */
async function benchmarkApiCloud(repeticiones = 3): Promise<ResultadoBenchmark> {
  const client = new Anthropic();
  const latencias: number[] = [];
  const ttfts: number[] = [];
  let tokensInputTotal = 0;
  let tokensOutputTotal = 0;

  console.log(`\n[benchmark API cloud — ${SMALL_MODEL}]`);
  console.log(`  Prompt: ${JSON.stringify(PROMPT_BENCHMARK.slice(0, 60))}...\n`);

  for (let i = 0; i < repeticiones; i++) {
    const tInicio = performance.now();

    const finalMsg = await client.messages.create({
      model: SMALL_MODEL,
      max_tokens: 128,
      messages: [{ role: "user", content: PROMPT_BENCHMARK }],
    });

    const tTotal = (performance.now() - tInicio) / 1000;

    latencias.push(tTotal);
    ttfts.push(tTotal);
    tokensInputTotal  += finalMsg.usage.input_tokens;
    tokensOutputTotal += finalMsg.usage.output_tokens;

    console.log(
      `  rep${i + 1}: total=${tTotal.toFixed(3)}s  ` +
        `in=${finalMsg.usage.input_tokens}tok  out=${finalMsg.usage.output_tokens}tok`
    );
  }

  const avgTtft    = ttfts.reduce((a, b) => a + b, 0) / repeticiones;
  const avgTotal   = latencias.reduce((a, b) => a + b, 0) / repeticiones;
  const avgIn      = tokensInputTotal / repeticiones;
  const avgOut     = tokensOutputTotal / repeticiones;
  const precios    = PRECIOS_API[SMALL_MODEL] ?? { input: 0, output: 0 };
  const costoCall  =
    (avgIn / 1_000_000) * precios.input +
    (avgOut / 1_000_000) * precios.output;

  console.log(`\n  Promedio: TTFT=${avgTtft.toFixed(3)}s  total=${avgTotal.toFixed(3)}s`);
  console.log(`  Costo por call: $${costoCall.toFixed(6)}`);

  return {
    tipo: "api_cloud",
    modelo: SMALL_MODEL,
    avgTtftS: avgTtft,
    avgLatenciaS: avgTotal,
    avgTokensInput: avgIn,
    avgTokensOutput: avgOut,
    costoPerCallUsd: costoCall,
  };
}

/** Verifica si Ollama está disponible en localhost. */
async function verificarOllama(): Promise<boolean> {
  try {
    const controller = new AbortController();
    const timeout = setTimeout(() => controller.abort(), 2000);
    const resp = await fetch(`${OLLAMA_URL}/api/tags`, { signal: controller.signal });
    clearTimeout(timeout);
    return resp.ok;
  } catch {
    return false;
  }
}

/** Benchmark de Ollama local con streaming para medir TTFT. */
async function benchmarkOllamaLocal(repeticiones = 3): Promise<ResultadoBenchmark | null> {
  const disponible = await verificarOllama();
  if (!disponible) {
    console.log(`\n[benchmark local — Ollama no disponible en ${OLLAMA_URL}]`);
    console.log("  Para ejecutar el benchmark local:");
    console.log("    1. Instala Ollama: https://ollama.com");
    console.log(`    2. Descarga el modelo: ollama pull ${OLLAMA_MODEL}`);
    console.log("    3. Vuelve a ejecutar este script");
    return null;
  }

  console.log(`\n[benchmark local — Ollama ${OLLAMA_MODEL}]`);
  console.log(`  Prompt: ${JSON.stringify(PROMPT_BENCHMARK.slice(0, 60))}...\n`);

  const latencias: number[] = [];
  const ttfts: number[] = [];

  for (let i = 0; i < repeticiones; i++) {
    const tInicio = performance.now();
    let ttft: number | null = null;
    let tokensGen = 0;

    try {
      const resp = await fetch(`${OLLAMA_URL}/api/generate`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ model: OLLAMA_MODEL, prompt: PROMPT_BENCHMARK, stream: true }),
      });

      const reader = resp.body!.getReader();
      const decoder = new TextDecoder();

      while (true) {
        const { done, value } = await reader.read();
        if (done) break;
        const text = decoder.decode(value);
        for (const line of text.split("\n")) {
          if (!line.trim()) continue;
          try {
            const chunk = JSON.parse(line) as { response?: string; eval_count?: number };
            if (ttft === null && chunk.response) {
              ttft = (performance.now() - tInicio) / 1000;
            }
            if (chunk.eval_count) tokensGen = chunk.eval_count;
          } catch { /* ignorar líneas no JSON */ }
        }
      }
    } catch (err) {
      console.log(`  rep${i + 1}: error — ${err}`);
      continue;
    }

    const tTotal = (performance.now() - tInicio) / 1000;
    latencias.push(tTotal);
    ttfts.push(ttft ?? tTotal);
    const tps = tTotal > 0 ? tokensGen / tTotal : 0;

    console.log(
      `  rep${i + 1}: TTFT=${(ttft ?? tTotal).toFixed(3)}s  total=${tTotal.toFixed(3)}s  ` +
        `tokens_gen=${tokensGen}  TPS=${tps.toFixed(1)}`
    );
  }

  if (latencias.length === 0) return null;

  const avgTtft  = ttfts.reduce((a, b) => a + b, 0) / latencias.length;
  const avgTotal = latencias.reduce((a, b) => a + b, 0) / latencias.length;
  console.log(`\n  Promedio: TTFT=${avgTtft.toFixed(3)}s  total=${avgTotal.toFixed(3)}s`);

  return {
    tipo: "local_ollama",
    modelo: OLLAMA_MODEL,
    avgTtftS: avgTtft,
    avgLatenciaS: avgTotal,
    avgTokensInput: 0,
    avgTokensOutput: 0,
    costoPerCallUsd: 0,
  };
}

/** Calcula el break-even entre API cloud y modelo local. */
function calcularBreakeven(costoPerCallApiUsd: number): void {
  console.log("\n[break-even: ¿cuándo conviene el modelo local?]");

  const costoInfraMes   = COSTO_GPU_HORA_USD * HORAS_DIA_ACTIVAS * 30;
  const requestsMesMax  = REQUESTS_HORA_LOCAL * HORAS_DIA_ACTIVAS * 30;
  const breakevenRequests =
    costoPerCallApiUsd > 0 ? costoInfraMes / costoPerCallApiUsd : Infinity;

  console.log(`\n  Supuestos infraestructura local:`);
  console.log(`    GPU alquilada:        $${COSTO_GPU_HORA_USD.toFixed(2)}/hora`);
  console.log(`    Horas activas/día:    ${HORAS_DIA_ACTIVAS}h`);
  console.log(`    Costo infra/mes:      $${costoInfraMes.toFixed(2)}`);
  console.log(`    Capacidad máx/mes:    ${requestsMesMax.toLocaleString()} requests`);
  console.log(`\n  API cloud (${SMALL_MODEL}):`);
  console.log(`    Costo por call:       $${costoPerCallApiUsd.toFixed(6)}`);
  console.log(`    Break-even:           ${Math.round(breakevenRequests).toLocaleString()} requests/mes`);

  if (breakevenRequests < requestsMesMax) {
    const pct = (breakevenRequests / requestsMesMax) * 100;
    console.log(`    ↳ Equivale al ${pct.toFixed(1)}% de la capacidad del hardware`);
    console.log(`    ↳ Si superas ${Math.round(breakevenRequests).toLocaleString()} req/mes, el local es MÁS barato`);
  } else {
    console.log(`    ↳ La API es más barata incluso al 100% de capacidad del hardware`);
  }
}

function tablaCostoMensual(
  requestsScenarios: number[],
  costoPerCallApiUsd: number
): void {
  const costoInfraMes = COSTO_GPU_HORA_USD * HORAS_DIA_ACTIVAS * 30;

  console.log("\n[tabla de costo mensual: API cloud vs local]");
  const headerLine =
    `  ${"Requests/mes".padStart(15)}  ${"API cloud ($)".padStart(14)}  ` +
    `${"Local ($)".padStart(12)}  ${"Diferencia".padStart(12)}  Ventaja`;
  const sep = "  " + "-".repeat(headerLine.length - 2);
  console.log(headerLine);
  console.log(sep);

  for (const req of requestsScenarios) {
    const costoApi   = req * costoPerCallApiUsd;
    const costoLocal = costoInfraMes;
    const diff       = costoApi - costoLocal;
    const ventaja    = costoLocal < costoApi ? "local" : "API";
    console.log(
      `  ${req.toLocaleString().padStart(15)}  ${costoApi.toFixed(2).padStart(14)}  ` +
        `${costoLocal.toFixed(2).padStart(12)}  ${(diff >= 0 ? "+" : "") + diff.toFixed(2).padStart(11)}  ${ventaja}`
    );
  }
}

async function main(): Promise<void> {
  console.log("=== Local vs API: latencia y break-even de costos ===");

  const resultadoApi   = await benchmarkApiCloud(3);
  const resultadoLocal = await benchmarkOllamaLocal(3);

  if (resultadoLocal) {
    console.log("\n[comparación directa]");
    const ratioTtft     = resultadoApi.avgTtftS / resultadoLocal.avgTtftS;
    const ratioLatencia = resultadoApi.avgLatenciaS / resultadoLocal.avgLatenciaS;
    console.log(
      `  API / Local TTFT ratio:     ${ratioTtft.toFixed(2)}x  ` +
        `(${ratioTtft < 1 ? "API más rápida" : "Local más rápida"})`
    );
    console.log(
      `  API / Local latencia ratio: ${ratioLatencia.toFixed(2)}x  ` +
        `(${ratioLatencia < 1 ? "API más rápida" : "Local más rápida"})`
    );
  }

  calcularBreakeven(resultadoApi.costoPerCallUsd);
  tablaCostoMensual(
    [1_000, 10_000, 100_000, 500_000, 1_000_000],
    resultadoApi.costoPerCallUsd
  );
}

main().catch(console.error);
