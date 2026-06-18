// Cómo ejecutar: make ts SCRIPT=typescript/09-planificacion/llm_compiler.ts
/**
 * LLM Compiler (Kim et al. 2023, arXiv:2312.04511) — ejecución paralela de DAG.
 *
 * Planner LLM genera un plan con $idx como dependencias implícitas.
 * Task Fetching Unit schedula con Promises — cada tarea espera sus deps
 * antes de ejecutar. Joiner decide Finish o Replan.
 *
 * Requiere: npm install @anthropic-ai/sdk
 */
import Anthropic from "@anthropic-ai/sdk";

const MODEL = process.env["MODEL"] ?? "claude-sonnet-4-6";
const MAX_REPLANS = 3;

const PLANNER_SYSTEM = (toolDocs: string) => `\
Eres un planificador. Descompón el problema en tool calls que maximicen el paralelismo.

Formato estricto (una tarea por línea):
  <idx>. <tool>(<args>)

Reglas:
- Índices desde 1, estrictamente crecientes
- Para usar el output de la tarea N como argumento: $N
- Tareas sin $N en sus args se ejecutan en paralelo de inmediato
- Última línea siempre: join()

Herramientas disponibles:
${toolDocs}`;

const JOINER_PROMPT = (tao: string) => `\
Historial de ejecución del plan:
${tao}

Decide:
- Si la información es suficiente: Finish(<respuesta completa>)
- Si falta información: Replan(<qué faltó>)

Responde SOLO con una de las dos opciones anteriores.`;

interface Tarea {
  idx: number;
  tool: string;
  args: string;
  deps: Set<number>;
}

type ToolFn = (args: string) => string | Promise<string>;

function parsearPlan(texto: string): Tarea[] {
  const patron = /^(\d+)\.\s+(\w+)\(([^)]*)\)/gm;
  const tareas: Tarea[] = [];
  for (const m of texto.matchAll(patron)) {
    const idx = parseInt(m[1]);
    const tool = m[2];
    const argsStr = m[3];
    if (tool === "join") continue;
    const deps = new Set(
      [...argsStr.matchAll(/\$(\d+)/g)].map((d) => parseInt(d[1]))
    );
    tareas.push({ idx, tool, args: argsStr, deps });
  }
  return tareas;
}

function validarPlan(tareas: Tarea[]): void {
  const ids = new Set(tareas.map((t) => t.idx));
  for (const t of tareas) {
    const invalidos = [...t.deps].filter((dep) => dep >= t.idx || !ids.has(dep));
    for (const dep of invalidos) {
      console.log(`  [warn] Tarea ${t.idx}: dep $${dep} inválida — ignorando`);
      t.deps.delete(dep);
    }
  }
}

function sustituirPlaceholders(args: string, resultados: Map<number, string>): string {
  return args.replace(/\$(\d+)/g, (_, n) => resultados.get(parseInt(n)) ?? `$${n}`);
}

async function ejecutarDag(
  tareas: Tarea[],
  tools: Record<string, ToolFn>
): Promise<Map<number, string>> {
  const resultados = new Map<number, string>();

  // Un Promise por tarea que se resuelve cuando la tarea completa
  // — equivalente a asyncio.Event para broadcast a múltiples deps
  const promises = new Map<number, Promise<void>>();
  const resolvers = new Map<number, () => void>();

  for (const t of tareas) {
    const p = new Promise<void>((resolve) => resolvers.set(t.idx, resolve));
    promises.set(t.idx, p);
  }

  async function ejecutar(tarea: Tarea): Promise<void> {
    if (tarea.deps.size > 0) {
      await Promise.all([...tarea.deps].map((d) => promises.get(d)!));
    }

    const args = sustituirPlaceholders(tarea.args, resultados);
    const fn = tools[tarea.tool];
    const resultado = fn
      ? String(await fn(args))
      : `[tool '${tarea.tool}' no registrada]`;

    resultados.set(tarea.idx, resultado);
    resolvers.get(tarea.idx)!(); // notificar a tareas dependientes

    console.log(`  T${tarea.idx} ${tarea.tool}(${args.slice(0, 40)}) → ${resultado.slice(0, 50)}`);
  }

  await Promise.all(tareas.map((t) => ejecutar(t)));
  return resultados;
}

function parsearJoiner(texto: string): [string, string] {
  const m = texto.match(/(Finish|Replan)\((.+?)\)$/is);
  if (m) return [m[1][0].toUpperCase() + m[1].slice(1).toLowerCase(), m[2].trim()];
  return ["Finish", texto.trim()];
}

async function llmCompiler(
  tarea: string,
  tools: Record<string, ToolFn>,
  toolDocs: string,
  client: Anthropic
): Promise<string> {
  const context: Anthropic.Messages.MessageParam[] = [];
  let ultimoContenido = "";

  for (let ronda = 0; ronda < MAX_REPLANS; ronda++) {
    // 1. PLANNER
    const msgs: Anthropic.Messages.MessageParam[] = [
      ...context,
      { role: "user", content: tarea },
    ];
    const respPlanner = await client.messages.create({
      model: MODEL,
      max_tokens: 600,
      system: PLANNER_SYSTEM(toolDocs),
      messages: msgs,
    });
    const planTexto =
      respPlanner.content[0].type === "text" ? respPlanner.content[0].text : "";

    console.log(`\n[ronda ${ronda + 1}] Plan generado:`);
    console.log(planTexto.trim().slice(0, 300));

    const tareas = parsearPlan(planTexto);
    validarPlan(tareas);

    // 2. TASK FETCHING UNIT
    console.log("\nEjecutando DAG:");
    const resultados = await ejecutarDag(tareas, tools);

    // 3. JOINER
    const tao =
      planTexto +
      "\n\nResultados:\n" +
      [...resultados.entries()]
        .sort(([a], [b]) => a - b)
        .map(([idx, res]) => `T${idx}: ${res}`)
        .join("\n");

    const respJoiner = await client.messages.create({
      model: MODEL,
      max_tokens: 300,
      messages: [{ role: "user", content: JOINER_PROMPT(tao) }],
    });
    const joinerTexto =
      respJoiner.content[0].type === "text"
        ? respJoiner.content[0].text.trim()
        : "";
    const [accion, contenido] = parsearJoiner(joinerTexto);
    ultimoContenido = contenido;

    console.log(`\nJoiner: ${accion} → ${contenido.slice(0, 80)}`);

    if (accion === "Finish") return contenido;

    context.push(
      { role: "assistant", content: planTexto },
      { role: "user", content: tao }
    );
  }

  return ultimoContenido;
}

// Demo
(async () => {
  const client = new Anthropic();

  function calcular(expresion: string): string {
    try {
      const expr = expresion.replace(/\$\d+/g, "0").trim();
      // eslint-disable-next-line no-new-func
      return String(new Function(`return ${expr}`)());
    } catch (e) {
      return `Error: ${e}`;
    }
  }

  const tools: Record<string, ToolFn> = { calcular };
  const toolDocs =
    "calcular(expresion): evalúa una expresión matemática y devuelve el resultado numérico.";

  const tarea =
    "Calcula el área de un rectángulo de 15×8 metros y el área de un " +
    "círculo de radio 5 metros (π≈3.14159). ¿Cuál es mayor y por cuánto?";

  console.log(`Tarea: ${tarea}`);
  const resultado = await llmCompiler(tarea, tools, toolDocs, client);
  console.log(`\n=== Respuesta final ===\n${resultado}`);
})();
