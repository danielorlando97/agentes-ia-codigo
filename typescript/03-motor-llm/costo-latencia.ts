// Medir métricas clave de una sesión multi-turn con tool calls.
//
// Muestra:
//   1. TTFT (time to first token) por turno
//   2. TPOT (time per output token) en ms por turno
//   3. Tokens de input/output acumulados por turno
//   4. Costo total de la sesión y costo por tarea vs costo por token
//   5. Tabla resumen de la sesión completa

// Cómo ejecutar: make ts SCRIPT=typescript/03-motor-llm/costo-latencia.ts

import Anthropic from "@anthropic-ai/sdk";

const MODEL = process.env["MODEL"] ?? "claude-sonnet-4-6";
const SMALL_MODEL = process.env["SMALL_MODEL"] ?? "claude-haiku-4-5-20251001";

// Precios Haiku 4.5 (USD por millón de tokens, Mayo 2025)
const PRECIO_INPUT  = 0.80;
const PRECIO_OUTPUT = 4.00;

const HERRAMIENTAS: Anthropic.Tool[] = [
  {
    name: "calcular",
    description:
      "Realiza operaciones matemáticas. " +
      "Operaciones disponibles: suma, resta, multiplicacion, division, potencia.",
    input_schema: {
      type: "object",
      properties: {
        operacion: {
          type: "string",
          enum: ["suma", "resta", "multiplicacion", "division", "potencia"],
        },
        a: { type: "number", description: "Primer operando" },
        b: { type: "number", description: "Segundo operando" },
      },
      required: ["operacion", "a", "b"],
    },
  },
];

const TAREA =
  "Necesito resolver un problema en tres pasos:\n" +
  "1. Calcula 347 × 89\n" +
  "2. Al resultado anterior, réstale 5000\n" +
  "3. Eleva el resultado al cuadrado\n" +
  "Muéstrame los tres resultados.";

interface MetricasTurno {
  turno: number;
  ttftS: number;
  latenciaTotalS: number;
  tokensInput: number;
  tokensOutput: number;
  tpotMs: number;
  toolCalls: number;
  costoUsd: number;
}

/** Ejecuta una operación matemática mock. */
function ejecutarCalculadora(
  operacion: string,
  a: number,
  b: number
): string {
  const ops: Record<string, number> = {
    suma:            a + b,
    resta:           a - b,
    multiplicacion:  a * b,
    division:        b !== 0 ? a / b : Infinity,
    potencia:        a ** b,
  };
  const resultado = ops[operacion] ?? "operación desconocida";
  return JSON.stringify({ resultado, operacion, a, b });
}

/** Llama a la API midiendo TTFT y latencia total vía streaming. */
async function llamarConMetricas(
  client: Anthropic,
  mensajes: Anthropic.MessageParam[],
  turno: number
): Promise<[Anthropic.Message, MetricasTurno]> {
  const tInicio = performance.now();

  const finalMsg = await client.messages.create({
    model: SMALL_MODEL,
    max_tokens: 512,
    tools: HERRAMIENTAS,
    messages: mensajes,
  });

  const latenciaTotal = (performance.now() - tInicio) / 1000;

  const tokensOutput = finalMsg.usage.output_tokens;
  const tpotMs = tokensOutput > 0 ? (latenciaTotal * 1000) / tokensOutput : 0;
  const toolCalls = finalMsg.content.filter((b) => b.type === "tool_use").length;
  const costoUsd =
    (finalMsg.usage.input_tokens / 1_000_000) * PRECIO_INPUT +
    (tokensOutput / 1_000_000) * PRECIO_OUTPUT;

  const metricas: MetricasTurno = {
    turno,
    ttftS: latenciaTotal,
    latenciaTotalS: latenciaTotal,
    tokensInput: finalMsg.usage.input_tokens,
    tokensOutput,
    tpotMs,
    toolCalls,
    costoUsd,
  };

  return [finalMsg, metricas];
}

