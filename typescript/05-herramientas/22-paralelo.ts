// Tool calling paralelo.
//
// El modelo puede generar múltiples bloques tool_use en un único turno.
// El ejecutor los corre concurrentemente con Promise.all y devuelve
// todos los tool_results en un único mensaje user.
//
// Regla crítica: todos los tool_results deben ir en un único mensaje user.
// Si se envían en mensajes separados, el modelo aprende a serializar
// tool calls en turnos futuros porque así "ve" que trabaja el sistema.

// Cómo ejecutar: make ts SCRIPT=typescript/05-herramientas/22-paralelo.ts

import Anthropic from "@anthropic-ai/sdk";

const MODEL = process.env["MODEL"] ?? "claude-sonnet-4-6";

// --- Herramientas mock ---

interface WeatherResult {
  city: string;
  temp_c: number;
  condition: string;
}

interface CalcResult {
  expression: string;
  result: number;
}

interface SearchResult {
  query: string;
  hits: string[];
}

type ToolResult = WeatherResult | CalcResult | SearchResult | string;

async function mockWeather(city: string): Promise<WeatherResult> {
  // Simula una llamada a API externa (~300ms)
  await new Promise((r) => setTimeout(r, 300));
  const table: Record<string, { temp: number; cond: string }> = {
    Madrid: { temp: 24, cond: "sunny" },
    Paris: { temp: 18, cond: "cloudy" },
    Tokyo: { temp: 29, cond: "humid" },
  };
  const data = table[city] ?? { temp: 20, cond: "unknown" };
  return { city, temp_c: data.temp, condition: data.cond };
}

async function mockCalculate(expression: string): Promise<CalcResult> {
  // Simula evaluación (~50ms)
  await new Promise((r) => setTimeout(r, 50));
  try {
    // Evaluar solo expresiones numéricas básicas de forma segura
    const sanitized = expression.replace(/[^0-9+\-*/().\s]/g, "");
    const result = Function(`"use strict"; return (${sanitized})`)() as number;
    return { expression, result };
  } catch {
    throw new Error(`Expresión inválida: ${expression}`);
  }
}

async function mockSearch(query: string): Promise<SearchResult> {
  // Simula búsqueda (~400ms)
  await new Promise((r) => setTimeout(r, 400));
  return {
    query,
    hits: [
      `Resultado 1 para "${query}"`,
      `Resultado 2 para "${query}"`,
      `Resultado 3 para "${query}"`,
    ],
  };
}

// --- Definición de herramientas para el modelo ---

const TOOLS: Anthropic.Tool[] = [
  {
    name: "get_weather",
    description: "Obtiene el clima actual de una ciudad.",
    input_schema: {
      type: "object",
      properties: {
        city: { type: "string", description: "Nombre de la ciudad" },
      },
      required: ["city"],
    },
  },
  {
    name: "calculate",
    description: "Evalúa una expresión matemática y devuelve el resultado.",
    input_schema: {
      type: "object",
      properties: {
        expression: {
          type: "string",
          description: "Expresión matemática, e.g. '15 * 8 + 3'",
        },
      },
      required: ["expression"],
    },
  },
  {
    name: "search",
    description: "Busca información sobre un tema.",
    input_schema: {
      type: "object",
      properties: {
        query: { type: "string", description: "Término de búsqueda" },
      },
      required: ["query"],
    },
  },
];

// --- Ejecutor paralelo ---

async function ejecutarTool(
  name: string,
  input: Record<string, string>
): Promise<ToolResult> {
  switch (name) {
    case "get_weather":
      return mockWeather(input.city);
    case "calculate":
      return mockCalculate(input.expression);
    case "search":
      return mockSearch(input.query);
    default:
      throw new Error(`Herramienta desconocida: ${name}`);
  }
}

/**
 * Ejecuta todos los bloques tool_use concurrentemente con Promise.all.
 * Devuelve todos los resultados listos para incluir en un único mensaje user.
 */
async function ejecutarToolsParalelas(
  bloques: Anthropic.ToolUseBlock[]
): Promise<Anthropic.ToolResultBlockParam[]> {
  const t0 = Date.now();

  const promesas = bloques.map(async (bloque) => {
    try {
      const resultado = await ejecutarTool(
        bloque.name,
        bloque.input as Record<string, string>
      );
      return {
        type: "tool_result" as const,
        tool_use_id: bloque.id,
        content: JSON.stringify(resultado),
      };
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e);
      return {
        type: "tool_result" as const,
        tool_use_id: bloque.id,
        content: `${bloque.name}: ${msg}`,
        is_error: true,
      };
    }
  });

  // CLAVE: Promise.all ejecuta todas en paralelo
  const resultados = await Promise.all(promesas);
  const elapsed = Date.now() - t0;
  console.log(
    `  [paralelo] ${bloques.length} tools → ${elapsed}ms (max individual, no suma)`
  );
  return resultados;
}

// --- Loop del agente ---

async function agentLoop(tarea: string): Promise<string> {
  const client = new Anthropic();
  let messages: Anthropic.MessageParam[] = [{ role: "user", content: tarea }];

  for (let iter = 0; iter < 10; iter++) {
    const response = await client.messages.create({
      model: MODEL,
      max_tokens: 4096,
      tools: TOOLS,
      messages,
    });

    console.log(
      `  [iter=${iter + 1}] stop_reason=${response.stop_reason}, ` +
        `tool_calls=${response.content.filter((b) => b.type === "tool_use").length}`
    );

    if (response.stop_reason === "end_turn") {
      return response.content
        .filter((b): b is Anthropic.TextBlock => b.type === "text")
        .map((b) => b.text)
        .join("");
    }

    if (response.stop_reason === "tool_use") {
      const bloques = response.content.filter(
        (b): b is Anthropic.ToolUseBlock => b.type === "tool_use"
      );

      // Ejecutar todos en paralelo
      const toolResults = await ejecutarToolsParalelas(bloques);

      // CORRECTO: todos los tool_results en UN solo mensaje user
      messages.push({ role: "assistant", content: response.content });
      messages.push({ role: "user", content: toolResults });
      continue;
    }

    break;
  }

  return "[max iteraciones]";
}

async function main() {
  console.log("=== Tool calling paralelo ===\n");

  // Esta tarea debería generar múltiples tool_use blocks en un turno:
  // clima de 2 ciudades + un cálculo + una búsqueda — todos independientes
  const tarea =
    "Necesito: 1) el clima actual de Madrid y Paris, " +
    "2) cuánto es 1234 * 56 + 789, y " +
    "3) busca información sobre 'parallel tool calling LLM'. " +
    "Puedes hacer todas estas búsquedas a la vez.";

  console.log(`Tarea: ${tarea}\n`);

  const respuesta = await agentLoop(tarea);
  console.log(`\nRespuesta del modelo:\n${respuesta}`);
}

main().catch(console.error);
