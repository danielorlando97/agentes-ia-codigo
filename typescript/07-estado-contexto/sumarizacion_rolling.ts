// Cómo ejecutar: make ts SCRIPT=typescript/07-estado-contexto/sumarizacion_rolling.ts
import Anthropic from "@anthropic-ai/sdk";

function estimateTokens(messages: object[]): number {
  return messages.reduce((sum, m) => sum + JSON.stringify(m).length, 0) / 4;
}

function validarParidad(messages: object[]): string[] {
  const uses = new Set<string>();
  const results = new Set<string>();
  for (const msg of messages as Record<string, unknown>[]) {
    const content = msg["content"];
    if (!Array.isArray(content)) continue;
    for (const block of content) {
      if (typeof block !== "object" || block === null) continue;
      const b = block as Record<string, unknown>;
      if (b["type"] === "tool_use" && typeof b["id"] === "string") {
        uses.add(b["id"]);
      } else if (b["type"] === "tool_result" && typeof b["tool_use_id"] === "string") {
        results.add(b["tool_use_id"]);
      }
    }
  }
  const orphans: string[] = [];
  for (const id of uses) if (!results.has(id)) orphans.push(id);
  for (const id of results) if (!uses.has(id)) orphans.push(id);
  return orphans;
}

interface SummarizationConfig {
  head: number;
  tail: number;
  maxTokens: number;
  threshold: number;
  model: string;
  summaryMaxTokens: number;
}

const defaultConfig: SummarizationConfig = {
  head:             2,
  tail:             6,
  maxTokens:        110_000,
  threshold:        0.75,
  model:            process.env["SMALL_MODEL"] ?? "claude-haiku-4-5-20251001",
  summaryMaxTokens: 1_500,
};

class SummarizationBuffer {
  private client: Anthropic;
  private cfg: SummarizationConfig;
  private _messages: object[];
  compactionCount: number;

  constructor(client: Anthropic, config?: Partial<SummarizationConfig>) {
    this.client          = client;
    this.cfg             = { ...defaultConfig, ...config };
    this._messages       = [];
    this.compactionCount = 0;
  }

  add(message: object): void {
    this._messages.push(message);
  }

  get(): object[] {
    return [...this._messages];
  }

  get tokens(): number {
    return estimateTokens(this._messages);
  }

  get length(): number {
    return this._messages.length;
  }

  private shouldSummarize(): boolean {
    const trigger = Math.floor(this.cfg.maxTokens * this.cfg.threshold);
    return this.tokens > trigger;
  }

  private buildCompactionPrompt(messages: object[]): string {
    return (
      "Resume este historial de un agente. Preserva exactamente:\n" +
      "- Cada herramienta llamada, sus parámetros y su resultado (números, IDs, rutas)\n" +
      "- Cada decisión tomada y su justificación\n" +
      "- Restricciones y constraints del usuario\n" +
      "- El estado actual de la tarea y el progreso\n" +
      "No parafrasees valores numéricos ni identificadores — cópialos literalmente.\n\n" +
      `Historial: ${JSON.stringify(messages).slice(0, 14_000)}`
    );
  }

  async compact(): Promise<boolean> {
    if (!this.shouldSummarize()) return false;

    const msgs = this._messages;
    if (msgs.length <= this.cfg.head + this.cfg.tail) return false;

    const head   = msgs.slice(0, this.cfg.head);
    const tail   = msgs.slice(msgs.length - this.cfg.tail);
    const middle = msgs.slice(this.cfg.head, msgs.length - this.cfg.tail);

    if (middle.length === 0) return false;

    const response = await this.client.messages.create({
      model:      this.cfg.model,
      max_tokens: this.cfg.summaryMaxTokens,
      messages:   [{ role: "user", content: this.buildCompactionPrompt(middle) }],
    });

    const summary    = (response.content[0] as { text: string }).text;
    const compressed = { role: "user", content: `[HISTORIAL COMPRIMIDO]\n${summary}` };

    this._messages = [...head, compressed, ...tail];
    this.compactionCount += 1;

    const orphans = validarParidad([...head, ...tail]);
    if (orphans.length > 0) {
      console.log(`  [aviso] paridad en boundary: ${orphans}`);
    }

    return true;
  }
}

async function main() {
  const client = new Anthropic();

  const cfg: Partial<SummarizationConfig> = {
    head:             1,
    tail:             2,
    maxTokens:        3_000,
    threshold:        0.6,
    summaryMaxTokens: 300,
  };
  const buf = new SummarizationBuffer(client, cfg);

  buf.add({ role: "user", content: "Analiza el repo y encuentra bugs de seguridad." });
  for (let i = 0; i < 10; i++) {
    buf.add({ role: "assistant", content: `Analicé auth_${i}.py: sin vulnerabilidades evidentes.` });
    buf.add({ role: "user",      content: `Continúa con el módulo ${i + 1}.` });
  }

  console.log(`Antes de compact: ${buf.length} msgs, ~${buf.tokens} tokens`);
  const compactado = await buf.compact();
  console.log(`Compactó: ${compactado} | Tras compact: ${buf.length} msgs, ~${buf.tokens} tokens`);
  console.log(`Compacciones totales: ${buf.compactionCount}`);

  if (compactado) {
    console.log(`\nMensaje comprimido (primeros 200 chars):`);
    const content = (buf.get()[1] as Record<string, unknown>)["content"] as string;
    console.log(`  ${content.slice(0, 200)}`);
  }
}

main();
