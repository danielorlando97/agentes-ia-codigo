// Representación de herramientas: descripción, input_schema y errores de selección.
//
// Una herramienta es un contrato textual: nombre, descripción en lenguaje natural,
// y JSON Schema de parámetros. La calidad de ese contrato determina si el modelo
// elige la herramienta correcta (selección) y si genera los argumentos válidos
// (parametrización). IAC (Insufficient API Calls) — el modelo no invoca la tool
// cuando debería — es el error más frecuente, causado por descripciones pobres.

// Cómo ejecutar: make ts SCRIPT=typescript/05-herramientas/representacion.ts

import Anthropic from "@anthropic-ai/sdk";

const MODEL = process.env["MODEL"] ?? "claude-haiku-4-5-20251001";

// --- Definiciones de herramientas ---

// Herramienta con descripción pobre — solo describe el mecanismo.
// Causa IAC: el modelo responde desde memoria en lugar de invocarla.
const TOOL_MALA: Anthropic.Tool = {
  name: "get_account_info",
  description: "Gets account information from the database.",
  input_schema: {
    type: "object",
    properties: {
      id: {
        type: "string",
      },
    },
    required: ["id"],
  },
};

// Herramienta con descripción efectiva — incluye cuándo usarla, qué no hace,
// y qué campos devuelve. Resuelve la ambigüedad de selección.
const TOOL_BUENA: Anthropic.Tool = {
  name: "get_account_info",
  description:
    "Retrieves complete account information for a customer. " +
    "Use this when the user asks about their account status, balance, " +
    "subscription plan, or any account-specific detail. " +
    "Do NOT use this for order-specific questions — use get_order_info instead. " +
    "Returns: account_id, email, subscription_plan, account_balance, created_at.",
  input_schema: {
    type: "object",
    properties: {
      account_id: {
        type: "string",
        description:
          "Customer account ID (format: ACC-XXXXXX). " +
          "If not provided, uses the ID from the current conversation context.",
      },
    },
    required: [],
    additionalProperties: false, // Equivalente a strict en Anthropic
  },
};

// Herramienta con schema bien documentado para formatos no obvios.
// Las descripciones de parámetros con ejemplos reducen errores de parametrización.
const TOOL_BUSQUEDA: Anthropic.Tool = {
  name: "search_orders",
  description:
    "Searches orders within a date range and optional status filter. " +
    "Use when the user asks to find, list, or review orders. " +
    "Do NOT use for a single known order ID — use get_order_info instead.",
  input_schema: {
    type: "object",
    properties: {
      date_range: {
        type: "string",
        description:
          "Date range in ISO 8601 format: 'YYYY-MM-DD/YYYY-MM-DD'. " +
          "Example: '2024-01-01/2024-03-31'",
      },
      status: {
        type: "string",
        enum: ["active", "inactive", "pending"],
        description:
          "Account status filter. Use 'active' for currently subscribed accounts.",
      },
      limit: {
        type: "integer",
        minimum: 1,
        maximum: 100,
        description:
          "Maximum number of results. Default is 20. Use higher values only for exports.",
      },
    },
    required: ["date_range"],
    additionalProperties: false,
  },
};

// --- Mock de herramientas ---

function mockGetAccountInfo(args: Record<string, unknown>): string {
  const accountId = (args.account_id ?? args.id ?? "ACC-000000") as string;
  return JSON.stringify({
    account_id: accountId,
    email: "usuario@ejemplo.com",
    subscription_plan: "Pro",
    account_balance: 42.5,
    created_at: "2023-05-15",
  });
}

// --- Demo: diferencia entre descripción mala y buena ---

