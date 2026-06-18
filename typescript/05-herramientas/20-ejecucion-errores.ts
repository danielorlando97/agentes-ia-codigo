// Ejecución y manejo de errores en tool calling.
//
// El 20-40% de tool calls en producción encuentran algún tipo de error.
// Este ejecutor distingue entre errores transitorios (retry con backoff)
// y errores determinísticos (fail fast), y devuelve errores formativos
// al modelo para que pueda autocorregir su llamada.
//
// El agent loop tiene cinco stop_reason posibles, no dos:
// end_turn, tool_use, max_tokens, pause_turn, refusal.

// Cómo ejecutar: make ts SCRIPT=typescript/05-herramientas/20-ejecucion-errores.ts

import Anthropic from "@anthropic-ai/sdk";

const MODEL = process.env["MODEL"] ?? "claude-sonnet-4-6";
const MAX_ITERATIONS = 20;

// --- Tipos de error ---

class ToolNotFoundError extends Error {
  constructor(public toolName: string) {
    super(`Herramienta '${toolName}' no registrada`);
    this.name = "ToolNotFoundError";
  }
}

class ToolTimeoutError extends Error {
  constructor(
    public toolName: string,
    public timeoutMs: number
  ) {
    super(`${toolName} no completó en ${timeoutMs}ms`);
    this.name = "ToolTimeoutError";
  }
}

class AuthError extends Error {
  constructor(public resource: string) {
    super(`Sin permisos para acceder a ${resource}`);
    this.name = "AuthError";
  }
}

class RateLimitError extends Error {
  constructor(public retryAfterMs: number) {
    super(`Rate limit excedido. Reintenta en ${retryAfterMs}ms`);
    this.name = "RateLimitError";
  }
}

// --- Herramientas mock con distintos comportamientos de error ---

type ToolFn = (args: Record<string, unknown>) => Promise<string>;

async function toolFetchData(args: Record<string, unknown>): Promise<string> {
  const { source } = args as { source: string };
  if (source === "restricted") {
    throw new AuthError(source);
  }
  if (source === "slow") {
    // Simulará timeout desde el ejecutor
    await new Promise((r) => setTimeout(r, 5000));
    return "datos muy tardíos";
  }
  return JSON.stringify({ data: `datos de ${source}`, rows: 42 });
}

async function toolCalculate(args: Record<string, unknown>): Promise<string> {
  const { expression } = args as { expression: string };
  const sanitized = (expression as string).replace(/[^0-9+\-*/().\s]/g, "");
  const result = Function(`"use strict"; return (${sanitized})`)() as number;
  return String(result);
}

async function toolSaveFile(args: Record<string, unknown>): Promise<string> {
  const { path, content } = args as { path: string; content: string };
  if (!path || !content) {
    throw new Error("path y content son requeridos");
  }
  // Simular escritura exitosa
  return `Archivo guardado: ${path} (${content.length} bytes)`;
}

// Registry de herramientas
const TOOL_REGISTRY: Record<string, ToolFn> = {
  fetch_data: toolFetchData,
  calculate: toolCalculate,
  save_file: toolSaveFile,
};

const TOOLS: Anthropic.Tool[] = [
  {
    name: "fetch_data",
    description: "Obtiene datos de una fuente. source puede ser: 'database', 'api', 'cache', 'restricted' (sin permisos), 'slow' (timeout).",
    input_schema: {
      type: "object",
      properties: {
        source: { type: "string", description: "Nombre de la fuente de datos" },
      },
      required: ["source"],
    },
  },
  {
    name: "calculate",
    description: "Evalúa una expresión matemática.",
    input_schema: {
      type: "object",
      properties: {
        expression: { type: "string" },
      },
      required: ["expression"],
    },
  },
  {
    name: "save_file",
    description: "Guarda contenido en un archivo.",
    input_schema: {
      type: "object",
      properties: {
        path: { type: "string" },
        content: { type: "string" },
      },
      required: ["path", "content"],
    },
  },
];

// --- Retry con backoff exponencial ---

async function conBackoff<T>(
  fn: () => Promise<T>,
  maxRetries: number,
  baseDelayMs = 100
): Promise<T> {
  for (let intento = 0; intento < maxRetries; intento++) {
    try {
      return await fn();
    } catch (e) {
      if (intento === maxRetries - 1) throw e;
      // Solo reintentar errores transitorios
      if (e instanceof AuthError || e instanceof ToolNotFoundError) throw e;

      const delay = baseDelayMs * 2 ** intento;
      const jitter = delay * 0.1 * (Math.random() * 2 - 1);
      const wait = Math.round(delay + jitter);
      console.log(
        `    [backoff] intento ${intento + 1}/${maxRetries} falló, esperando ${wait}ms`
      );
      await new Promise((r) => setTimeout(r, wait));
    }
  }
  throw new Error("No debería llegar aquí");
}

// --- Ejecutar con timeout ---

async function conTimeout<T>(
  promise: Promise<T>,
  timeoutMs: number,
  toolName: string
): Promise<T> {
  const timeoutPromise = new Promise<never>((_, reject) =>
    setTimeout(() => reject(new ToolTimeoutError(toolName, timeoutMs)), timeoutMs)
  );
  return Promise.race([promise, timeoutPromise]);
}

