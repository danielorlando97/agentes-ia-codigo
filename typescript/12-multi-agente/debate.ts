// Patrón Debate: N agentes generan respuestas independientes, luego leen las de
// los demás y actualizan las suyas por R rondas. Agregación por mayoría o árbitro.

// Cómo ejecutar: make ts SCRIPT=typescript/12-multi-agente/debate.ts

import Anthropic from "@anthropic-ai/sdk";

const client = new Anthropic();
const MODEL_AGENTS = process.env["SMALL_MODEL"] ?? "claude-haiku-4-5-20251001";
const MODEL_ARBITER = process.env["MODEL"] ?? "claude-sonnet-4-6";

async function llamarLLM(
  system: string,
  messages: Array<{ role: "user" | "assistant"; content: string }>,
  model: string = MODEL_AGENTS,
  temperature: number = 0.7
): Promise<string> {
  const resp = await client.messages.create({
    model,
    max_tokens: 600,
    system,
    messages,
    temperature,
  });
  return (resp.content[0] as { type: "text"; text: string }).text.trim();
}

function majorityVote(respuestas: string[]): string {
  const conteo = new Map<string, number>();
  for (const r of respuestas) {
    const key = r.toLowerCase().trim();
    conteo.set(key, (conteo.get(key) ?? 0) + 1);
  }
  let maxCount = 0;
  let modal = "";
  for (const [k, v] of conteo) {
    if (v > maxCount) { maxCount = v; modal = k; }
  }
  return respuestas.find(r => r.toLowerCase().trim() === modal) ?? respuestas[0];
}

async function llmArbiter(pregunta: string, respuestas: string[]): Promise<string> {
  const poolTexto = respuestas
    .map((r, i) => `Agente ${i + 1}:\n${r}`)
    .join("\n\n");
  const system =
    "Eres un árbitro experto. Lee las respuestas de distintos agentes, " +
    "identifica cuál razonamiento es más sólido y sintetiza la mejor respuesta. " +
    "Si hay contradicción, explica cuál es correcta y por qué.";
  return llamarLLM(
    system,
    [{ role: "user", content: `Pregunta original: ${pregunta}\n\nRespuestas:\n${poolTexto}\n\nProporciona la respuesta final más precisa.` }],
    MODEL_ARBITER,
    0.0
  );
}

async function debate(
  pregunta: string,
  nAgents: number = 3,
  nRounds: number = 2,
  useArbiter: boolean = false
): Promise<string> {
  const agentSystem =
    "Eres un agente analítico. Responde con precisión y razonamiento claro. " +
    "Cuando veas las respuestas de otros agentes, actualiza la tuya si tienen razón; " +
    "mantén tu posición y justifícala si no la tienen.";

  // Ronda 0: respuestas independientes con temperatura alta para diversidad
  let respuestas: string[] = [];
  console.log(`[Ronda 0] Generando ${nAgents} respuestas independientes...`);
  for (let i = 0; i < nAgents; i++) {
    const r = await llamarLLM(
      agentSystem,
      [{ role: "user", content: pregunta }],
      MODEL_AGENTS,
      0.7
    );
    respuestas.push(r);
    console.log(`  Agente ${i + 1}: ${r.slice(0, 80)}...`);
  }

  // Rondas de debate
  for (let ronda = 0; ronda < nRounds; ronda++) {
    console.log(`\n[Ronda ${ronda + 1}] Actualizando respuestas...`);
    const nuevas: string[] = [];
    for (let i = 0; i < nAgents; i++) {
      const otros = respuestas
        .filter((_, j) => j !== i)
        .map((r, j) => `Agente ${j < i ? j + 1 : j + 2}: ${r}`)
        .join("\n\n");
      const actualizada = await llamarLLM(
        agentSystem,
        [
          { role: "user", content: pregunta },
          { role: "assistant", content: respuestas[i] },
          {
            role: "user",
            content:
              `Otros agentes respondieron:\n${otros}\n\n` +
              "Usa sus argumentos para mejorar tu respuesta. " +
              "Si tienen razón, actualiza. Si no, mantén y justifica.",
          },
        ],
        MODEL_AGENTS,
        0.3
      );
      nuevas.push(actualizada);
      console.log(`  Agente ${i + 1} (actualizado): ${actualizada.slice(0, 80)}...`);
    }
    respuestas = nuevas;
  }

  console.log("\n[Agregación]");
  if (useArbiter) {
    console.log("  Usando árbitro LLM...");
    return llmArbiter(pregunta, respuestas);
  }
  console.log("  Usando majority_vote...");
  return majorityVote(respuestas);
}

async function main() {
  const pregunta =
    "Un tren parte de la ciudad A a 60 km/h. Otro parte simultáneamente " +
    "de la ciudad B a 90 km/h en dirección contraria. Las ciudades están a 300 km. " +
    "¿En cuántos minutos se cruzan los trenes?";
  console.log(`Pregunta: ${pregunta}\n`);

  const resultado = await debate(pregunta, 3, 2, false);
  console.log(`\nRespuesta final (majority_vote):\n${resultado}`);

  console.log("\n" + "=".repeat(60) + "\n");

  const resultadoArbitro = await debate(pregunta, 3, 2, true);
  console.log(`\nRespuesta final (árbitro LLM):\n${resultadoArbitro}`);
}

main().catch(console.error);
