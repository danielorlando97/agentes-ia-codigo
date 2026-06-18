// Function calling sin soporte nativo — constrained via system prompt + retry.
//
// Cuando el modelo no tiene fine-tuning para tool calling, se describe
// el formato JSON esperado en el system prompt y se valida la respuesta.
// Si el JSON es inválido, se reintenta con el error acumulado en el prompt
// (máx 3 intentos).
//
// Tasa de fallo sin fine-tuning: 15-40%. Con retry x3 y 80% accuracy/intento,
// la probabilidad de fallo total ≈ 0.8%.

// Cómo ejecutar: make ts SCRIPT=typescript/05-herramientas/10-formatos/sin-soporte-nativo.ts

import Anthropic from "@anthropic-ai/sdk";

const MODEL = process.env["MODEL"] ?? "claude-sonnet-4-6";
const MAX_RETRIES = 3;

// --- Schema de tool calls ---

interface ToolCallSchema {
  tool: string;
  arguments: Record<string, unknown>;
}

const TOOLS_DESCRIPTION = `
Tienes acceso a las siguientes herramientas:

- search_database(query: string, limit?: number)
  Busca en la base de datos. limit debe ser entre 1 y 100.

- calculate(expression: string)
  Evalúa una expresión matemática. Solo operadores +, -, *, /.

Para usar una herramienta, responde ÚNICAMENTE con JSON válido en este formato:
{
  "tool": "nombre_herramienta",
  "arguments": {
    "param1": "valor1",
    "param2": valor2
  }
}

Si no necesitas una herramienta, responde con texto normal.
NO incluyas texto adicional antes o después del JSON cuando uses una herramienta.
`.trim();

// --- Validación del JSON ---

interface ValidationError {
  message: string;
  hint?: string;
}

function validarToolCall(texto: string): { ok: true; data: ToolCallSchema } | { ok: false; error: ValidationError } {
  // Intentar extraer JSON del texto (puede estar rodeado de markdown)
  const jsonMatch = texto.match(/```(?:json)?\s*([\s\S]*?)\s*```/) ||
    texto.match(/(\{[\s\S]*\})/);

  const jsonStr = jsonMatch ? jsonMatch[1].trim() : texto.trim();

  let parsed: unknown;
  try {
    parsed = JSON.parse(jsonStr);
  } catch (e) {
    return {
      ok: false,
      error: {
        message: `JSON inválido: ${e instanceof Error ? e.message : String(e)}`,
        hint: "Asegúrate de que la respuesta sea JSON puro sin texto adicional.",
      },
    };
  }

  if (typeof parsed !== "object" || parsed === null) {
    return {
      ok: false,
      error: { message: "La respuesta debe ser un objeto JSON, no un valor primitivo." },
    };
  }

  const obj = parsed as Record<string, unknown>;

  // Validar campo 'tool'
  if (!("tool" in obj)) {
    return {
      ok: false,
      error: {
        message: 'Campo requerido faltante: "tool"',
        hint: 'El JSON debe tener un campo "tool" con el nombre de la herramienta.',
      },
    };
  }

  if (typeof obj.tool !== "string") {
    return {
      ok: false,
      error: {
        message: `Campo "tool" debe ser string, recibido: ${typeof obj.tool}`,
      },
    };
  }

  const toolsValidas = ["search_database", "calculate"];
  if (!toolsValidas.includes(obj.tool)) {
    return {
      ok: false,
      error: {
        message: `Herramienta desconocida: "${obj.tool}"`,
        hint: `Herramientas disponibles: ${toolsValidas.join(", ")}`,
      },
    };
  }

  // Validar campo 'arguments'
  if (!("arguments" in obj)) {
    return {
      ok: false,
      error: {
        message: 'Campo requerido faltante: "arguments"',
        hint: 'El JSON debe tener un campo "arguments" con los parámetros de la herramienta.',
      },
    };
  }

  if (typeof obj.arguments !== "object" || obj.arguments === null) {
    return {
      ok: false,
      error: { message: '"arguments" debe ser un objeto.' },
    };
  }

  // Validaciones específicas por herramienta
  const args = obj.arguments as Record<string, unknown>;

  if (obj.tool === "search_database") {
    if (!("query" in args) || typeof args.query !== "string") {
      return {
        ok: false,
        error: {
          message: 'search_database requiere "query" (string)',
          hint: 'Ejemplo: {"tool": "search_database", "arguments": {"query": "usuarios activos", "limit": 10}}',
        },
      };
    }
    if ("limit" in args) {
      const limit = Number(args.limit);
      if (!Number.isInteger(limit) || limit < 1 || limit > 100) {
        return {
          ok: false,
          error: { message: '"limit" debe ser un entero entre 1 y 100.' },
        };
      }
    }
  }

  if (obj.tool === "calculate") {
    if (!("expression" in args) || typeof args.expression !== "string") {
      return {
        ok: false,
        error: {
          message: 'calculate requiere "expression" (string)',
          hint: 'Ejemplo: {"tool": "calculate", "arguments": {"expression": "15 * 8 + 3"}}',
        },
      };
    }
  }

  return { ok: true, data: obj as unknown as ToolCallSchema };
}