/** Ejecuta la sesión multi-turn y devuelve las métricas por turno. */
async function ejecutarSesionMultiturn(): Promise<MetricasTurno[]> {
  const client = new Anthropic();
  const mensajes: Anthropic.MessageParam[] = [{ role: "user", content: TAREA }];
  const metricasSesion: MetricasTurno[] = [];
  let turno = 1;

  console.log("\n[sesión multi-turn con tool calls]");
  console.log(`  Tarea: ${JSON.stringify(TAREA.slice(0, 80))}...\n`);

  while (turno <= 10) {
    console.log(`  --- Turno ${turno} ---`);
    const [resp, metricas] = await llamarConMetricas(client, mensajes, turno);
    metricasSesion.push(metricas);

    console.log(
      `  TTFT=${metricas.ttftS.toFixed(3)}s  ` +
        `total=${metricas.latenciaTotalS.toFixed(3)}s  ` +
        `TPOT=${metricas.tpotMs.toFixed(1)}ms/tok  ` +
        `in=${metricas.tokensInput}  out=${metricas.tokensOutput}  ` +
        `tool_calls=${metricas.toolCalls}  ` +
        `costo=$${metricas.costoUsd.toFixed(6)}`
    );

    mensajes.push({ role: "assistant", content: resp.content });

    const toolUses = resp.content.filter(
      (b): b is Anthropic.ToolUseBlock => b.type === "tool_use"
    );

    if (toolUses.length === 0) {
      const textoFinal = resp.content
        .filter((b): b is Anthropic.TextBlock => b.type === "text")
        .map((b) => b.text)
        .join("");
      console.log(`\n  Respuesta final: ${JSON.stringify(textoFinal.slice(0, 200))}`);
      break;
    }

    const resultadosTools: Anthropic.ToolResultBlockParam[] = toolUses.map((tu) => {
      const args = tu.input as { operacion: string; a: number; b: number };
      const resultado = ejecutarCalculadora(args.operacion, args.a, args.b);
      console.log(`  Tool: calcular(${args.operacion}, ${args.a}, ${args.b}) → ${resultado}`);
      return { type: "tool_result", tool_use_id: tu.id, content: resultado };
    });

    mensajes.push({ role: "user", content: resultadosTools });
    turno++;
  }

  return metricasSesion;
}

/** Imprime la tabla resumen de la sesión. */
function tablaResumenSesion(metricasSesion: MetricasTurno[]): void {
  console.log("\n[tabla resumen de la sesión]");

  const header =
    `  ${"Turno".padStart(6)}  ${"TTFT(s)".padStart(8)}  ${"Total(s)".padStart(9)}  ` +
    `${"TPOT(ms)".padStart(9)}  ${"In tok".padStart(7)}  ${"Out tok".padStart(8)}  ` +
    `${"Tool calls".padStart(11)}  ${"Costo ($)".padStart(10)}`;
  const sep = "  " + "-".repeat(header.length - 2);

  console.log(header);
  console.log(sep);

  let tokensInTotal  = 0;
  let tokensOutTotal = 0;
  let costoTotal     = 0;

  for (const m of metricasSesion) {
    console.log(
      `  ${String(m.turno).padStart(6)}  ${m.ttftS.toFixed(3).padStart(8)}  ` +
        `${m.latenciaTotalS.toFixed(3).padStart(9)}  ` +
        `${m.tpotMs.toFixed(1).padStart(9)}  ` +
        `${String(m.tokensInput).padStart(7)}  ${String(m.tokensOutput).padStart(8)}  ` +
        `${String(m.toolCalls).padStart(11)}  ${m.costoUsd.toFixed(6).padStart(10)}`
    );
    tokensInTotal  += m.tokensInput;
    tokensOutTotal += m.tokensOutput;
    costoTotal     += m.costoUsd;
  }

  const latenciaTotal = metricasSesion.reduce((a, m) => a + m.latenciaTotalS, 0);
  console.log(sep);
  console.log(
    `  ${"TOTAL".padStart(6)}  ${"".padStart(8)}  ${latenciaTotal.toFixed(3).padStart(9)}  ` +
      `${"".padStart(9)}  ${String(tokensInTotal).padStart(7)}  ` +
      `${String(tokensOutTotal).padStart(8)}  ${"".padStart(11)}  ` +
      `${costoTotal.toFixed(6).padStart(10)}`
  );

  console.log(`\n  Costo por tarea completa:   $${costoTotal.toFixed(6)}`);
  if (tokensOutTotal > 0) {
    const costoMillon = (costoTotal / tokensOutTotal) * 1_000_000;
    console.log(`  Costo por token de output:  $${costoMillon.toFixed(4)}/millón`);
  }
  if (tokensInTotal > 0) {
    const costoMillonIn = (costoTotal / tokensInTotal) * 1_000_000;
    console.log(`  Costo por token de input:   $${costoMillonIn.toFixed(4)}/millón`);
  }

  const overhead = tokensInTotal - (metricasSesion[0]?.tokensInput ?? 0);
  console.log(`\n  Overhead de historial acumulado: ${overhead} tokens extra de input`);
  console.log(`  (Los tool schemas se cuentan en cada turno)`);
}

async function main(): Promise<void> {
  console.log("=== Costo y latencia: sesión multi-turn con tool calls ===");
  const metricas = await ejecutarSesionMultiturn();
  tablaResumenSesion(metricas);
}

main().catch(console.error);
