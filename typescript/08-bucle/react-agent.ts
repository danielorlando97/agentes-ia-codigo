// Cómo ejecutar: make ts SCRIPT=typescript/08-bucle/react-agent.ts
/**
 * ReAct (Reason + Act) — Yao et al. 2022 (arXiv:2210.03629).
 *
 * Implementación text-based: el modelo genera Thought + Action en texto libre;
 * el ejecutor parsea la acción, llama la herramienta e inyecta la Observation.
 *
 * Requiere: npm install @anthropic-ai/sdk
 */
import Anthropic from "@anthropic-ai/sdk";

const MODEL = process.env["MODEL"] ?? "claude-sonnet-4-6";
const MAX_ITERATIONS = 10;

const FEW_SHOT = `\
Thought: Necesito buscar la capital de Australia.
Action: Search[capital Australia]
Observation: La capital de Australia es Canberra.
Thought: Tengo la respuesta.
Action: Finish[Canberra]

---

Thought: Necesito saber quién fue el padre de Zeus.
Action: Search[padre Zeus mitología]
Observation: Crono es el padre de Zeus. Era un Titán que gobernó el cosmos.
Thought: La respuesta es Crono.
Action: Finish[Crono]

---

`;

const SYSTEM =
  "Responde siguiendo el formato Thought/Action/Observation del ejemplo. " +
  "Las acciones disponibles son: Search[query] y Finish[respuesta].";

type ToolFn = (args: string) => string;

interface ReActAgent {
  client: Anthropic;
  tools: Record<string, ToolFn>;
  model?: string;
  maxIterations?: number;
}

function parseAction(text: string): [string, string] | null {
  const m = text.match(/Action:\s*(\w+)\[(.+?)\]/s);
  return m ? [m[1], m[2].trim()] : null;
}

async function runReAct(
  { client, tools, model = MODEL, maxIterations = MAX_ITERATIONS }: ReActAgent,
  task: string
): Promise<string> {
  let prompt = FEW_SHOT + `Task: ${task}\n`;

  for (let i = 0; i < maxIterations; i++) {
    const response = await client.messages.create({
      model,
      max_tokens: 300,
      system: SYSTEM,
      messages: [{ role: "user", content: prompt }],
      stop_sequences: ["Observation:"],
    });

    const generated =
      response.content[0].type === "text" ? response.content[0].text : "";
    prompt += generated;

    console.log(`[iter ${i + 1}] ${generated.trim().slice(0, 100)}`);

    const action = parseAction(generated);
    if (!action) break;

    const [toolName, toolArgs] = action;
    if (toolName === "Finish") return toolArgs;

    const fn = tools[toolName];
    const observation = fn
      ? fn(toolArgs)
      : `[Error: herramienta '${toolName}' no encontrada]`;
    prompt += `Observation: ${observation}\n`;
    console.log(`  Observation: ${observation.slice(0, 80)}`);
  }

  return "[MAX_ITERATIONS sin respuesta]";
}

// Demo
const KB: Record<string, string> = {
  "capital españa": "Madrid es la capital de España.",
  "capital francia": "París es la capital de Francia.",
  "capital alemania": "Berlín es la capital de Alemania.",
  "padre zeus": "Crono es el padre de Zeus en la mitología griega.",
  "capital australia": "La capital de Australia es Canberra.",
};

function search(query: string): string {
  const q = query.toLowerCase();
  for (const [key, val] of Object.entries(KB)) {
    if (key.split(" ").every((w) => q.includes(w))) return val;
  }
  return "No encontré información sobre esa consulta.";
}

(async () => {
  const client = new Anthropic();
  const result = await runReAct(
    { client, tools: { Search: search } },
    "¿Cuáles son las capitales de España y Francia?"
  );
  console.log(`\nRespuesta final: ${result}`);
})();
