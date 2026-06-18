// Copiloto: sugerencia inline disparada por evento del editor. Sin loop, sin estado.

// Cómo ejecutar: make ts SCRIPT=typescript/01-que-es-un-agente/copiloto.ts
// Qué esperar: buffer de codigo + sugerencia de completado generada por el modelo.

import Anthropic from "@anthropic-ai/sdk";

const MODEL = process.env["MODEL"] ?? "claude-sonnet-4-6";
const SYSTEM =
  "Eres un copiloto de codigo. Dado un fragmento de codigo, " +
  "sugiere la continuacion mas probable. Responde solo con el codigo sugerido, sin explicaciones.";

async function suggest(buffer: string): Promise<string> {
  const client = new Anthropic();
  const response = await client.messages.create({
    model: MODEL,
    max_tokens: 256,
    system: SYSTEM,
    messages: [{ role: "user", content: `Completa:\n\n\`\`\`\n${buffer}\n\`\`\`` }],
  });
  return response.content
    .filter((b): b is Anthropic.TextBlock => b.type === "text")
    .map((b) => b.text)
    .join("");
}

const code = "def fibonacci(n):\n    ";
console.log(`Buffer:\n${code}`);
suggest(code).then((s) => console.log(`Sugerencia: ${s}`));
