// Ventana deslizante por conteo de turns.
// Invariante: mantiene los últimos maxTurns mensajes,
// preservando siempre el primero (ancla de la tarea).

// Cómo ejecutar: make ts SCRIPT=typescript/06-memoria/02-corto-plazo/nivel-1-minimo.ts


interface Mensaje {
  role: string;
  content: string;
}

function buildContext(messages: Mensaje[], maxTurns: number = 20): Mensaje[] {
  if (messages.length <= maxTurns) {
    return messages;
  }
  return [messages[0], ...messages.slice(-(maxTurns - 1))];
}

const msgs: Mensaje[] = Array.from({ length: 40 }, (_, i) => ({
  role: i % 2 === 0 ? "user" : "assistant",
  content: `mensaje ${i}`,
}));

const result = buildContext(msgs, 10);
console.log(`Entrada: ${msgs.length} mensajes`);
console.log(`Salida:  ${result.length} mensajes`);
console.log(`Primero: ${result[0].content}`);
console.log(`Último:  ${result[result.length - 1].content}`);
