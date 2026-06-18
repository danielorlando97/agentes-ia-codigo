// Cómo ejecutar: make ts SCRIPT=typescript/10-decisiones/abstencion.ts
import Anthropic from "@anthropic-ai/sdk";

const MODEL = process.env["MODEL"] ?? "claude-sonnet-4-6";
const client = new Anthropic({ apiKey: process.env.ANTHROPIC_API_KEY });

const UMBRAL_RESPONDER = 0.8;
const UMBRAL_SOFT = 0.5;

interface PredictionResult {
  tipo: "respuesta" | "soft" | "abstencion";
  contenido: string;
  confianza: number;
}

function extraerRespuesta(texto: string): string {
  const matches = texto.match(/\b(\d+(?:\.\d+)?)\b/g);
  if (matches && matches.length > 0) {
    return matches[matches.length - 1];
  }
  const lineas = texto
    .split("\n")
    .map((l) => l.trim())
    .filter((l) => l.length > 0);
  return lineas.length > 0 ? lineas[lineas.length - 1].slice(0, 50) : texto.slice(0, 50);
}

async function selfConsistency(
  prompt: string,
  k: number = 5
): Promise<[string, number]> {
  const respuestas: string[] = [];

  for (let i = 0; i < k; i++) {
    const resp = await client.messages.create({
      model: MODEL,
      max_tokens: 200,
      temperature: 0.7,
      messages: [{ role: "user", content: prompt }],
    });
    const texto = (resp.content[0] as { type: string; text: string }).text;
    respuestas.push(extraerRespuesta(texto));
  }

  const conteos = new Map<string, number>();
  for (const r of respuestas) {
    conteos.set(r, (conteos.get(r) ?? 0) + 1);
  }

  let mejor = "";
  let votos = 0;
  for (const [respuesta, count] of conteos) {
    if (count > votos) {
      mejor = respuesta;
      votos = count;
    }
  }

  return [mejor, votos / k];
}

async function selectivePredict(query: string): Promise<PredictionResult> {
  const [respuesta, confianza] = await selfConsistency(query);

  if (confianza >= UMBRAL_RESPONDER) {
    return { tipo: "respuesta", contenido: respuesta, confianza };
  } else if (confianza >= UMBRAL_SOFT) {
    return {
      tipo: "soft",
      contenido: `Según mi información (no verificada): ${respuesta}. Recomiendo verificar.`,
      confianza,
    };
  } else {
    return {
      tipo: "abstencion",
      contenido: "No tengo suficiente certeza para responder esto correctamente.",
      confianza,
    };
  }
}

async function main() {
  const queries = [
    "¿Cuánto es 8 × 7? Da solo el número.",
    "¿Quién ganó el Premio Nobel de Literatura en 2019?",
    "¿Cuál es el precio exacto de la acción de Apple en este momento?",
  ];

  for (const q of queries) {
    console.log(`Query: ${q}`);
    const resultado = await selectivePredict(q);
    console.log(`  Tipo:      ${resultado.tipo}`);
    console.log(`  Contenido: ${resultado.contenido}`);
    console.log(`  Confianza: ${resultado.confianza.toFixed(2)}`);
    console.log();
  }
}

main().catch(console.error);
