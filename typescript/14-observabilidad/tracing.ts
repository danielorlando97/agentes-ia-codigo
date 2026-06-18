// Cómo ejecutar: make ts SCRIPT=typescript/14-observabilidad/tracing.ts
import Anthropic from "@anthropic-ai/sdk";

const MODEL = process.env["MODEL"] ?? "claude-haiku-4-5-20251001";
const cliente = new Anthropic();

interface SpanData {
  nombre: string;
  traceId: string;
  spanId: string;
  parentId: string | null;
  atributos: Record<string, unknown>;
  inicioMs: number;
  finMs: number | null;
}

function generateId(len = 16): string {
  return Math.random().toString(16).slice(2).padStart(len, "0").slice(0, len);
}

class Span {
  nombre: string;
  traceId: string;
  spanId: string;
  parentId: string | null;
  atributos: Record<string, unknown>;
  inicioMs: number;
  finMs: number | null;

  constructor(nombre: string, traceId: string, parentId: string | null = null) {
    this.nombre = nombre;
    this.traceId = traceId;
    this.spanId = generateId(16);
    this.parentId = parentId;
    this.atributos = {};
    this.inicioMs = Date.now();
    this.finMs = null;
  }

  setAttribute(key: string, value: unknown): void {
    this.atributos[key] = value;
  }

  end(): void {
    this.finMs = Date.now();
  }

  get duracionMs(): number {
    if (this.finMs === null) return 0;
    return this.finMs - this.inicioMs;
  }
}

class Tracer {
  nombre: string;
  private spans: Span[];
  private activo: Span | null;

  constructor(nombre: string) {
    this.nombre = nombre;
    this.spans = [];
    this.activo = null;
  }

  startSpan(nombre: string, traceId?: string): Span {
    const tid = traceId ?? (this.activo ? this.activo.traceId : generateId(32));
    const parentId = this.activo ? this.activo.spanId : null;
    const span = new Span(nombre, tid, parentId);
    this.spans.push(span);
    this.activo = span;
    return span;
  }

  endSpan(span: Span): void {
    span.end();
    if (span.parentId) {
      const parent = [...this.spans].reverse().find((s) => s.spanId === span.parentId);
      this.activo = parent ?? null;
    } else {
      this.activo = null;
    }
  }

  report(): void {
    console.log("\n─── Trace report ───");
    for (const s of this.spans) {
      const indent = s.parentId ? "  " : "";
      console.log(`${indent}[${s.nombre}] ${s.duracionMs}ms | ${JSON.stringify(s.atributos)}`);
    }
  }
}

const tracer = new Tracer("agente");

const TOOLS: Anthropic.Tool[] = [
  {
    name: "obtener_clima",
    description: "Devuelve el clima actual de una ciudad.",
    input_schema: {
      type: "object" as const,
      properties: { ciudad: { type: "string" } },
      required: ["ciudad"],
    },
  },
];

function ejecutarHerramienta(nombre: string, params: Record<string, string>): string {
  if (nombre === "obtener_clima") {
    return `El clima en ${params.ciudad} es soleado, 22°C.`;
  }
  return `Herramienta '${nombre}' no reconocida.`;
}

async function ejecutarAgente(tarea: string, threadId: string): Promise<string> {
  const spanRaiz = tracer.startSpan("agent.run");
  spanRaiz.setAttribute("thread_id", threadId);
  spanRaiz.setAttribute("tarea", tarea.slice(0, 200));
  spanRaiz.setAttribute("gen_ai.request.model", MODEL);

  const mensajes: Anthropic.MessageParam[] = [{ role: "user", content: tarea }];
  let tokensTotales = 0;
  let step = 0;
  let resp: Anthropic.Message | null = null;

  try {
    for (let i = 0; i < 10; i++) {
      const spanLlm = tracer.startSpan("llm.call", spanRaiz.traceId);
      spanLlm.setAttribute("step", step);
      const t0 = Date.now();

      resp = await cliente.messages.create({
        model: MODEL,
        max_tokens: 512,
        tools: TOOLS,
        messages: mensajes,
      });
      const latencia = Date.now() - t0;

      spanLlm.setAttribute("gen_ai.usage.input_tokens", resp.usage.input_tokens);
      spanLlm.setAttribute("gen_ai.usage.output_tokens", resp.usage.output_tokens);
      spanLlm.setAttribute("gen_ai.response.finish_reason", resp.stop_reason);
      spanLlm.setAttribute("latencia_ms", latencia);
      tokensTotales += resp.usage.input_tokens + resp.usage.output_tokens;
      tracer.endSpan(spanLlm);

      mensajes.push({ role: "assistant", content: resp.content });

      if (resp.stop_reason === "end_turn") break;

      const toolResults: Anthropic.ToolResultBlockParam[] = [];
      for (const bloque of resp.content) {
        if (bloque.type !== "tool_use") continue;

        const spanTool = tracer.startSpan("tool.call", spanRaiz.traceId);
        spanTool.setAttribute("tool.name", bloque.name);
        spanTool.setAttribute("tool.input", JSON.stringify(bloque.input).slice(0, 300));
        const t1 = Date.now();

        const resultado = ejecutarHerramienta(bloque.name, bloque.input as Record<string, string>);
        const ok = !resultado.startsWith("Herramienta");

        spanTool.setAttribute("tool.latencia_ms", Date.now() - t1);
        spanTool.setAttribute("tool.success", ok);
        tracer.endSpan(spanTool);

        toolResults.push({
          type: "tool_result",
          tool_use_id: bloque.id,
          content: resultado,
        });
      }

      mensajes.push({ role: "user", content: toolResults });
      step++;
    }

    spanRaiz.setAttribute("tokens_totales", tokensTotales);
    spanRaiz.setAttribute("steps_totales", step + 1);
    const texto = resp?.content.find((b) => b.type === "text") as Anthropic.TextBlock | undefined;
    return texto?.text ?? "";
  } finally {
    tracer.endSpan(spanRaiz);
    tracer.report();
  }
}

(async () => {
  const threadId = generateId(32);
  console.log("=== Agente con tracing ===");
  const resultado = await ejecutarAgente("¿Qué tiempo hace en Madrid hoy?", threadId);
  console.log(`\nRespuesta: ${resultado.slice(0, 300)}`);
})();