// --- Herramientas mock ---

function ejecutarTool(call: ToolCallSchema): string {
  if (call.tool === "search_database") {
    const { query, limit = 10 } = call.arguments as { query: string; limit?: number };
    return JSON.stringify({
      results: [
        { id: 1, texto: `Resultado 1 para "${query}"` },
        { id: 2, texto: `Resultado 2 para "${query}"` },
      ],
      total: 2,
      limit,
    });
  }

  if (call.tool === "calculate") {
    const { expression } = call.arguments as { expression: string };
    try {
      const sanitized = expression.replace(/[^0-9+\-*/().\s]/g, "");
      const result = Function(`"use strict"; return (${sanitized})`)() as number;
      return String(result);
    } catch {
      return `Error al evaluar: ${expression}`;
    }
  }

  return "Herramienta no encontrada";
}

// --- Loop con retry ---

async function llamarConRetry(
  pregunta: string
): Promise<{ toolCall: ToolCallSchema; intentos: number } | { respuesta: string; intentos: number }> {
  const client = new Anthropic();
  const mensajesError: string[] = [];

  for (let intento = 1; intento <= MAX_RETRIES; intento++) {
    // Construir el prompt acumulando errores previos
    let userContent = pregunta;
    if (mensajesError.length > 0) {
      userContent +=
        "\n\n[ERRORES PREVIOS — corrige estos problemas en tu respuesta:]\n" +
        mensajesError
          .map((e, i) => `Intento ${i + 1}: ${e}`)
          .join("\n");
    }

    const response = await client.messages.create({
      model: MODEL,
      max_tokens: 512,
      system: TOOLS_DESCRIPTION,
      messages: [{ role: "user", content: userContent }],
    });

    const texto = response.content
      .filter((b): b is Anthropic.TextBlock => b.type === "text")
      .map((b) => b.text)
      .join("");

    console.log(`  [intento ${intento}] respuesta: ${texto.slice(0, 120)}...`);

    // Intentar validar como tool call
    const validacion = validarToolCall(texto);
    if (validacion.ok) {
      return { toolCall: validacion.data, intentos: intento };
    }

    // Acumular el error para el siguiente intento
    const errorMsg = validacion.error.hint
      ? `${validacion.error.message} — ${validacion.error.hint}`
      : validacion.error.message;

    mensajesError.push(errorMsg);
    console.log(`  [intento ${intento}] error de validación: ${errorMsg}`);

    // Si fue el último intento, puede ser respuesta de texto (no tool call)
    if (intento === MAX_RETRIES) {
      // Si el texto no parece JSON en absoluto, tratar como respuesta normal
      if (!texto.includes("{")) {
        return { respuesta: texto, intentos: intento };
      }
    }
  }

  throw new Error(`No se obtuvo JSON válido tras ${MAX_RETRIES} intentos`);
}

async function main() {
  console.log("=== Function calling sin soporte nativo (system prompt + retry) ===\n");

  const casos = [
    {
      descripcion: "Caso normal: debería generar JSON válido",
      pregunta: "Busca los usuarios que se registraron en el último mes, máximo 20 resultados.",
    },
    {
      descripcion: "Caso aritmético: debería usar calculate",
      pregunta: "¿Cuánto es 1234 * 56 + 789?",
    },
  ];

  const client = new Anthropic();

  for (const caso of casos) {
    console.log(`\n--- ${caso.descripcion} ---`);
    console.log(`Pregunta: ${caso.pregunta}`);

    const resultado = await llamarConRetry(caso.pregunta);

    if ("toolCall" in resultado) {
      console.log(`\nTool call validada (${resultado.intentos} intento/s):`);
      console.log(JSON.stringify(resultado.toolCall, null, 2));

      // Ejecutar la herramienta
      const toolResult = ejecutarTool(resultado.toolCall);
      console.log(`\nResultado de la herramienta: ${toolResult}`);

      // Devolver el resultado al modelo para respuesta final
      const response = await client.messages.create({
        model: MODEL,
        max_tokens: 512,
        system: TOOLS_DESCRIPTION,
        messages: [
          { role: "user", content: caso.pregunta },
          { role: "assistant", content: JSON.stringify(resultado.toolCall) },
          { role: "user", content: `Resultado de la herramienta: ${toolResult}` },
        ],
      });

      const respuestaFinal = response.content
        .filter((b): b is Anthropic.TextBlock => b.type === "text")
        .map((b) => b.text)
        .join("");

      console.log(`\nRespuesta final: ${respuestaFinal}`);
    } else {
      console.log(`\nRespuesta directa (sin tool call): ${resultado.respuesta}`);
    }
  }
}

main().catch(console.error);