async function demoDescripcion(
  descripcion: string,
  tool: Anthropic.Tool,
  pregunta: string
): Promise<void> {
  const client = new Anthropic();

  console.log(`\n${"=".repeat(60)}`);
  console.log(`[${descripcion}]`);
  console.log(`Pregunta: ${pregunta}`);
  console.log(
    `Descripción de la herramienta: "${tool.description?.slice(0, 80)}..."`
  );

  const response = await client.messages.create({
    model: MODEL,
    max_tokens: 512,
    tools: [tool],
    messages: [{ role: "user", content: pregunta }],
  });

  if (response.stop_reason === "tool_use") {
    const toolBlock = response.content.find(
      (b): b is Anthropic.ToolUseBlock => b.type === "tool_use"
    )!;

    console.log(`\n  → El modelo invocó '${toolBlock.name}'`);
    console.log(`    input: ${JSON.stringify(toolBlock.input)}`);

    // Devolver el resultado y obtener respuesta final
    const toolResult = mockGetAccountInfo(
      toolBlock.input as Record<string, unknown>
    );

    const final = await client.messages.create({
      model: MODEL,
      max_tokens: 256,
      tools: [tool],
      messages: [
        { role: "user", content: pregunta },
        { role: "assistant", content: response.content },
        {
          role: "user",
          content: [
            {
              type: "tool_result",
              tool_use_id: toolBlock.id,
              content: toolResult,
              is_error: false,
            },
          ],
        },
      ],
    });

    const respuesta = final.content
      .filter((b): b is Anthropic.TextBlock => b.type === "text")
      .map((b) => b.text)
      .join("");

    console.log(`  ← Respuesta final: ${respuesta.slice(0, 120)}`);
  } else {
    // IAC: el modelo respondió sin llamar la herramienta
    const texto = response.content
      .filter((b): b is Anthropic.TextBlock => b.type === "text")
      .map((b) => b.text)
      .join("");

    console.log(
      "\n  [IAC] El modelo respondió desde memoria sin invocar la herramienta."
    );
    console.log(`  Respuesta: ${texto.slice(0, 120)}`);
  }
}

async function demoSchemaDetallado(): Promise<void> {
  const client = new Anthropic();

  const pregunta =
    "Muéstrame los pedidos pendientes de los últimos 3 meses, máximo 50.";
  console.log(`\n${"=".repeat(60)}`);
  console.log("[Schema con descripciones de parámetros]");
  console.log(`Pregunta: ${pregunta}`);

  const response = await client.messages.create({
    model: MODEL,
    max_tokens: 512,
    tools: [TOOL_BUSQUEDA],
    messages: [{ role: "user", content: pregunta }],
  });

  if (response.stop_reason === "tool_use") {
    const toolBlock = response.content.find(
      (b): b is Anthropic.ToolUseBlock => b.type === "tool_use"
    )!;

    console.log(`\n  → El modelo invocó '${toolBlock.name}'`);
    console.log(
      `    input: ${JSON.stringify(toolBlock.input, null, 4)}`
    );
    console.log(
      "\n  Notar: date_range en ISO 8601, status y limit correctamente inferidos."
    );
  } else {
    const texto = response.content
      .filter((b): b is Anthropic.TextBlock => b.type === "text")
      .map((b) => b.text)
      .join("");
    console.log(`\n  Respuesta directa: ${texto.slice(0, 120)}`);
  }
}

async function main(): Promise<void> {
  console.log("=== Representación de herramientas: descripción y schema ===");
  console.log();
  console.log("Principios clave:");
  console.log(
    "  - Descripción efectiva: CUÁNDO usar la tool + QUÉ hace + QUÉ NO hace"
  );
  console.log(
    "  - Schema: descripción de parámetros con ejemplos para formatos no obvios"
  );
  console.log(
    "  - additionalProperties: false equivale a strict (Anthropic aplica"
  );
  console.log(
    "    constrained decoding sobre input_schema por defecto, sin flag opt-in)"
  );
  console.log();
  console.log("Tipos de error:");
  console.log(
    "  - IAC (Insufficient API Calls): el modelo no llama la tool cuando debería"
  );
  console.log(
    "    Causa: descripción que solo describe el mecanismo ('Gets X from DB')"
  );
  console.log("  - Llamada incorrecta: el modelo invoca la tool equivocada");
  console.log(
    "    Causa: falta de diferenciación entre tools similares"
  );

  // Caso 1: descripción pobre → potencial IAC
  await demoDescripcion(
    "Descripción POBRE — solo describe el mecanismo",
    TOOL_MALA,
    "¿Puedes verificar mi cuenta? Mi ID es ACC-123456."
  );

  // Caso 2: descripción efectiva → selección correcta
  await demoDescripcion(
    "Descripción EFECTIVA — incluye cuándo usar, qué no usar, qué devuelve",
    TOOL_BUENA,
    "¿Puedes verificar mi cuenta? Mi ID es ACC-123456."
  );

  // Caso 3: schema con parámetros documentados
  await demoSchemaDetallado();

  console.log(`\n${"=".repeat(60)}`);
  console.log("Nota sobre strict / constrained decoding:");
  console.log(
    "  OpenAI: campo 'strict: true' en la function — incompatible con parallel_tool_calls"
  );
  console.log(
    "  Anthropic: constrained decoding siempre activo sobre input_schema"
  );
  console.log("             sin flag, compatible con parallel tool calls");
  console.log(
    "  En ambos casos: reduce fallo de formato de 2-5% a <0.1%"
  );
}

main().catch(console.error);
