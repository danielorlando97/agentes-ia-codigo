// Cómo ejecutar: make ts SCRIPT=typescript/08-bucle/tree-of-thoughts.ts
/**
 * Tree of Thoughts (ToT) — Yao et al. 2023 (arXiv:2305.10601).
 *
 * totBfs: beam search en anchura; conserva los beamWidth mejores por nivel.
 * totDfs: búsqueda en profundidad con backtracking en nodos 'impossible'.
 * proponer: genera k pensamientos candidatos (temp=0.7).
 * evaluar: clasifica el estado como sure/maybe/impossible (temp=0.0).
 * esSolucion: función configurable por el llamador.
 *
 * Requiere: npm install @anthropic-ai/sdk
 */
import Anthropic from "@anthropic-ai/sdk";

const MODEL = process.env["MODEL"] ?? "claude-sonnet-4-6";

const PROMPT_PROPUESTA = (estado: string, k: number) =>
  `Estado actual del problema:\n${estado}\n\nGenera ${k} posibles próximos pasos (uno por línea, comenzando con un verbo).`;

const PROMPT_EVALUACION = (objetivo: string, estado: string) =>
  `Objetivo: ${objetivo}\nEstado: ${estado}\n\n¿Es posible alcanzar el objetivo desde este estado?\nResponde SOLO una de estas tres palabras: sure | maybe | impossible`;

type EsSolucionFn = (estado: string, objetivo: string) => boolean;
type Evaluacion = "sure" | "maybe" | "impossible";

interface ToTConfig {
  client: Anthropic;
  esSolucion: EsSolucionFn;
  branchingFactor?: number;
  profundidadMax?: number;
  beamWidth?: number;
  model?: string;
}

async function proponer(
  client: Anthropic,
  estado: string,
  k: number,
  model: string
): Promise<string[]> {
  const resp = await client.messages.create({
    model,
    max_tokens: 300,
    temperature: 0.7,
    messages: [{ role: "user", content: PROMPT_PROPUESTA(estado, k) }],
  });
  const text = resp.content[0].type === "text" ? resp.content[0].text : "";
  return text
    .split("\n")
    .map((l) => l.trim())
    .filter(Boolean)
    .slice(0, k);
}

async function evaluar(
  client: Anthropic,
  estado: string,
  objetivo: string,
  model: string
): Promise<Evaluacion> {
  const resp = await client.messages.create({
    model,
    max_tokens: 5,
    temperature: 0.0,
    messages: [{ role: "user", content: PROMPT_EVALUACION(objetivo, estado) }],
  });
  const t =
    resp.content[0].type === "text" ? resp.content[0].text.toLowerCase() : "";
  if (t.includes("sure")) return "sure";
  if (t.includes("impossible")) return "impossible";
  return "maybe";
}

async function totBfs(
  cfg: ToTConfig,
  estadoInicial: string,
  objetivo: string
): Promise<string | null> {
  const {
    client,
    esSolucion,
    branchingFactor = 3,
    profundidadMax = 3,
    beamWidth = 3,
    model = MODEL,
  } = cfg;
  let frontera = [estadoInicial];

  for (let depth = 0; depth < profundidadMax; depth++) {
    const candidatos: Array<[string, Evaluacion]> = [];

    for (const estado of frontera) {
      if (esSolucion(estado, objetivo)) return estado;
      const propuestas = await proponer(client, estado, branchingFactor, model);
      for (const prop of propuestas) {
        const nuevo = `${estado}\n${prop}`;
        const ev = await evaluar(client, nuevo, objetivo, model);
        if (ev !== "impossible") candidatos.push([nuevo, ev]);
      }
    }

    candidatos.sort((a, b) => (a[1] === "sure" ? -1 : b[1] === "sure" ? 1 : 0));
    frontera = candidatos.slice(0, beamWidth).map(([e]) => e);
    console.log(`  [BFS depth=${depth + 1}] ${frontera.length} nodos en frontera`);
    if (!frontera.length) break;
  }

  return frontera[0] ?? null;
}

async function totDfs(
  cfg: ToTConfig,
  estado: string,
  objetivo: string,
  depth = 0
): Promise<string | null> {
  const {
    client,
    esSolucion,
    branchingFactor = 3,
    profundidadMax = 3,
    model = MODEL,
  } = cfg;

  if (esSolucion(estado, objetivo)) return estado;
  if (depth >= profundidadMax) return null;

  const propuestas = await proponer(client, estado, branchingFactor, model);
  for (const prop of propuestas) {
    const nuevo = `${estado}\n${prop}`;
    const ev = await evaluar(client, nuevo, objetivo, model);
    if (ev === "impossible") {
      console.log(`  [DFS depth=${depth + 1}] backtrack: '${prop.slice(0, 40)}'`);
      continue;
    }
    const resultado = await totDfs(cfg, nuevo, objetivo, depth + 1);
    if (resultado) return resultado;
  }
  return null;
}

// Demo
(async () => {
  const client = new Anthropic();

  const esSolucion = (estado: string, _objetivo: string): boolean => {
    const e = estado.toLowerCase();
    return e.includes("5-7-5") || (e.includes("haiku") && e.split("\n").length > 5);
  };

  const cfg: ToTConfig = {
    client,
    esSolucion,
    branchingFactor: 2,
    profundidadMax: 2,
    beamWidth: 2,
  };

  const objetivo = "Escribe un haiku sobre el otoño con 5-7-5 sílabas";
  const estadoInicial = `Objetivo: ${objetivo}\nEstado: ningún pensamiento todavía.`;

  console.log("=== BFS ===");
  const bfsResult = await totBfs(cfg, estadoInicial, objetivo);
  console.log(bfsResult ? `Mejor estado:\n${bfsResult.slice(-300)}` : "Sin solución");

  console.log("\n=== DFS ===");
  const dfsResult = await totDfs(cfg, estadoInicial, objetivo);
  console.log(dfsResult ? `Estado DFS:\n${dfsResult.slice(-300)}` : "Sin solución");
})();
