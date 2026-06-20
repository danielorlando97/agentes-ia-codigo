// Sumarización lazy del historial conversacional.
// Comprime el intermedio cuando supera el umbral de tokens.
// Preserva cabeza (primeros 2) + cola (últimos 6 turnos) intactos.
//
// Cómo ejecutar: make ts SCRIPT=typescript/06-memoria/10-tecnicas/02-sumarizacion.ts

const UMBRAL_TOKENS = 2_000;
const TURNOS_PRESERVAR = 6;
const CABEZA_PRESERVAR = 2;

interface Mensaje {
  role: string;
  type?: string;
  id?: string;
  tool_use_id?: string;
  name?: string;
  content?: string;
}

function estimarTokens(mensajes: Mensaje[]): number {
  return mensajes.reduce((sum, m) => sum + Math.floor(JSON.stringify(m).length / 4), 0);
}

function resumirMock(mensajes: Mensaje[]): string {
  const herramientas = [...new Set(
    mensajes.filter((m) => m.type === "tool_use").map((m) => m.name ?? "?")
  )];
  const nErrores = mensajes.filter((m) =>
    String(m.content ?? "").toLowerCase().includes("error")
  ).length;
  let resumen = `[${mensajes.length} turnos comprimidos] `;
  if (herramientas.length) resumen += `Herramientas: ${herramientas.join(", ")}. `;
  if (nErrores) resumen += `${nErrores} errores encontrados. `;
  resumen += "El agente continuó investigando y encontró información relevante.";
  return resumen;
}

function sanitizarPares(mensajes: Mensaje[]): Mensaje[] {
  const resultIds = new Set(
    mensajes.filter((m) => m.type === "tool_result").map((m) => m.tool_use_id!)
  );
  return mensajes.filter(
    (m) => !(m.type === "tool_use" && !resultIds.has(m.id!))
  );
}

function buildContext(mensajes: Mensaje[], umbral = UMBRAL_TOKENS): Mensaje[] {
  if (estimarTokens(mensajes) <= umbral) return mensajes;

  const cabeza = mensajes.slice(0, CABEZA_PRESERVAR);
  const cola = mensajes.slice(-TURNOS_PRESERVAR);
  const middle = mensajes.slice(CABEZA_PRESERVAR, -TURNOS_PRESERVAR);

  if (!middle.length) return mensajes;

  const middleLimpio = sanitizarPares(middle);
  const resumen = resumirMock(middleLimpio);

  const bloqueResumen: Mensaje = {
    role: "user",
    content:
      `[HISTORIAL COMPRIMIDO — ${middleLimpio.length} turnos]\n` +
      resumen +
      "\n[FIN COMPRIMIDO]",
  };

  return [...cabeza, bloqueResumen, ...cola];
}

// ── Demo ──────────────────────────────────────────────────────────────────

function simularHistorial(n: number): Mensaje[] {
  const msgs: Mensaje[] = [];
  for (let i = 0; i < n; i++) {
    if (i === 0) {
      msgs.push({ role: "user", content: "Analiza el repositorio completo y encuentra el bug." });
    } else if (i % 4 === 1) {
      msgs.push({ role: "assistant", type: "tool_use", id: `tool_${i}`, name: "read_file", content: undefined });
    } else if (i % 4 === 2) {
      msgs.push({
        role: "user",
        type: "tool_result",
        tool_use_id: `tool_${i - 1}`,
        content: `Contenido del archivo ${Math.floor(i / 4)}: ${"código ".repeat(15)}`,
      });
    } else {
      msgs.push({ role: "assistant", content: `Análisis parcial #${Math.floor(i / 4)}: continuando.` });
    }
  }
  return msgs;
}

const historial = simularHistorial(30);
const tokensOriginal = estimarTokens(historial);
console.log(`Historial original: ${historial.length} mensajes, ~${tokensOriginal} tokens`);

const contexto = buildContext(historial);
const tokensComprimido = estimarTokens(contexto);
console.log(`Contexto comprimido: ${contexto.length} mensajes, ~${tokensComprimido} tokens`);
console.log(`Reducción: ${Math.round(100 * (1 - tokensComprimido / tokensOriginal))}%`);
console.log();

for (const m of contexto) {
  const tipo = m.type ?? m.role;
  const contenido = String(m.content ?? m.name ?? "").slice(0, 80);
  console.log(`  [${tipo}] ${contenido}`);
}
