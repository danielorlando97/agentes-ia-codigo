// Comparación entre modelo instruct y modelo base (simulada).
//
// Muestra:
//   1. Diferencia de formato: instrucción directa vs completar texto
//   2. Diferencia de output: seguimiento de instrucciones vs continuación libre
//   3. Tokens consumidos y tasa de seguimiento de instrucciones
//
// NOTA: Anthropic no expone modelos base en su API pública.
// Este script simula el contraste enviando dos tipos de prompt al mismo modelo
// instruct y midiendo el seguimiento de instrucciones en cada caso.

// Cómo ejecutar: make ts SCRIPT=typescript/03-motor-llm/instruct-vs-base.ts

import Anthropic from "@anthropic-ai/sdk";

const MODEL = process.env["MODEL"] ?? "claude-sonnet-4-6";
const SMALL_MODEL = process.env["SMALL_MODEL"] ?? "claude-haiku-4-5-20251001";

const PROMPT_INSTRUCT =
  "Lista los tres pasos principales para preparar una taza de té. " +
  "Sé conciso. Usa formato de lista numerada.";

const PROMPT_BASE = "Para preparar una taza de té, primero";

const SYSTEM_BASE_SIM =
  "Continúa el texto que se te da. No añadas saludos ni despedidas. " +
  "No uses formato de lista a menos que el texto de entrada lo sugiera. " +
  "Escribe en el mismo registro y tono del texto de entrada.";

/** Verifica si el texto contiene una lista numerada. */
function detectarListaNumerada(texto: string): boolean {
  return /^\s*[123]\./m.test(texto);
}

/** Verifica si el texto contiene exactamente 3 ítems numerados. */
function detectarTresPasos(texto: string): boolean {
  return (texto.match(/^\s*\d+\./gm) ?? []).length === 3;
}

interface ConfiguracionPrompt {
  label: string;
  prompt: string;
  system?: string;
  descripcion: string;
}

/** Mide seguimiento de instrucciones para distintas configuraciones de prompt. */
async function medirSeguimientoInstrucciones(repeticiones = 3): Promise<void> {
  const client = new Anthropic();
  console.log("\n[comparación: prompt instruct vs prompt base]");
  console.log(`  Repeticiones: ${repeticiones}\n`);

  const configuraciones: ConfiguracionPrompt[] = [
    {
      label: "instruct-prompt",
      prompt: PROMPT_INSTRUCT,
      descripcion: "Instrucción directa (formato imperativo con requisitos explícitos)",
    },
    {
      label: "base-sim-prompt",
      prompt: PROMPT_BASE,
      system: SYSTEM_BASE_SIM,
      descripcion: "Continuación de texto (simulación del estilo base model)",
    },
  ];

  for (const config of configuraciones) {
    console.log(`  --- ${config.label} ---`);
    console.log(`  ${config.descripcion}`);
    console.log(`  Prompt: ${JSON.stringify(config.prompt)}`);
    console.log();

    let tasaLista   = 0;
    let tasa3Pasos  = 0;
    let totalTokens = 0;
    const outputs: string[] = [];

    for (let rep = 0; rep < repeticiones; rep++) {
      const params: Anthropic.MessageCreateParamsNonStreaming = {
        model: SMALL_MODEL,
        max_tokens: 200,
        messages: [{ role: "user", content: config.prompt }],
      };
      if (config.system) params.system = config.system;

      const resp = await client.messages.create(params);
      const texto = resp.content
        .filter((b): b is Anthropic.TextBlock => b.type === "text")
        .map((b) => b.text)
        .join("");

      outputs.push(texto);
      if (detectarListaNumerada(texto)) tasaLista++;
      if (detectarTresPasos(texto))    tasa3Pasos++;
      totalTokens += resp.usage.input_tokens + resp.usage.output_tokens;
    }

    const avgTokens = totalTokens / repeticiones;

    console.log(`  Tasa lista numerada:  ${tasaLista}/${repeticiones} (${((tasaLista / repeticiones) * 100).toFixed(0)}%)`);
    console.log(`  Tasa 3 ítems exactos: ${tasa3Pasos}/${repeticiones} (${((tasa3Pasos / repeticiones) * 100).toFixed(0)}%)`);
    console.log(`  Tokens promedio/call: ${avgTokens.toFixed(0)}`);
    console.log();
    console.log("  Outputs:");
    outputs.forEach((out, i) =>
      console.log(`    rep${i + 1}: ${JSON.stringify(out.slice(0, 120))}`)
    );
    console.log();
  }
}

function tablaDiferencias(): void {
  console.log("\n[tabla: diferencias documentadas base vs instruct]");
  const filas: [string, string, string][] = [
    ["Formato de prompt",  "Instrucción imperativa directa", "Texto a completar"],
    ["Output esperado",    "Sigue instrucciones explícitas", "Continúa el texto dado"],
    ["Saludos/formato",    "Sí (conversacional por defecto)", "No (texto plano)"],
    ["Seguimiento reglas", "Alto (RLHF/SFT orientado)",      "Bajo (no fine-tuneado)"],
    ["Uso en agentes",     "Siempre (tool calling, system)", "Nunca directamente"],
    ["Acceso API",         "Público (claude-haiku, sonnet)", "No expuesto por Anthropic"],
    ["Temperatura típica", "0.0–1.0 según tarea",            "0.7–1.0 para completar"],
  ];
  console.log(
    `  ${"Dimensión".padEnd(30)}  ${"Instruct model".padEnd(35)}  Base model`
  );
  console.log("  " + "-".repeat(100));
  filas.forEach(([dim, inst, base]) =>
    console.log(`  ${dim.padEnd(30)}  ${inst.padEnd(35)}  ${base}`)
  );
  console.log();
}

async function main(): Promise<void> {
  console.log("=== Instruct vs Base: diferencias de comportamiento ===");
  await medirSeguimientoInstrucciones(3);
  tablaDiferencias();
}

main().catch(console.error);
