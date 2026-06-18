// Evicción FIFO por presupuesto de tokens con mensajes anclados.
// - Estimación de tokens (len(json) / 4, error ±15% texto, ±30% código)
// - Evicción por tokens en lugar de conteo de turns
// - Mensajes con pinned=true nunca se evictan

// Cómo ejecutar: make ts SCRIPT=typescript/06-memoria/02-corto-plazo/nivel-2-basico.ts


interface Mensaje {
  role: string;
  content: string;
  pinned?: boolean;
}

const HISTORY_BUDGET = 110_000;

function estimateTokens(messages: Mensaje[]): number {
  return messages.reduce((acc, m) => acc + JSON.stringify(m).length, 0) / 4;
}

function buildContext(messages: Mensaje[], budget: number = HISTORY_BUDGET): Mensaje[] {
  const working = [...messages];

  while (estimateTokens(working) > budget) {
    const idx = working.findIndex((m) => !m.pinned);
    if (idx === -1) break;
    working.splice(idx, 1);
  }

  return working;
}

const msgs: Mensaje[] = [{ role: "user", content: "Analiza este repositorio.", pinned: true }];
for (let i = 1; i < 20; i++) {
  msgs.push({
    role: i % 2 === 0 ? "user" : "assistant",
    content: "resultado de herramienta: " + "x".repeat(800),
  });
}

const budget = 4_000;
const result = buildContext(msgs, budget);

console.log(`Entrada: ${msgs.length} msgs, ~${Math.floor(estimateTokens(msgs))} tokens`);
console.log(`Salida:  ${result.length} msgs, ~${Math.floor(estimateTokens(result))} tokens (budget=${budget})`);
console.log(`Anclado preservado: ${result[0].pinned} → '${result[0].content.slice(0, 40)}'`);
