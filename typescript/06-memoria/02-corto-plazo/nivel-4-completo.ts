// ContextManager con conteo exacto de tokens y compactación LLM como fallback.
// - countTokensExact: usa client.messages.countTokens() para precisión real
// - Compactación LLM: cuando FIFO no basta, un modelo barato resume el historial
// - ContextMetrics: fifoEvictions, llmCompactions, tokensSaved

// Cómo ejecutar: make ts SCRIPT=typescript/06-memoria/02-corto-plazo/nivel-4-completo.ts


import Anthropic from "@anthropic-ai/sdk";

const MODEL = process.env["MODEL"] ?? "claude-sonnet-4-6";
const COMPACT_MODEL = process.env["COMPACT_MODEL"] ?? "claude-haiku-4-5-20251001";

interface Mensaje {
  role: "user" | "assistant";
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

interface ContextMetrics {
  fifoEvictions: number;
  llmCompactions: number;
  tokensSaved: number;
}

class ContextManager {
  private client: Anthropic;
  private budget: ContextBudget;
  private systemPrompt: string;
  metrics: ContextMetrics;

  constructor(client: Anthropic, budget?: ContextBudget, systemPrompt: string = "") {
    this.client = client;
    this.budget = budget ?? new ContextBudget();
    this.systemPrompt = systemPrompt;
    this.metrics = { fifoEvictions: 0, llmCompactions: 0, tokensSaved: 0 };
  }

  async countTokensExact(messages: Mensaje[]): Promise<number> {
    try {
      const r = await this.client.messages.countTokens({
        model: MODEL,
        system: this.systemPrompt,
        messages: messages,
      });
      return r.input_tokens;
    } catch {
      return this.estimate(messages);
    }
  }

  private estimate(messages: Mensaje[]): number {
    return Math.floor(
      messages.reduce((acc, m) => acc + JSON.stringify(m).length, 0) / 4,
    );
  }

  private fifoReduce(messages: Mensaje[], budget: number): [Mensaje[], number] {
    const working = [...messages];
    let evicted = 0;
    while (this.estimate(working) > budget) {
      const idx = working.findIndex((m) => !m.pinned);
      if (idx === -1) break;
      working.splice(idx, 1);
      evicted++;
    }
    return [working, evicted];
  }

  private async llmCompact(messages: Mensaje[]): Promise<Mensaje[]> {
    if (messages.length <= 8) return messages;

    const head = messages.slice(0, 2);
    const tail = messages.slice(-6);
    const middle = messages.slice(2, -6);

    if (middle.length === 0) return messages;

    const tokensBefore = this.estimate(messages);
    console.log(`[compactación LLM] resumiendo ${middle.length} mensajes intermedios`);

    const response = await this.client.messages.create({
      model: COMPACT_MODEL,
      max_tokens: 1_500,
      messages: [
        {
          role: "user",
          content:
            "Resume este historial preservando exactamente: " +
            "cada herramienta llamada y su resultado, " +
            "cada decisión tomada y por qué, " +
            "el estado actual de la tarea.\n\n" +
            `Historial: ${JSON.stringify(middle).slice(0, 12_000)}`,
        },
      ],
    });

    const summary = (response.content[0] as { text: string }).text;
    const compressed: Mensaje = { role: "user", content: `[HISTORIAL COMPRIMIDO]\n${summary}` };
    const result = [...head, compressed, ...tail];

    const tokensAfter = this.estimate(result);
    this.metrics.llmCompactions++;
    this.metrics.tokensSaved += Math.max(0, tokensBefore - tokensAfter);
    console.log(`[compactación LLM] ~${tokensBefore}t → ~${tokensAfter}t`);
    return result;
  }

  async prepare(messages: Mensaje[]): Promise<Mensaje[]> {
    const current = await this.countTokensExact(messages);
    if (current <= this.budget.compactTrigger) return messages;

    console.log(`[contexto] ${current}t > threshold=${this.budget.compactTrigger}t`);

    const [reduced, evicted] = this.fifoReduce(messages, this.budget.history);
    this.metrics.fifoEvictions += evicted;

    if (this.estimate(reduced) <= this.budget.history) return reduced;

    return this.llmCompact(reduced);
  }

  report(): ContextMetrics {
    return { ...this.metrics };
  }
}

async function main() {
  const client = new Anthropic();
  const budget = new ContextBudget({
    total: 10_000,
    system: 500,
    retrieved: 300,
    tools: 200,
    response: 500,
  });
  const mgr = new ContextManager(client, budget, "Eres un asistente de código.");

  const history: Mensaje[] = [
    { role: "user", content: "Analiza este repositorio.", pinned: true },
    ...Array.from({ length: 29 }, (_, i) => ({
      role: (i % 2 === 0 ? "user" : "assistant") as "user" | "assistant",
      content: `Turno ${i}: ` + "resultado de análisis. ".repeat(40),
    })),
  ];

  const prepared = await mgr.prepare(history);
  console.log(`Historial final: ${prepared.length} mensajes`);
  console.log(`Métricas: ${JSON.stringify(mgr.report())}`);
}

main().catch(console.error);
