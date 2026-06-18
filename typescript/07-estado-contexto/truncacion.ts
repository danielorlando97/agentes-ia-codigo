// Cómo ejecutar: make ts SCRIPT=typescript/07-estado-contexto/truncacion.ts
type Message = Record<string, unknown>;

function estimateTokens(messages: Message[]): number {
  return messages.reduce((sum, m) => sum + JSON.stringify(m).length, 0) / 4;
}

function validarParidad(messages: Message[]): string[] {
  const uses    = new Set<string>();
  const results = new Set<string>();

  for (const msg of messages) {
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
  for (const id of uses)    if (!results.has(id)) orphans.push(id);
  for (const id of results) if (!uses.has(id))    orphans.push(id);
  return orphans;
}

function hasToolUse(msg: Message): boolean {
  const content = msg["content"];
  if (!Array.isArray(content)) return false;
  return content.some(
    (b) => typeof b === "object" && b !== null && (b as Record<string, unknown>)["type"] === "tool_use"
  );
}

function truncarFifo(messages: Message[], maxTokens: number): Message[] {
  const working = [...messages];
  while (estimateTokens(working) > maxTokens && working.length > 1) {
    const removed = working.shift()!;
    if (
      working.length > 0 &&
      removed["role"] === "assistant" &&
      hasToolUse(removed) &&
      working[0]["role"] === "user"
    ) {
      working.shift();
    }
  }
  return working;
}

function truncarHeadTail(
  messages: Message[],
  maxTokens: number,
  head = 2,
  tail = 6
): Message[] {
  if (messages.length <= head + tail) return messages;

  let result = [...messages.slice(0, head), ...messages.slice(messages.length - tail)];
  if (estimateTokens(result) > maxTokens) {
    result = truncarFifo(result, maxTokens);
  }
  return result;
}

function limpiarToolResults(messages: Message[], minAge = 4): Message[] {
  let toolResultCount = 0;
  const resultMsgs: Message[] = [];

  for (let i = messages.length - 1; i >= 0; i--) {
    const msg     = messages[i];
    const content = msg["content"];

    if (!Array.isArray(content)) {
      resultMsgs.unshift(msg);
      continue;
    }

    const newBlocks = content.map((block) => {
      if (
        typeof block === "object" &&
        block !== null &&
        (block as Record<string, unknown>)["type"] === "tool_result"
      ) {
        toolResultCount += 1;
        if (toolResultCount > minAge) {
          return { ...(block as object), content: [{ type: "text", text: "[cleared]" }] };
        }
      }
      return block;
    });

    resultMsgs.unshift({ ...msg, content: newBlocks });
  }

  return resultMsgs;
}

class AlmacenHistorial {
  maxTokens: number;
  private _messages: Message[];

  constructor(maxTokens = 110_000) {
    this.maxTokens = maxTokens;
    this._messages = [];
  }

  add(message: Message): void {
    this._messages.push(message);
  }

  get(): Message[] {
    return [...this._messages];
  }

  get tokens(): number {
    return estimateTokens(this._messages);
  }

  get length(): number {
    return this._messages.length;
  }

  applyFifo(): void {
    this._messages = truncarFifo(this._messages, this.maxTokens);
  }

  applyHeadTail(head = 2, tail = 6): void {
    this._messages = truncarHeadTail(this._messages, this.maxTokens, head, tail);
  }

  clearToolResults(minAge = 4): void {
    this._messages = limpiarToolResults(this._messages, minAge);
  }

  checkParity(): string[] {
    return validarParidad(this._messages);
  }
}

const historial = new AlmacenHistorial(2_000);

historial.add({ role: "user", content: "Analiza el repo." });
for (let i = 0; i < 8; i++) {
  historial.add({
    role:    "assistant",
    content: [{ type: "tool_use", id: `tu_${String(i).padStart(2, "0")}`, name: "read_file", input: { path: `file_${i}.py` } }],
  });
  historial.add({
    role:    "user",
    content: [{ type: "tool_result", tool_use_id: `tu_${String(i).padStart(2, "0")}`, content: [{ type: "text", text: "x".repeat(200) }] }],
  });
}

console.log(`Antes: ${historial.length} msgs, ~${historial.tokens} tokens`);
console.log(`Paridad: ${historial.checkParity().length === 0 ? "OK" : historial.checkParity()}`);

historial.clearToolResults(4);
console.log(`\nTras clearToolResults(minAge=4): ~${historial.tokens} tokens`);
console.log(`Paridad tras limpiar: ${historial.checkParity().length === 0 ? "OK" : historial.checkParity()}`);

historial.applyHeadTail(1, 4);
console.log(`\nTras head_tail(1,4): ${historial.length} msgs, ~${historial.tokens} tokens`);
console.log(`Paridad final: ${historial.checkParity().length === 0 ? "OK" : historial.checkParity()}`);