// --- Construir mensaje de error formativo ---

function construirErrorFormativo(
  toolName: string,
  error: unknown,
  input: Record<string, unknown>
): string {
  if (error instanceof ToolNotFoundError) {
    const disponibles = Object.keys(TOOL_REGISTRY).join(", ");
    return (
      `Herramienta '${toolName}' no existe. ` +
      `Herramientas disponibles: ${disponibles}.`
    );
  }

  if (error instanceof ToolTimeoutError) {
    return (
      `${toolName} no completó en ${error.timeoutMs}ms ` +
      `con input ${JSON.stringify(input)}. ` +
      `Intenta con un scope más pequeño o una fuente diferente.`
    );
  }

  if (error instanceof AuthError) {
    return (
      `Sin permisos para acceder a '${error.resource}'. ` +
      `No reintentes — usa una fuente diferente.`
    );
  }

  if (error instanceof RateLimitError) {
    return `Rate limit excedido. Reintenta en ${error.retryAfterMs}ms.`;
  }

  const msg = error instanceof Error ? error.message : String(error);
  const type = error instanceof Error ? error.constructor.name : "Error";
  return `${type} en ${toolName}: ${msg}`;
}

// --- Dispatcher: ejecutar una tool con manejo completo de errores ---

async function despacharTool(
  toolName: string,
  input: Record<string, unknown>
): Promise<Anthropic.ToolResultBlockParam & { tool_use_id: string }> {
  const fn = TOOL_REGISTRY[toolName];
  let content: string;
  let isError = false;

  if (!fn) {
    content = construirErrorFormativo(toolName, new ToolNotFoundError(toolName), input);
    isError = true;
  } else {
    try {
      // Timeout de 500ms para herramientas "slow"
      const resultado = await conTimeout(
        conBackoff(() => fn(input), 2),
        500,
        toolName
      );
      content = resultado;
    } catch (error) {
      content = construirErrorFormativo(toolName, error, input);
      isError = true;
    }
  }

  return {
    type: "tool_result",
    tool_use_id: "", // será seteado por el caller
    content,
    ...(isError ? { is_error: true } : {}),
  };
}

// --- Agent loop con manejo completo de stop_reason ---

async function agentLoop(tarea: string): Promise<string> {
  const client = new Anthropic();
  let messages: Anthropic.MessageParam[] = [{ role: "user", content: tarea }];

  for (let iter = 0; iter < MAX_ITERATIONS; iter++) {
    const response = await client.messages.create({
      model: MODEL,
      max_tokens: 4096,
      tools: TOOLS,
      messages,
    });

    console.log(`\n[iter=${iter + 1}] stop_reason=${response.stop_reason}`);

    switch (response.stop_reason) {
      case "end_turn":
        return response.content
          .filter((b): b is Anthropic.TextBlock => b.type === "text")
          .map((b) => b.text)
          .join("");

      case "tool_use": {
        const toolBlocks = response.content.filter(
          (b): b is Anthropic.ToolUseBlock => b.type === "tool_use"
        );

        const toolResults: Anthropic.ToolResultBlockParam[] = [];

        for (const block of toolBlocks) {
          console.log(
            `  → ${block.name}(${JSON.stringify(block.input)})`
          );
          const result = await despacharTool(
            block.name,
            block.input as Record<string, unknown>
          );
          result.tool_use_id = block.id;

          const isError = "is_error" in result && result.is_error;
          console.log(
            `  ← [${isError ? "ERROR" : "OK"}] ${String(result.content).slice(0, 100)}`
          );

          toolResults.push(result);
        }

        // CRÍTICO: todos los tool_results en un único mensaje user
        messages.push({ role: "assistant", content: response.content });
        messages.push({ role: "user", content: toolResults });
        break;
      }

      case "max_tokens": {
        // Verificar si el último bloque es un tool_use truncado
        const lastBlock = response.content[response.content.length - 1];
        if (lastBlock?.type === "tool_use") {
          console.log(
            "  [warn] tool_use block truncado por max_tokens — necesita más tokens"
          );
        }
        return "[respuesta truncada — max_tokens alcanzado]";
      }

      case "pause_turn":
        // El servidor excedió su límite de iteraciones internas.
        // Continuar sin añadir ningún mensaje nuevo.
        console.log("  [pause_turn] continuando...");
        messages.push({ role: "assistant", content: response.content });
        break;

      default:
        console.log(`  [warn] stop_reason desconocido: ${response.stop_reason}`);
        return "[stop_reason inesperado]";
    }
  }

  return "[max iteraciones alcanzadas]";
}

async function main() {
  console.log("=== Ejecución y manejo de errores en tool calling ===\n");

  const tarea =
    "Necesito: 1) obtener datos de 'database', " +
    "2) intentar obtener datos de 'restricted' (esto fallará), " +
    "3) calcular 15 * 8 + 3, " +
    "4) guardar el resultado en /tmp/resultado.txt. " +
    "Si algo falla, descríbelo en tu respuesta final.";

  console.log(`Tarea: ${tarea}`);

  const resultado = await agentLoop(tarea);
  console.log(`\n=== Respuesta final ===\n${resultado}`);
}

main().catch(console.error);
