// Demostración del efecto 'lost in the middle' y coste de estrategias de contexto.
//
// Muestra:
//   1. Construcción de contexto con hechos en inicio, medio y final
//   2. Accuracy de recuperación por posición
//   3. Costo en tokens: full-context vs RAG selectivo
//   4. Savings estimados y proyección a escala

// Cómo ejecutar: make ts SCRIPT=typescript/03-motor-llm/ventana-contexto.ts

import Anthropic from "@anthropic-ai/sdk";

const MODEL = process.env["MODEL"] ?? "claude-sonnet-4-6";
const SMALL_MODEL = process.env["SMALL_MODEL"] ?? "claude-haiku-4-5-20251001";

// Precios Haiku 4.5 (USD por millón de tokens, Mayo 2025)
const PRECIO_INPUT_POR_MILLON  = 0.80;
const PRECIO_OUTPUT_POR_MILLON = 4.00;

const HECHO_INICIO = "CÓDIGO_INICIO: el código de seguridad del servidor es ALFA-7742.";
const HECHO_MEDIO  = "CÓDIGO_MEDIO: el código de seguridad del servidor es BETA-3319.";
const HECHO_FINAL  = "CÓDIGO_FINAL: el código de seguridad del servidor es GAMMA-8851.";

const RELLENO_PARRAFO =
  "El equipo de infraestructura realizó mantenimiento preventivo en todos los nodos " +
  "del clúster. Se actualizaron las dependencias de seguridad y se realizaron pruebas " +
  "de carga con resultados satisfactorios. El tiempo de respuesta promedio se mantuvo " +
  "por debajo de los 200ms durante toda la ventana de mantenimiento programada. " +
  "Los registros de auditoría no mostraron anomalías y el sistema quedó estable. ";

/** Construye un documento con hechos en inicio, medio y final. */
function construirContexto(rellenoBloques = 30): string {
  const bloqueRelleno = RELLENO_PARRAFO.repeat(5) + "\n\n";
  const mitad = Math.floor(rellenoBloques / 2);
  const partes: string[] = ["=== INFORME DE INFRAESTRUCTURA ===\n\n", HECHO_INICIO + "\n\n"];
  for (let i = 0; i < mitad; i++) partes.push(bloqueRelleno);
  partes.push(HECHO_MEDIO + "\n\n");
  for (let i = 0; i < mitad; i++) partes.push(bloqueRelleno);
  partes.push(HECHO_FINAL + "\n\n");
  partes.push("=== FIN DEL INFORME ===\n");
  return partes.join("");
}

/** Estimación simple de tokens: ~4 chars por token (heurística). */
function estimarTokens(texto: string): number {
  return Math.ceil(texto.length / 4);
}

function calcularCostoUsd(tokensInput: number, tokensOutput = 50): number {
  return (
    (tokensInput / 1_000_000) * PRECIO_INPUT_POR_MILLON +
    (tokensOutput / 1_000_000) * PRECIO_OUTPUT_POR_MILLON
  );
}

const PREGUNTAS: Record<string, { pregunta: string; esperado: string }> = {
  inicio: { pregunta: "¿Cuál es el CÓDIGO_INICIO mencionado en el informe?", esperado: "ALFA-7742" },
  medio:  { pregunta: "¿Cuál es el CÓDIGO_MEDIO mencionado en el informe?",  esperado: "BETA-3319" },
  final:  { pregunta: "¿Cuál es el CÓDIGO_FINAL mencionado en el informe?",  esperado: "GAMMA-8851" },
};

