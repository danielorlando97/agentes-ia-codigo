// Cómo ejecutar: make ts SCRIPT=typescript/14-observabilidad/metricas.ts
import Anthropic from "@anthropic-ai/sdk";

const MODEL = process.env["MODEL"] ?? "claude-haiku-4-5-20251001";
const PRECIO_INPUT_POR_MTOK = 0.80;
const PRECIO_OUTPUT_POR_MTOK = 4.0;

const cliente = new Anthropic();

interface ResultadoTarea {
  taskId: string;
  completada: boolean;
  latenciaMs: number;
  inputTokens: number;
  outputTokens: number;
  toolCallsExitosos: number;
  toolCallsFallidos: number;
  error?: string;
}

function costeUsd(r: ResultadoTarea): number {
  return (
    (r.inputTokens * PRECIO_INPUT_POR_MTOK) / 1_000_000 +
    (r.outputTokens * PRECIO_OUTPUT_POR_MTOK) / 1_000_000
  );
}

class MetricasAgente {
  private resultados: ResultadoTarea[] = [];

  registrar(resultado: ResultadoTarea): void {
    this.resultados.push(resultado);
  }

  resumen(): Record<string, number> {
    const r = this.resultados;
    if (r.length === 0) return {};

    const total = r.length;
    const completadas = r.filter((t) => t.completada).length;
    const latencias = [...r.map((t) => t.latenciaMs)].sort((a, b) => a - b);
    const n = latencias.length;
    const costes = r.map(costeUsd);
    const toolOk = r.reduce((s, t) => s + t.toolCallsExitosos, 0);
    const toolErr = r.reduce((s, t) => s + t.toolCallsFallidos, 0);

    return {
      task_completion_rate: completadas / total,
      error_rate: (total - completadas) / total,
      latencia_p50_ms: latencias[Math.floor(n / 2)],
      latencia_p95_ms: latencias[Math.floor(n * 0.95)],
      cost_per_task_usd: costes.reduce((s, c) => s + c, 0) / total,
      cost_total_usd: costes.reduce((s, c) => s + c, 0),
      tool_success_rate:
        toolOk + toolErr > 0 ? toolOk / (toolOk + toolErr) : 1.0,
      total_tareas: total,
    };
  }

  alertas(umbralCompletion = 0.95, umbralP95Ms = 30_000): string[] {
    const s = this.resumen();
    const problemas: string[] = [];
    if ((s.task_completion_rate ?? 1) < umbralCompletion) {
      problemas.push(
        `task_completion_rate ${(s.task_completion_rate * 100).toFixed(1)}% < ${(umbralCompletion * 100).toFixed(0)}%`
      );
    }
    if ((s.latencia_p95_ms ?? 0) > umbralP95Ms) {
      problemas.push(
        `P95 latencia ${s.latencia_p95_ms.toFixed(0)}ms > ${umbralP95Ms.toFixed(0)}ms`
      );
    }
    return problemas;
  }
}

const TOOLS: Anthropic.Tool[] = [
  {
    name: "calcular",
    description: "Evalúa una expresión matemática simple.",
    input_schema: {
      type: "object" as const,
      properties: { expresion: { type: "string" } },
      required: ["expresion"],
    },
  },
];

function ejecutarHerramienta(nombre: string, params: Record<string, string>): [string, boolean] {
  if (nombre === "calcular") {
    try {
      const resultado = Function(`"use strict"; return (${params.expresion})`)();
      return [String(resultado), true];
    } catch (e) {
      return [String(e), false];
    }
  }
  return ["desconocida", false];
}

function generateId(len = 8): string {
  return Math.random().toString(16).slice(2).slice(0, len);
}

async function ejecutarTareaConMetricas(tarea: string): Promise<ResultadoTarea> {
  const taskId = generateId(8);
  const t0 = Date.now();
  let inputTokens = 0;
  let outputTokens = 0;
  let toolOk = 0;
  let toolErr = 0;

  const mensajes: Anthropic.MessageParam[] = [{ role: "user", content: tarea }];

  try {
    for (let i = 0; i < 10; i++) {
      const resp = await cliente.messages.create({
        model: MODEL,
        max_tokens: 256,
        tools: TOOLS,
        messages: mensajes,
      });
      inputTokens += resp.usage.input_tokens;
      outputTokens += resp.usage.output_tokens;
      mensajes.push({ role: "assistant", content: resp.content });

      if (resp.stop_reason === "end_turn") {
        return {
          taskId,
          completada: true,
          latenciaMs: Date.now() - t0,
          inputTokens,
          outputTokens,
          toolCallsExitosos: toolOk,
          toolCallsFallidos: toolErr,
        };
      }

      const toolResults: Anthropic.ToolResultBlockParam[] = [];
      for (const bloque of resp.content) {
        if (bloque.type !== "tool_use") continue;
        const [resultado, ok] = ejecutarHerramienta(
          bloque.name,
          bloque.input as Record<string, string>
        );
        if (ok) toolOk++;
        else toolErr++;
        toolResults.push({
          type: "tool_result",
          tool_use_id: bloque.id,
          content: resultado,
        });
      }
      mensajes.push({ role: "user", content: toolResults });
    }

    return {
      taskId,
      completada: false,
      latenciaMs: Date.now() - t0,
      inputTokens,
      outputTokens,
      toolCallsExitosos: toolOk,
      toolCallsFallidos: toolErr,
      error: "max iteraciones",
    };
  } catch (e) {
    return {
      taskId,
      completada: false,
      latenciaMs: Date.now() - t0,
      inputTokens,
      outputTokens,
      toolCallsExitosos: toolOk,
      toolCallsFallidos: toolErr,
      error: String(e).slice(0, 200),
    };
  }
}

(async () => {
  console.log("=== Métricas de agente ===\n");
  const metricas = new MetricasAgente();

  const TAREAS = [
    "¿Cuánto es 15 * 23?",
    "Calcula la raíz cuadrada de 144.",
    "¿Cuántos días tiene un año normal?",
  ];

  for (const tarea of TAREAS) {
    console.log(`Ejecutando: ${tarea.slice(0, 60)}`);
    const resultado = await ejecutarTareaConMetricas(tarea);
    metricas.registrar(resultado);
    const estado = resultado.completada ? "✓" : "✗";
    console.log(`  [${estado}] ${resultado.latenciaMs.toFixed(0)}ms | $${costeUsd(resultado).toFixed(5)}`);
  }

  console.log("\n─── Resumen de métricas ───");
  const s = metricas.resumen();
  for (const [k, v] of Object.entries(s)) {
    if (typeof v === "number" && !Number.isInteger(v)) {
      console.log(`  ${k}: ${v.toFixed(3)}`);
    } else {
      console.log(`  ${k}: ${v}`);
    }
  }

  const alertas = metricas.alertas();
  if (alertas.length > 0) {
    console.log(`\n[ALERTA] ${JSON.stringify(alertas)}`);
  } else {
    console.log("\n[OK] Todas las métricas dentro de umbral");
  }
})();
