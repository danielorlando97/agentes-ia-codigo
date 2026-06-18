// Ensamblador de contexto con presupuesto explícito por región.
// - ContextBudget: modelo de presupuesto con 5 regiones
// - Umbral configurable que activa la evicción antes del límite
// - clipText para recortar system prompt y memoria recuperada

// Cómo ejecutar: make ts SCRIPT=typescript/06-memoria/02-corto-plazo/nivel-3-produccion.ts


interface Mensaje {
  role: string;
  content: string;
  pinned?: boolean;
}

interface ContextBudgetOptions {
  total?: number;
  system?: number;
  retrieved?: number;
  tools?: number;
  response?: number;
  threshold?: number;
}

class ContextBudget {
  total: number;
  system: number;
  retrieved: number;
  tools: number;
  response: number;
  threshold: number;

  constructor(opts: ContextBudgetOptions = {}) {
    this.total = opts.total ?? 128_000;
    this.system = opts.system ?? 4_000;
    this.retrieved = opts.retrieved ?? 3_000;
    this.tools = opts.tools ?? 2_000;
    this.response = opts.response ?? 8_000;
    this.threshold = opts.threshold ?? 0.75;
  }

  get history(): number {
    return this.total - this.system - this.retrieved - this.tools - this.response;
  }

  get compactTrigger(): number {
    return Math.floor(this.history * this.threshold);
  }
}

function estimateTokens(obj: unknown): number {
  return Math.floor(JSON.stringify(obj).length / 4);
}

function clipText(text: string, maxTokens: number): string {
  const maxChars = maxTokens * 4;
  return text.length > maxChars ? text.slice(0, maxChars) : text;
}

function reduceHistory(messages: Mensaje[], budget: number): Mensaje[] {
  const working = [...messages];
  while (estimateTokens(working) > budget) {
    const idx = working.findIndex((m) => !m.pinned);
    if (idx === -1) break;
    working.splice(idx, 1);
  }
  return working;
}

interface ContextResult {
  system: string;
  retrieved: string;
  tools: unknown[];
  messages: Mensaje[];
}

function buildContext(
  history: Mensaje[],
  systemPrompt: string,
  retrieved: string = "",
  tools: unknown[] = [],
  budget?: ContextBudget,
): ContextResult {
  const b = budget ?? new ContextBudget();

  const historyTokens = estimateTokens(history);
  if (historyTokens > b.compactTrigger) {
    console.log(
      `[contexto] historial=${historyTokens}t > threshold=${b.compactTrigger}t → reduciendo`,
    );
    history = reduceHistory(history, b.history);
    console.log(`[contexto] historial reducido a ~${estimateTokens(history)}t`);
  }

  return {
    system: clipText(systemPrompt, b.system),
    retrieved: clipText(retrieved, b.retrieved),
    tools,
    messages: history,
  };
}

const budget = new ContextBudget({ total: 10_000, system: 500, retrieved: 300, tools: 200, response: 500 });

const history: Mensaje[] = [
  { role: "user", content: "Analiza este repositorio.", pinned: true },
  ...Array.from({ length: 24 }, (_, i) => ({
    role: i % 2 === 0 ? "user" : "assistant",
    content: "contenido: " + "x".repeat(300),
  })),
];

const ctx = buildContext(
  history,
  "Eres un asistente de análisis de código experto en Python.",
  "Sesión anterior: el usuario analizó auth.py y encontró un bug en validate_token().",
  [],
  budget,
);

console.log(`Historial final: ${ctx.messages.length} mensajes`);
console.log(`Anclado preservado: '${ctx.messages[0].content}'`);
console.log(`System clipeado a: ${ctx.system.length} chars`);
