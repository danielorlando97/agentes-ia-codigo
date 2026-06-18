// Demostración del pipeline de sampling: temperature, top-p y min-p.
//
// Muestra:
//   1. Efecto de temperature 0.0 / 0.5 / 1.0 en varianza de output
//   2. Diversidad léxica (TTR) como métrica de varianza
//   3. Tasa de JSON malformado en tool calling con temperature alta (1.5)
//   4. Tabla resumen comparativa

// Cómo ejecutar: make ts SCRIPT=typescript/03-motor-llm/sampling.ts

import Anthropic from "@anthropic-ai/sdk";

const MODEL = process.env["MODEL"] ?? "claude-sonnet-4-6";
const SMALL_MODEL = process.env["SMALL_MODEL"] ?? "claude-haiku-4-5-20251001";

const PROMPT_CREATIVO =
  "En dos oraciones, explica por qué el cielo es azul. Sé creativo y variado en tu respuesta.";

const TOOL_SCHEMA: Anthropic.Tool[] = [
  {
    name: "crear_tarea",
    description: "Crea una tarea en el gestor de proyectos.",
    input_schema: {
      type: "object",
      properties: {
        titulo: { type: "string", description: "Título corto de la tarea" },
        prioridad: {
          type: "string",
          enum: ["alta", "media", "baja"],
          description: "Nivel de prioridad",
        },
        estimacion_horas: {
          type: "number",
          description: "Estimación en horas (número decimal)",
        },
      },
      required: ["titulo", "prioridad", "estimacion_horas"],
    },
  },
];

const PROMPT_TOOL =
  "Crea una tarea para revisar el informe de ventas del Q3. Prioridad alta, estimación 2.5 horas.";

/** Type-Token Ratio: palabras únicas / total palabras. */
function diversidadLexica(texto: string): number {
  const palabras = texto.toLowerCase().match(/\b\w+\b/g) ?? [];
  if (palabras.length === 0) return 0;
  return new Set(palabras).size / palabras.length;
}

/** Mide varianza de output para distintas temperatures. */
async function medirVarianzaTemperature(
  temperatures: number[],
  repeticiones = 3
): Promise<void> {
  const client = new Anthropic();
  console.log("\n[varianza de output por temperature]");
  console.log(`  Prompt: '${PROMPT_CREATIVO.slice(0, 60)}...'`);
  console.log(`  Repeticiones por temperatura: ${repeticiones}\n`);

  for (const temp of temperatures) {
    const longitudes: number[] = [];
    const ttrs: number[] = [];
    const outputs: string[] = [];

    for (let rep = 0; rep < repeticiones; rep++) {
      const params: Anthropic.MessageCreateParamsNonStreaming = {
        model: SMALL_MODEL,
        max_tokens: 120,
        messages: [{ role: "user", content: PROMPT_CREATIVO }],
      };
      if (temp > 0) params.temperature = temp;

      const resp = await client.messages.create(params);
      const texto = resp.content
        .filter((b): b is Anthropic.TextBlock => b.type === "text")
        .map((b) => b.text)
        .join("");

      longitudes.push(texto.split(/\s+/).length);
      ttrs.push(diversidadLexica(texto));
      outputs.push(texto);
    }

    const avgLen = longitudes.reduce((a, b) => a + b, 0) / repeticiones;
    const avgTtr = ttrs.reduce((a, b) => a + b, 0) / repeticiones;
    const rangoLen = Math.max(...longitudes) - Math.min(...longitudes);

    const label = `T=${temp.toFixed(1)}`;
    console.log(
      `  ${label.padEnd(6)}  avg_palabras=${avgLen.toFixed(1).padStart(5)}  ` +
        `rango_len=${String(rangoLen).padStart(3)}  TTR=${avgTtr.toFixed(3)}`
    );
    outputs.forEach((out, i) =>
      console.log(`         rep${i + 1}: ${JSON.stringify(out.slice(0, 90))}`)
    );
    console.log();
  }
}

/** Mide tasa de JSON malformado en tool calling por temperatura. */
async function medirTasaJsonMalformado(
  temperatures: number[],
  intentos = 5
): Promise<void> {
  const client = new Anthropic();
  console.log("\n[tasa de JSON malformado en tool calling]");
  console.log(`  Intentos por temperatura: ${intentos}\n`);

  for (const temp of temperatures) {
    let fallos = 0;
    const errores: string[] = [];

    for (let i = 0; i < intentos; i++) {
      const params: Anthropic.MessageCreateParamsNonStreaming = {
        model: SMALL_MODEL,
        max_tokens: 256,
        tools: TOOL_SCHEMA,
        messages: [{ role: "user", content: PROMPT_TOOL }],
      };
      if (temp > 0) params.temperature = temp;

      const resp = await client.messages.create(params);
      const toolUses = resp.content.filter(
        (b): b is Anthropic.ToolUseBlock => b.type === "tool_use"
      );

      if (toolUses.length === 0) {
        fallos++;
        errores.push("sin tool_use en respuesta");
        continue;
      }

      const input = toolUses[0].input as Record<string, unknown>;
      const required = new Set(["titulo", "prioridad", "estimacion_horas"]);
      const missing = [...required].filter((k) => !(k in input));

      if (missing.length > 0) {
        fallos++;
        errores.push(`campos faltantes: ${missing.join(", ")}`);
      } else if (!["alta", "media", "baja"].includes(input.prioridad as string)) {
        fallos++;
        errores.push(`prioridad inválida: ${JSON.stringify(input.prioridad)}`);
      }
    }

    const tasa = fallos / intentos;
    const label = `T=${temp.toFixed(1)}`;
    console.log(
      `  ${label.padEnd(6)}  fallos=${fallos}/${intentos}  tasa_error=${(tasa * 100).toFixed(0)}%`
    );
    errores.forEach((e) => console.log(`         ✗ ${e}`));
  }
  console.log();
}

function tablaResumen(): void {
  console.log("\n[tabla resumen: temperatura vs uso recomendado]");
  const filas: [string, string, string, string, string][] = [
    ["0.0", "Greedy",      "Mínima",  "Máxima local", "Tool calling, JSON, extracción estructurada"],
    ["0.5", "Concentrada", "Baja",    "Alta",          "Q&A factual, código, análisis"],
    ["1.0", "Original",    "Media",   "Buena",         "Chatbot conversacional, texto general"],
    ["1.5", "Plana",       "Alta",    "Menor",         "Escritura creativa (usar min-p=0.05)"],
  ];
  console.log(
    `  ${"T".padStart(4)}  ${"Distribución".padEnd(15)}  ${"Diversidad".padEnd(12)}  ${"Coherencia".padEnd(12)}  Uso`
  );
  console.log("  " + "-".repeat(80));
  filas.forEach(([t, dist, div, coh, uso]) =>
    console.log(
      `  ${t.padStart(4)}  ${dist.padEnd(15)}  ${div.padEnd(12)}  ${coh.padEnd(12)}  ${uso}`
    )
  );
}

async function main(): Promise<void> {
  console.log("=== Sampling: temperatura, diversidad y fiabilidad ===");
  await medirVarianzaTemperature([0.0, 0.5, 1.0], 3);
  await medirTasaJsonMalformado([0.0, 0.5, 1.0, 1.5], 5);
  tablaResumen();
}

main().catch(console.error);
