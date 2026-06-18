// Tool calling con JSON nativo (Anthropic).
//
// El formato Anthropic serializa la llamada como un bloque tool_use con
// `input` como objeto ya parseado (no string JSON). El resultado vuelve
// como tool_result en un mensaje de role "user".
//
// Diferencias clave vs OpenAI Chat Completions:
//   Anthropic: stop_reason="tool_use", input=objeto, role="user", is_error
//   OpenAI:    finish_reason="tool_calls", arguments=string, role="tool", sin is_error

// Cómo ejecutar: make ts SCRIPT=typescript/05-herramientas/10-formatos/json-nativo.ts

import Anthropic from "@anthropic-ai/sdk";

const client = new Anthropic();
const MODEL = process.env["MODEL"] ?? "claude-haiku-4-5-20251001";

// --- Definición de tools ---

const TOOLS: Anthropic.Tool[] = [
  {
    name: "get_weather",
    description:
      "Get current weather for a city. " +
      "Use when the user asks about weather conditions, temperature, or forecast. " +
      "Do NOT use for historical weather — use get_weather_history instead.",
    input_schema: {
      type: "object",
      properties: {
        location: {
          type: "string",
          description: "City and country, e.g. 'Madrid, Spain'",
        },
        unit: {
          type: "string",
          enum: ["celsius", "fahrenheit"],
          description: "Temperature unit. Default: celsius.",
        },
      },
      required: ["location"],
    },
  },
  {
    name: "get_time",
    description: "Get current local time for a timezone or city.",
    input_schema: {
      type: "object",
      properties: {
        timezone: {
          type: "string",
          description: "IANA timezone string, e.g. 'Europe/Madrid'",
        },
      },
      required: ["timezone"],
    },
  },
];

// --- Mock de ejecución ---

function ejecutarHerramienta(nombre: string, args: Record<string, unknown>): string {
  if (nombre === "get_weather") {
    return JSON.stringify({
      location: args.location,
      temperature: 22,
      unit: args.unit ?? "celsius",
      conditions: "parcialmente nublado",
    });
  }
  if (nombre === "get_time") {
    return JSON.stringify({ timezone: args.timezone, local_time: "14:35:00" });
  }
  return JSON.stringify({ error: `herramienta desconocida: ${nombre}` });
}

// --- Loop de tool use ---

async function toolUseLoop(pregunta: string): Promise<string> {
  const mensajes: Anthropic.MessageParam[] = [{ role: "user", content: pregunta }];

  for (let paso = 0; paso < 10; paso++) {
    const resp = await client.messages.create({
      model: MODEL,
      max_tokens: 1024,
      tools: TOOLS,
      messages: mensajes,
    });

    if (resp.stop_reason === "end_turn") {
      return resp.content
        .filter((b): b is Anthropic.TextBlock => b.type === "text")
        .map(b => b.text)
        .join("");
    }

    if (resp.stop_reason === "tool_use") {
      // Añadir respuesta del asistente (texto + tool_use blocks)
      mensajes.push({ role: "assistant", content: resp.content });

      // Ejecutar todas las tool calls del turno (pueden ser paralelas)
      const resultados: Anthropic.ToolResultBlockParam[] = [];
      for (const bloque of resp.content) {
        if (bloque.type === "tool_use") {
          // input es un objeto ya parseado, no un string
          const resultado = ejecutarHerramienta(bloque.name, bloque.input as Record<string, unknown>);
          console.log(`  → ${bloque.name}(${JSON.stringify(bloque.input)}) = ${resultado.slice(0, 60)}`);
          resultados.push({
            type: "tool_result",
            tool_use_id: bloque.id,   // mismo ID del tool_use block
            content: resultado,
            is_error: false,           // campo exclusivo de Anthropic
          });
        }
      }

      // Todos los resultados en UN solo mensaje de role "user"
      mensajes.push({ role: "user", content: resultados });
    }
  }
  return "[límite de pasos alcanzado]";
}

async function main(): Promise<void> {
  console.log("=== Tool calling JSON nativo (Anthropic) ===\n");

  // Caso 1: tool call simple
  console.log("Pregunta: ¿Qué tiempo hace en Madrid?");
  const r1 = await toolUseLoop("¿Qué tiempo hace en Madrid?");
  console.log(`Respuesta: ${r1}\n`);

  // Caso 2: parallel tool calls — el modelo genera múltiples bloques en un turno
  console.log("Pregunta: ¿Qué tiempo y hora es en Tokio?");
  const r2 = await toolUseLoop("¿Qué tiempo y hora es en Tokio ahora mismo?");
  console.log(`Respuesta: ${r2}\n`);
}

main().catch(console.error);
