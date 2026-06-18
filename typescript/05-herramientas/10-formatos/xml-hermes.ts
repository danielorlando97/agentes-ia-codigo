// Formato XML estilo Hermes (NousResearch) para tool calling.
//
// Los tags <tool_call> / </tool_call> son tokens únicos en el vocabulario
// del modelo Hermes — el parser detecta límites O(1) por token, no O(n).
// El output NO es XML real: se parsea con regex, no con un parser XML.
//
// Aquí se instruye a Claude a responder en formato Hermes para demostrar
// el parser. En producción se usaría Hermes 2 Pro o Hermes 3 (Llama 3.1).

// Cómo ejecutar: make ts SCRIPT=typescript/05-herramientas/10-formatos/xml-hermes.ts

import Anthropic from "@anthropic-ai/sdk";

const client = new Anthropic();
const MODEL = process.env["MODEL"] ?? "claude-haiku-4-5-20251001";

// --- Definición de tools en formato Hermes (JSON dentro de <tools>) ---

const TOOLS_HERMES = [
  {
    type: "function",
    function: {
      name: "get_weather",
      description:
        "Get current weather for a city. " +
        "Use when the user asks about weather conditions or temperature.",
      parameters: {
        type: "object",
        properties: {
          location: { type: "string", description: "City and country, e.g. 'Madrid, Spain'" },
          unit: { type: "string", enum: ["celsius", "fahrenheit"], description: "Temperature unit. Default: celsius." },
        },
        required: ["location"],
      },
    },
  },
  {
    type: "function",
    function: {
      name: "get_time",
      description: "Get current local time for a given timezone.",
      parameters: {
        type: "object",
        properties: {
          timezone: { type: "string", description: "IANA timezone string, e.g. 'Europe/Madrid'" },
        },
        required: ["timezone"],
      },
    },
  },
];

const SYSTEM_HERMES = `Eres un asistente con acceso a herramientas.

<tools>
${JSON.stringify(TOOLS_HERMES, null, 2)}
</tools>

Cuando necesites usar una herramienta, responde con este formato exacto:

<tool_call>
{"name": "<nombre_herramienta>", "arguments": {<argumentos en JSON>}}
</tool_call>

Puedes emitir múltiples <tool_call> para llamadas paralelas. El output dentro del tag
no es XML real — el sistema lo parsea con regex, no con un parser XML.
Después de recibir los resultados en <tool_response>, responde al usuario.`;

// --- Parser de <tool_call> por regex ---

interface ToolCall {
  name: string;
  arguments: Record<string, unknown>;
}

function extraerToolCalls(respuesta: string): ToolCall[] {
  const patron = /<tool_call>\s*(\{.*?\})\s*<\/tool_call>/gs;
  const resultado: ToolCall[] = [];
  for (const match of respuesta.matchAll(patron)) {
    try {
      const datos = JSON.parse(match[1]);
      resultado.push({
        name: datos.name,
        arguments: datos.arguments ?? datos.parameters ?? {},
      });
    } catch {
      // JSON malformado — en producción usar json-repair
    }
  }
  return resultado;
}

// --- Mock de ejecución ---

function ejecutarHerramienta(nombre: string, args: Record<string, unknown>): Record<string, unknown> {
  if (nombre === "get_weather") {
    return { location: args.location, temperature: 22, unit: args.unit ?? "celsius", conditions: "parcialmente nublado" };
  }
  if (nombre === "get_time") {
    return { timezone: args.timezone, local_time: "14:35:00" };
  }
  return { error: `herramienta desconocida: ${nombre}` };
}

function formatearToolResponse(nombre: string, resultado: Record<string, unknown>): string {
  return `<tool_response>\n${JSON.stringify({ name: nombre, content: resultado })}\n</tool_response>`;
}

// --- Loop de tool use con formato Hermes ---

async function hermesLoop(pregunta: string): Promise<string> {
  const historial: Anthropic.MessageParam[] = [{ role: "user", content: pregunta }];

  for (let paso = 0; paso < 10; paso++) {
    const resp = await client.messages.create({
      model: MODEL,
      max_tokens: 1024,
      system: SYSTEM_HERMES,
      messages: historial,
    });

    const texto = (resp.content[0] as Anthropic.TextBlock).text.trim();
    historial.push({ role: "assistant", content: texto });

    const toolCalls = extraerToolCalls(texto);
    if (toolCalls.length === 0) return texto;

    // Ejecutar todas las tool calls (pueden ser paralelas en Hermes)
    const responsesXml: string[] = [];
    for (const tc of toolCalls) {
      const resultado = ejecutarHerramienta(tc.name, tc.arguments);
      console.log(`  → ${tc.name}(${JSON.stringify(tc.arguments)}) = ${JSON.stringify(resultado).slice(0, 60)}`);
      responsesXml.push(formatearToolResponse(tc.name, resultado));
    }

    historial.push({ role: "user", content: responsesXml.join("\n") });
  }
  return "[límite de pasos alcanzado]";
}

async function main(): Promise<void> {
  console.log("=== Formato XML Hermes (NousResearch style) ===");
  console.log("Parser de <tool_call> por regex — no es XML real\n");

  // Demo del parser con respuesta simulada
  const respuestaSimulada = `Voy a consultar el tiempo y la hora simultáneamente.
<tool_call>
{"name": "get_weather", "arguments": {"location": "Madrid, Spain", "unit": "celsius"}}
</tool_call><tool_call>
{"name": "get_time", "arguments": {"timezone": "Europe/Madrid"}}
</tool_call>`;

  console.log("Respuesta simulada del modelo:");
  console.log(respuestaSimulada);
  const calls = extraerToolCalls(respuestaSimulada);
  console.log(`\nTool calls extraídas por regex: ${JSON.stringify(calls)}\n`);

  // Loop completo con el modelo
  console.log("=".repeat(60));
  console.log("Pregunta: ¿Qué tiempo hace en Tokio?");
  const respuesta = await hermesLoop("¿Qué tiempo hace en Tokio?");
  const respuestaLimpia = respuesta.replace(/<tool_call>.*?<\/tool_call>/gs, "").trim();
  console.log(`Respuesta final: ${respuestaLimpia}`);
}

main().catch(console.error);
