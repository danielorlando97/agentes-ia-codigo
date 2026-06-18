// Cómo ejecutar: make ts SCRIPT=typescript/14-observabilidad/logs.ts
import Anthropic from "@anthropic-ai/sdk";

const MODEL = process.env["MODEL"] ?? "claude-haiku-4-5-20251001";
const cliente = new Anthropic();

class StructLogger {
  private ctx: Record<string, unknown>;

  constructor(ctx: Record<string, unknown> = {}) {
    this.ctx = ctx;
  }

  bind(extra: Record<string, unknown>): StructLogger {
    return new StructLogger({ ...this.ctx, ...extra });
  }

  private emit(nivel: string, evento: string, campos: Record<string, unknown>): void {
    const registro = {
      ts: new Date().toISOString().replace(/\.\d{3}Z$/, "Z"),
      nivel,
      evento,
      ...this.ctx,
      ...campos,
    };
    console.log(JSON.stringify(registro));
  }

  info(evento: string, campos: Record<string, unknown> = {}): void {
    this.emit("INFO", evento, campos);
  }

  error(evento: string, campos: Record<string, unknown> = {}): void {
    this.emit("ERROR", evento, campos);
  }

  warn(evento: string, campos: Record<string, unknown> = {}): void {
    this.emit("WARN", evento, campos);
  }
}

const baseLogger = new StructLogger({ agente_version: "1.0.0", entorno: "demo" });

function crearLoggerSesion(threadId: string, userId: string, sessionId: string): StructLogger {
  return baseLogger.bind({ thread_id: threadId, user_id: userId, session_id: sessionId });
}

const TOOLS: Anthropic.Tool[] = [
  {
    name: "buscar_info",
    description: "Busca información sobre un tema.",
    input_schema: {
      type: "object" as const,
      properties: { tema: { type: "string" } },
      required: ["tema"],
    },
  },
];

function ejecutarHerramienta(nombre: string, params: Record<string, string>): [string, boolean] {
  if (nombre === "buscar_info") {
    return [`Información sobre ${params.tema}: dato relevante de ejemplo.`, true];
  }
  return ["Herramienta no reconocida.", false];
}

function generateId(): string {
  return Math.random().toString(16).slice(2) + Math.random().toString(16).slice(2);
}

async function ejecutarAgente(tarea: string, userId: string): Promise<string> {
  const threadId = generateId();
  const sessionId = generateId();
  const log = crearLoggerSesion(threadId, userId, sessionId);

  log.info("task.started", { tarea: tarea.slice(0, 200), modelo: MODEL });
  const tInicio = Date.now();

  const mensajes: Anthropic.MessageParam[] = [{ role: "user", content: tarea }];
  let step = 0;
  let tokensInput = 0;
  let tokensOutput = 0;
  let resp: Anthropic.Message | null = null;

  try {
    for (let i = 0; i < 10; i++) {
      log.info("llm.call.started", { step, modelo: MODEL });
      const t0 = Date.now();

      try {
        resp = await cliente.messages.create({
          model: MODEL,
          max_tokens: 512,
          tools: TOOLS,
          messages: mensajes,
        });
        const latencia = Date.now() - t0;
        tokensInput += resp.usage.input_tokens;
        tokensOutput += resp.usage.output_tokens;
        log.info("llm.call.completed", {
          step,
          input_tokens: resp.usage.input_tokens,
          output_tokens: resp.usage.output_tokens,
          finish_reason: resp.stop_reason,
          latencia_ms: latencia,
        });
      } catch (e) {
        const err = e as Error;
        log.error("llm.call.failed", {
          step,
          error_type: err.constructor.name,
          error_msg: err.message.slice(0, 500),
        });
        throw e;
      }

      mensajes.push({ role: "assistant", content: resp.content });

      if (resp.stop_reason === "end_turn") break;

      const toolResults: Anthropic.ToolResultBlockParam[] = [];
      for (const bloque of resp.content) {
        if (bloque.type !== "tool_use") continue;

        log.info("tool.execution.started", {
          step,
          tool: bloque.name,
          params: JSON.stringify(bloque.input).slice(0, 300),
        });
        const t1 = Date.now();

        const [resultado, ok] = ejecutarHerramienta(
          bloque.name,
          bloque.input as Record<string, string>
        );
        const latenciaTool = Date.now() - t1;

        if (ok) {
          log.info("tool.execution.completed", {
            step,
            tool: bloque.name,
            success: true,
            latencia_ms: latenciaTool,
          });
        } else {
          log.error("tool.execution.failed", {
            step,
            tool: bloque.name,
            error: resultado.slice(0, 300),
            latencia_ms: latenciaTool,
          });
        }

        toolResults.push({
          type: "tool_result",
          tool_use_id: bloque.id,
          content: resultado,
        });
      }

      mensajes.push({ role: "user", content: toolResults });
      step++;
    }

    const texto = resp?.content.find((b) => b.type === "text") as Anthropic.TextBlock | undefined;
    const duracion = Date.now() - tInicio;
    log.info("task.completed", {
      duracion_ms: duracion,
      steps: step + 1,
      tokens_input: tokensInput,
      tokens_output: tokensOutput,
    });
    return texto?.text ?? "";
  } catch (e) {
    const err = e as Error;
    const duracion = Date.now() - tInicio;
    log.error("task.failed", {
      error_type: err.constructor.name,
      error_msg: err.message.slice(0, 500),
      duracion_ms: duracion,
      steps: step,
    });
    throw e;
  }
}

(async () => {
  console.log("=== Logging estructurado ===\n");
  const resultado = await ejecutarAgente("¿Qué es la computación cuántica?", "user_demo");
  console.log(`\nRespuesta: ${resultado.slice(0, 300)}`);
})();
