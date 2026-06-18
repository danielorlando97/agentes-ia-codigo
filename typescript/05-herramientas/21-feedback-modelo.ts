// Devolver el resultado al modelo — formatos de tool_result.
//
// Muestra el formato correcto de tool_result en cinco escenarios:
//   1. Texto simple
//   2. JSON estructurado
//   3. Imagen (content array con type: "image")
//   4. Error formativo (is_error: true)
//   5. Loop completo: request → tool_use → execute → tool_result → segunda response
//
// El contenido del campo 'content' cuando is_error=true determina
// si el modelo puede autocorregir — un error genérico produce retry
// idéntico; un error formativo produce recovery inteligente.

// Cómo ejecutar: make ts SCRIPT=typescript/05-herramientas/21-feedback-modelo.ts

import Anthropic from "@anthropic-ai/sdk";

const MODEL = process.env["MODEL"] ?? "claude-sonnet-4-6";

const client = new Anthropic();

// --- 1. Tool result con texto simple ---

function toolResultTexto(toolUseId: string): Anthropic.ToolResultBlockParam {
  return {
    type: "tool_result",
    tool_use_id: toolUseId,
    content: "La temperatura en Madrid es 24°C, condición: soleado.",
  };
}

// --- 2. Tool result con JSON estructurado ---

function toolResultJSON(toolUseId: string): Anthropic.ToolResultBlockParam {
  const datos = {
    city: "Madrid",
    temperature: { value: 24, unit: "celsius" },
    condition: "sunny",
    humidity: 45,
    wind: { speed: 12, direction: "NW" },
    forecast: [
      { day: "mañana", high: 26, low: 18 },
      { day: "pasado", high: 23, low: 16 },
    ],
  };

  return {
    type: "tool_result",
    tool_use_id: toolUseId,
    content: JSON.stringify(datos),
  };
}

// --- 3. Tool result con imagen ---

function toolResultImagen(toolUseId: string): Anthropic.ToolResultBlockParam {
  // Imagen PNG 1x1 roja (base64) — en producción sería el PNG real del gráfico
  const pngBase64 =
    "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwADhQGAWjR9awAAAABJRU5ErkJggg==";

  return {
    type: "tool_result",
    tool_use_id: toolUseId,
    content: [
      {
        type: "text",
        text: "Gráfico de temperaturas de Madrid — últimas 24 horas:",
      },
      {
        type: "image",
        source: {
          type: "base64",
          media_type: "image/png",
          data: pngBase64,
        },
      },
    ],
  };
}

// --- 4. Tool result con error formativo ---

function toolResultErrorFormativo(
  toolUseId: string,
  tipo: "not_found" | "timeout" | "permission" | "generic"
): Anthropic.ToolResultBlockParam {
  const mensajes: Record<typeof tipo, string> = {
    not_found:
      "Archivo no encontrado: /tmp/report.md\n" +
      "Archivos disponibles en /tmp/: budget.md, analysis.md, notes.txt\n" +
      "Sugerencia: usa read_file con el path de uno de los archivos disponibles.",

    timeout:
      "Timeout tras 10s buscando 'todos los documentos de 2024'.\n" +
      "Intenta filtrar por rango de fecha más pequeño, e.g. '2024-Q1' o 'enero 2024'.",

    permission:
      "Sin permisos para acceder a /etc/passwords.\n" +
      "No reintentes — usa un directorio dentro de /home/usuario/.",

    generic:
      "RateLimitError: demasiadas requests a la API externa.\n" +
      "Reintenta después de 60 segundos.",
  };

  return {
    type: "tool_result",
    tool_use_id: toolUseId,
    content: mensajes[tipo],
    is_error: true,
  };
}

// --- Mostrar los formatos ---

