// Cómo ejecutar: make ts SCRIPT=typescript/10-decisiones/confianza.ts
import Anthropic from "@anthropic-ai/sdk";

const MODEL = process.env["MODEL"] ?? "claude-sonnet-4-6";
const client = new Anthropic({ apiKey: process.env.ANTHROPIC_API_KEY });

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

async function main() {
  const pregunta =
    "¿Cuánto es 17 × 23? Razona paso a paso y da solo el número al final.";

  console.log(`Pregunta: ${pregunta}`);
  console.log("Muestreando k=5 respuestas con temperature=0.7...\n");

  const respuestasRaw: string[] = [];
  for (let i = 0; i < 5; i++) {
    const resp = await client.messages.create({
      model: MODEL,
      max_tokens: 200,
      temperature: 0.7,
      messages: [{ role: "user", content: pregunta }],
    });
    const texto = (resp.content[0] as { type: string; text: string }).text;
    const extraida = extraerRespuesta(texto);
    respuestasRaw.push(extraida);
    console.log(`  Muestra ${i + 1}: '${extraida}'`);
  }

  const conteos = new Map<string, number>();
  for (const r of respuestasRaw) {
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
  const confianza = votos / 5;

  console.log(`\nDistribución de votos: ${JSON.stringify(Object.fromEntries(conteos))}`);
  console.log(`Respuesta: ${mejor}`);
  console.log(`Confianza: ${confianza.toFixed(2)} (${votos}/5 votos)`);
}

main().catch(console.error);

export { selfConsistency, extraerRespuesta };
