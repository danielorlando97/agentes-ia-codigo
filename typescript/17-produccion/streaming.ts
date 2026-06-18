// Streaming SSE: muestra tokens del agente en tiempo real con eventos de tool calls

// Cómo ejecutar: make ts SCRIPT=typescript/17-produccion/streaming.ts

import Anthropic from "@anthropic-ai/sdk";

const cliente = new Anthropic();

const HERRAMIENTAS: Anthropic.Tool[] = [
  {
    name: "buscar_docs",
    description:
      "Busca en la documentación. Úsala cuando el usuario pregunte por APIs o funciones.",
    input_schema: {
      type: "object",
      properties: { query: { type: "string" } },
      required: ["query"],
    },
  },
];

function ejecutarHerramienta(nombre: string, params: Record<string, string>): string {
  if (nombre === "buscar_docs") {
    return `Documentación para '${params.query}': función disponible desde v2.0, acepta str y devuelve dict.`;
  }
  return `Error: herramienta '${nombre}' no encontrada.`;
}

async function streamAgenteSimple(pregunta: string): Promise<void> {
  const stream = cliente.messages.stream({
    model: process.env["MODEL"] ?? "claude-sonnet-4-6",
    max_tokens: 1024,
    tools: HERRAMIENTAS,
    messages: [{ role: "user", content: pregunta }],
  });

  stream.on("text", (text) => {
    process.stdout.write(text);
  });

  const mensaje = await stream.finalMessage();

  for (const bloque of mensaje.content) {
    if (bloque.type === "tool_use") {
      console.log(`\n[tool: ${bloque.name}(${JSON.stringify(bloque.input)})]`);
      const resultado = ejecutarHerramienta(bloque.name, bloque.input as Record<string, string>);
      console.log(`[resultado: ${resultado.slice(0, 100)}]`);
    }
  }
  console.log();
}

interface SseEvent {
  type: "text" | "tool_start" | "tool_done" | "done" | "error";
  content?: string;
  tool?: string;
}

async function streamLoopReact(pregunta: string, queue: SseEvent[]): Promise<void> {
  const mensajes: Anthropic.MessageParam[] = [{ role: "user", content: pregunta }];
  const MAX_PASOS = 10;

  for (let paso = 0; paso < MAX_PASOS; paso++) {
    const stream = cliente.messages.stream({
      model: process.env["MODEL"] ?? "claude-sonnet-4-6",
      max_tokens: 1024,
      tools: HERRAMIENTAS,
      messages: mensajes,
    });

    stream.on("text", (text) => {
      queue.push({ type: "text", content: text });
    });

    const respuesta = await stream.finalMessage();
    mensajes.push({ role: "assistant", content: respuesta.content });

    if (respuesta.stop_reason === "end_turn") {
      queue.push({ type: "done" });
      return;
    }

    const toolResults: Anthropic.ToolResultBlockParam[] = [];
    for (const bloque of respuesta.content) {
      if (bloque.type === "tool_use") {
        queue.push({ type: "tool_start", tool: bloque.name });
        const resultado = ejecutarHerramienta(bloque.name, bloque.input as Record<string, string>);
        queue.push({ type: "tool_done", tool: bloque.name });
        toolResults.push({
          type: "tool_result",
          tool_use_id: bloque.id,
          content: resultado,
        });
      }
    }

    if (toolResults.length > 0) {
      mensajes.push({ role: "user", content: toolResults });
    }
  }

  queue.push({ type: "error", content: "Límite de pasos alcanzado" });
}

function consumirStream(queue: SseEvent[]): void {
  let idx = 0;
  const interval = setInterval(() => {
    while (idx < queue.length) {
      const evento = queue[idx++];
      if (evento.type === "text") {
        process.stdout.write(evento.content ?? "");
      } else if (evento.type === "tool_start") {
        console.log(`\n[iniciando ${evento.tool}...]`);
      } else if (evento.type === "tool_done") {
        console.log(`[${evento.tool} completado]`);
      } else if (evento.type === "done" || evento.type === "error") {
        if (evento.type === "error") {
          console.log(`\n[error: ${evento.content}]`);
        }
        clearInterval(interval);
      }
    }
  }, 10);
}

async function main(): Promise<void> {
  console.log("=== Stream simple ===");
  await streamAgenteSimple("¿Qué hace la función filter_context?");

  console.log("\n=== Loop ReAct con streaming ===");
  const queue: SseEvent[] = [];

  const productor = streamLoopReact("Busca cómo funciona filter_context y explícamelo.", queue);
  consumirStream(queue);
  await productor;
  console.log();
}

main().catch(console.error);