/** Mide accuracy de recuperación por posición del hecho en el contexto. */
async function medirAccuracyPorPosicion(
  contexto: string,
  repeticiones = 3
): Promise<void> {
  const client = new Anthropic();
  console.log("\n[accuracy de recuperación por posición del hecho]");
  console.log(`  Repeticiones por posición: ${repeticiones}\n`);

  for (const [posicion, { pregunta, esperado }] of Object.entries(PREGUNTAS)) {
    let aciertos = 0;

    for (let rep = 0; rep < repeticiones; rep++) {
      const resp = await client.messages.create({
        model: SMALL_MODEL,
        max_tokens: 64,
        system:
          "Responde SOLO con el código pedido, sin explicaciones. " +
          "Si no lo encuentras, responde 'NO_ENCONTRADO'.",
        messages: [{ role: "user", content: `${contexto}\n\n---\n${pregunta}` }],
      });

      const texto = resp.content
        .filter((b): b is Anthropic.TextBlock => b.type === "text")
        .map((b) => b.text)
        .join("")
        .trim();

      if (texto.includes(esperado)) aciertos++;
    }

    const tasa = aciertos / repeticiones;
    console.log(
      `  ${posicion.padEnd(6)}  accuracy=${(tasa * 100).toFixed(0)}%  (esperado: ${esperado})`
    );
  }
}

/** Compara el coste de enviar el contexto completo vs solo el chunk relevante. */
function compararEstrategiasContexto(contextoFull: string): void {
  console.log("\n[comparación de estrategias de contexto]");

  const tokensFull = estimarTokens(contextoFull);
  const chunks: Record<string, string> = {
    "inicio (RAG)": `Sección de inicio del informe:\n${HECHO_INICIO}`,
    "medio (RAG)":  `Sección intermedia del informe:\n${HECHO_MEDIO}`,
    "final (RAG)":  `Sección final del informe:\n${HECHO_FINAL}`,
  };

  for (const [nombre, chunk] of Object.entries(chunks)) {
    const tokensChunk = estimarTokens(chunk);
    const savingTokens = tokensFull - tokensChunk;
    const savingPct = (savingTokens / tokensFull) * 100;
    const costoFull  = calcularCostoUsd(tokensFull);
    const costoChunk = calcularCostoUsd(tokensChunk);
    const ahorroUsd  = costoFull - costoChunk;

    console.log(`\n  Recuperar hecho de ${nombre}:`);
    console.log(`    Full-context:  ${tokensFull.toString().padStart(6)} tokens  $${costoFull.toFixed(6)}`);
    console.log(`    Solo chunk:    ${tokensChunk.toString().padStart(6)} tokens  $${costoChunk.toFixed(6)}`);
    console.log(
      `    Ahorro:        ${savingTokens.toString().padStart(6)} tokens  ` +
        `(${savingPct.toFixed(1)}%)  $${ahorroUsd.toFixed(6)}`
    );
  }

  // Proyección a escala
  const requestsDia = 10_000;
  const costoFullDia = calcularCostoUsd(tokensFull) * requestsDia;
  const costoRagDia  = calcularCostoUsd(estimarTokens(chunks["inicio (RAG)"])) * requestsDia;
  const ratio = costoFullDia / costoRagDia;

  console.log(`\n  Proyección a ${requestsDia.toLocaleString()} requests/día:`);
  console.log(
    `    Full-context:  $${costoFullDia.toFixed(2)}/día  ≈ $${(costoFullDia * 30).toFixed(0)}/mes`
  );
  console.log(
    `    RAG selectivo: $${costoRagDia.toFixed(2)}/día  ≈ $${(costoRagDia * 30).toFixed(0)}/mes`
  );
  console.log(`    Full-context cuesta ~${ratio.toFixed(0)}x más que RAG selectivo`);
}

async function main(): Promise<void> {
  console.log("=== Ventana de contexto: lost-in-the-middle y coste de estrategias ===");

  const contexto = construirContexto(30);
  const tokensTotales = estimarTokens(contexto);
  console.log(`\nContexto construido: ~${tokensTotales} tokens (~${contexto.split(/\s+/).length.toLocaleString()} palabras)`);

  await medirAccuracyPorPosicion(contexto, 3);
  compararEstrategiasContexto(contexto);
}

main().catch(console.error);
