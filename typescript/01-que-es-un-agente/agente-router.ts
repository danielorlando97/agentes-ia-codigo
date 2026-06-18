// Nivel ★☆☆ router: LLM elige una ruta entre N. Sin loop, sin tools.

// Cómo ejecutar: make ts SCRIPT=typescript/01-que-es-un-agente/agente-router.ts
// Qué esperar: 4 casos de prueba, cada uno con la ruta elegida por el LLM.

import Anthropic from "@anthropic-ai/sdk";

const MODEL = process.env["MODEL"] ?? "claude-sonnet-4-6";
const RUTAS = ["facturacion", "soporte_tecnico", "ventas", "otro"] as const;
const SYSTEM =
  "Clasifica el mensaje del usuario en exactamente una de estas rutas: " +
  RUTAS.join(", ") +
  ". Responde solo con el nombre de la ruta, sin explicacion ni puntuacion.";

async function route(userInput: string): Promise<string> {
  const client = new Anthropic();
  const response = await client.messages.create({
    model: MODEL,
    max_tokens: 16,
    system: SYSTEM,
    messages: [{ role: "user", content: userInput }],
  });
  const text = response.content
    .filter((b): b is Anthropic.TextBlock => b.type === "text")
    .map((b) => b.text)
    .join("")
    .trim()
    .toLowerCase();
  return (RUTAS as readonly string[]).includes(text) ? text : "otro";
}

async function main() {
  const cases = [
    "No me llego la factura de marzo",
    "El servicio se cae cada vez que entro",
    "Quiero cambiar al plan empresarial",
    "Hace buen tiempo hoy",
  ];
  for (const c of cases) {
    const r = await route(c);
    console.log(r.padEnd(18) + "  " + c);
  }
}

main();