async function mostrarFormatos(): Promise<void> {
  console.log("=== Formatos de tool_result ===\n");

  const fakeId = "toolu_fake_id_001";

  console.log("1. Texto simple:");
  console.log(JSON.stringify(toolResultTexto(fakeId), null, 2));

  console.log("\n2. JSON estructurado:");
  const jsonResult = toolResultJSON(fakeId);
  console.log(JSON.stringify(jsonResult, null, 2));

  console.log("\n3. Imagen (content array):");
  const imgResult = toolResultImagen(fakeId);
  const imgResultDisplay = {
    ...imgResult,
    content: Array.isArray(imgResult.content)
      ? imgResult.content.map((c) =>
          "source" in c
            ? { ...c, source: { ...c.source, data: "[base64 truncado]" } }
            : c
        )
      : imgResult.content,
  };
  console.log(JSON.stringify(imgResultDisplay, null, 2));

  console.log("\n4. Errores formativos:");
  for (const tipo of ["not_found", "timeout", "permission"] as const) {
    console.log(`\n  [${tipo}]`);
    const errResult = toolResultErrorFormativo(fakeId, tipo);
    console.log(`  is_error: ${errResult.is_error}`);
    console.log(`  content: ${errResult.content}`);
  }
}

// --- 5. Loop completo: el modelo solicita una tool, recibe error formativo, autocorrige ---

async function loopCompleto(): Promise<void> {
  console.log("\n\n=== Loop completo con autocorrección por error formativo ===\n");

  const TOOLS: Anthropic.Tool[] = [
    {
      name: "read_file",
      description: "Lee el contenido de un archivo.",
      input_schema: {
        type: "object",
        properties: {
          path: { type: "string", description: "Path absoluto del archivo" },
        },
        required: ["path"],
      },
    },
    {
      name: "list_files",
      description: "Lista los archivos en un directorio.",
      input_schema: {
        type: "object",
        properties: {
          directory: { type: "string" },
        },
        required: ["directory"],
      },
    },
  ];

  // Mock: read_file falla en primer intento con error formativo
  let readFileIntento = 0;

  function ejecutarTool(
    toolUseId: string,
    name: string,
    input: Record<string, string>
  ): Anthropic.ToolResultBlockParam {
    if (name === "read_file") {
      readFileIntento++;
      if (readFileIntento === 1 && input.path === "/tmp/report.md") {
        // Primera llamada falla con error formativo
        return toolResultErrorFormativo(toolUseId, "not_found");
      }
      // Llamadas posteriores (path correcto) tienen éxito
      return {
        type: "tool_result",
        tool_use_id: toolUseId,
        content: `Contenido de ${input.path}:\n# Presupuesto 2024\nTotal: $1,234,567`,
      };
    }

    if (name === "list_files") {
      return {
        type: "tool_result",
        tool_use_id: toolUseId,
        content: JSON.stringify({
          directory: input.directory,
          files: ["budget.md", "analysis.md", "notes.txt"],
        }),
      };
    }

    return {
      type: "tool_result",
      tool_use_id: toolUseId,
      content: `Herramienta '${name}' no existe`,
      is_error: true,
    };
  }

  let messages: Anthropic.MessageParam[] = [
    {
      role: "user",
      content: "Lee el archivo /tmp/report.md y dime el presupuesto total.",
    },
  ];

  for (let iter = 0; iter < 5; iter++) {
    const response = await client.messages.create({
      model: MODEL,
      max_tokens: 1024,
      tools: TOOLS,
      messages,
    });

    console.log(
      `[iter=${iter + 1}] stop_reason=${response.stop_reason}`
    );

    if (response.stop_reason === "end_turn") {
      const texto = response.content
        .filter((b): b is Anthropic.TextBlock => b.type === "text")
        .map((b) => b.text)
        .join("");
      console.log(`\nRespuesta final:\n${texto}`);
      break;
    }

    if (response.stop_reason === "tool_use") {
      const toolBlocks = response.content.filter(
        (b): b is Anthropic.ToolUseBlock => b.type === "tool_use"
      );

      const toolResults: Anthropic.ToolResultBlockParam[] = [];
      for (const block of toolBlocks) {
        console.log(`  → ${block.name}(${JSON.stringify(block.input)})`);
        const result = ejecutarTool(
          block.id,
          block.name,
          block.input as Record<string, string>
        );
        const isError = result.is_error;
        console.log(
          `  ← [${isError ? "ERROR" : "OK"}] ${String(result.content).slice(0, 100)}`
        );
        toolResults.push(result);
      }

      messages.push({ role: "assistant", content: response.content });
      messages.push({ role: "user", content: toolResults });
    }
  }
}

async function main() {
  await mostrarFormatos();
  await loopCompleto();
}

main().catch(console.error);
