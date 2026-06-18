/**
 * Mini-proyecto: El simulador de ventana de contexto (TypeScript).
 *
 * Uso:
 *   npx ts-node mini-simulador-ventana.ts
 *   npx ts-node mini-simulador-ventana.ts --ventana 4096
 *   npx ts-node mini-simulador-ventana.ts --turnos 30 --tecnica clearing
 *   npx ts-node mini-simulador-ventana.ts --tecnica todos
 */

// Snapshot precios mayo 2026 — verificar en docs del proveedor

// Cómo ejecutar: make ts SCRIPT=typescript/07-estado-contexto/mini-simulador-ventana.ts

const PRECIO_SONNET_INPUT = 3.00; // USD / millón tokens
const VENTANA_DEFAULT = 8_192;

type Mensaje = {
  role: string;
  content: string | Array<Record<string, unknown>>;
};

function estimarTokens(texto: string): number {
  return Math.max(1, Math.floor(texto.length / 4));
}

function tokensMensaje(msg: Mensaje): number {
  let texto = "";
  if (typeof msg.content === "string") {
    texto = msg.content;
  } else if (Array.isArray(msg.content)) {
    texto = msg.content
      .map((b) => (typeof b === "object" ? JSON.stringify(b) : String(b)))
      .join(" ");
  }
  return estimarTokens(texto) + 4;
}

function tokensHistorial(mensajes: Mensaje[]): number {
  return mensajes.reduce((acc, m) => acc + tokensMensaje(m), 0);
}

// ── generador de conversación ficticia ───────────────────────────────────────

const TIPOS_TURNO: [string, number][] = [
  ["user_simple", 30],
  ["user_largo", 120],
  ["assistant_texto", 80],
  ["tool_call", 40],
  ["tool_result_corto", 60],
  ["tool_result_largo", 400],
];

const PALABRAS = ["agente", "contexto", "herramienta", "respuesta", "análisis",
  "código", "función", "resultado", "iteración", "plan",
  "decisión", "estado", "memoria", "búsqueda", "resumen"];

let seed = 42;
function rand(): number {
  seed = (seed * 1664525 + 1013904223) & 0xffffffff;
  return (seed >>> 0) / 0xffffffff;
}
function randInt(min: number, max: number): number {
  return Math.floor(rand() * (max - min + 1)) + min;
}
function randChoice<T>(arr: T[]): T {
  return arr[Math.floor(rand() * arr.length)];
}
function weightedChoice(tipos: string[], pesos: number[]): string {
  const total = pesos.reduce((a, b) => a + b, 0);
  let r = rand() * total;
  for (let i = 0; i < tipos.length; i++) {
    r -= pesos[i];
    if (r <= 0) return tipos[i];
  }
  return tipos[tipos.length - 1];
}

function lorem(nTokens: number): string {
  const palabras: string[] = [];
  while (palabras.join(" ").length / 4 < nTokens) {
    palabras.push(randChoice(PALABRAS));
  }
  return palabras.join(" ");
}

function generarTurno(tipo: string): Mensaje {
  if (tipo === "user_simple") return { role: "user", content: lorem(30) };
  if (tipo === "user_largo") return { role: "user", content: lorem(120) };
  if (tipo === "assistant_texto") return { role: "assistant", content: lorem(80) };
  if (tipo === "tool_call") {
    return {
      role: "assistant",
      content: [{ type: "tool_use", id: `t${randInt(1000, 9999)}`,
        name: "search_docs", input: { query: lorem(10) } }],
    };
  }
  if (tipo === "tool_result_corto") {
    return {
      role: "user",
      content: [{ type: "tool_result", tool_use_id: `t${randInt(1000, 9999)}`,
        content: lorem(60) }],
    };
  }
  if (tipo === "tool_result_largo") {
    return {
      role: "user",
      content: [{ type: "tool_result", tool_use_id: `t${randInt(1000, 9999)}`,
        content: lorem(400) }],
    };
  }
  return { role: "user", content: lorem(30) };
}

function generarHistorial(nTurnos: number): Mensaje[] {
  seed = 42;
  const tipos = TIPOS_TURNO.map(([t]) => t);
  const pesos = TIPOS_TURNO.map(([, p]) => p);
  return Array.from({ length: nTurnos }, () => generarTurno(weightedChoice(tipos, pesos)));
}

// ── técnicas de compactación ──────────────────────────────────────────────────

function esToolResult(msg: Mensaje): boolean {
  if (!Array.isArray(msg.content)) return false;
  return msg.content.some((b) => typeof b === "object" && (b as Record<string,unknown>).type === "tool_result");
}

function clearing(mensajes: Mensaje[]): Mensaje[] {
  return mensajes.map((msg) => {
    if (!esToolResult(msg)) return msg;
    const nuevo = { ...msg, content: (msg.content as Array<Record<string,unknown>>).map((b) => {
      if (typeof b === "object" && b.type === "tool_result") {
        return { ...b, content: "[cleared]" };
      }
      return b;
    })};
    return nuevo;
  });
}

function headTail(mensajes: Mensaje[], maxTokens: number): Mensaje[] {
  if (tokensHistorial(mensajes) <= maxTokens) return mensajes;
  const head: Mensaje[] = [];
  const tail: Mensaje[] = [];
  let presupuesto = maxTokens;
  for (const msg of mensajes) {
    const tok = tokensMensaje(msg);
    if (presupuesto >= tok) { head.push(msg); presupuesto -= tok; }
    else break;
  }
  for (let i = mensajes.length - 1; i >= head.length; i--) {
    const tok = tokensMensaje(mensajes[i]);
    if (presupuesto >= tok) { tail.unshift(mensajes[i]); presupuesto -= tok; }
    else break;
  }
  const omitidos = mensajes.length - head.length - tail.length;
  const sep: Mensaje = { role: "user", content: `[... ${omitidos} mensajes omitidos ...]` };
  return [...head, sep, ...tail];
}

function sumarizacionSimulada(mensajes: Mensaje[], maxTokens: number): Mensaje[] {
  if (tokensHistorial(mensajes) <= maxTokens) return mensajes;
  const nConservar = Math.max(2, Math.floor(mensajes.length / 3));
  const aResumir = mensajes.slice(0, mensajes.length - nConservar);
  const recientes = mensajes.slice(mensajes.length - nConservar);
  const tokResumidos = tokensHistorial(aResumir);
  const resumenTok = Math.max(20, Math.floor(tokResumidos / 5));
  const resumen: Mensaje = {
    role: "user",
    content: `[RESUMEN de ${aResumir.length} mensajes / ~${tokResumidos} tokens → ${resumenTok} tokens]: ${lorem(resumenTok)}`,
  };
  return [resumen, ...recientes];
}

// ── simulación ────────────────────────────────────────────────────────────────

type Resultado = {
  tecnica: string;
  tokensEnviados: number;
  compactaciones: number;
  tokensAhorrados: number;
  desbordamientos: number;
  tokensFinal: number;
  costoUsd: number;
};

function simular(historialOriginal: Mensaje[], tecnica: string, ventana: number): Resultado {
  const umbral = Math.floor(ventana * 0.85);
  let historial: Mensaje[] = [];
  let compactaciones = 0;
  let tokensEnviados = 0;
  let tokensAhorrados = 0;
  let desbordamientos = 0;

  for (const msg of historialOriginal) {
    historial.push(msg);
    const tokActual = tokensHistorial(historial);

    if (tokActual > umbral) {
      const tokAntes = tokActual;
      if (tecnica === "clearing") historial = clearing(historial);
      else if (tecnica === "head_tail") historial = headTail(historial, umbral);
      else if (tecnica === "sumarizacion") historial = sumarizacionSimulada(historial, umbral);
      // "ninguna" — no hace nada
      compactaciones++;
      tokensAhorrados += Math.max(0, tokAntes - tokensHistorial(historial));
    }

    const tokFinal = tokensHistorial(historial);
    tokensEnviados += tokFinal;
    if (tokFinal > ventana) desbordamientos++;
  }

  return {
    tecnica,
    tokensEnviados,
    compactaciones,
    tokensAhorrados,
    desbordamientos,
    tokensFinal: tokensHistorial(historial),
    costoUsd: tokensEnviados * PRECIO_SONNET_INPUT / 1_000_000,
  };
}

// ── presentación ──────────────────────────────────────────────────────────────

function barra(valor: number, maximo: number, ancho = 30): string {
  const lleno = maximo ? Math.round((valor / maximo) * ancho) : 0;
  return "█".repeat(lleno) + "░".repeat(ancho - lleno);
}

function imprimirResultados(resultados: Resultado[], nTurnos: number, ventana: number): void {
  console.log(`\n${"=".repeat(66)}`);
  console.log(`  SIMULADOR DE VENTANA DE CONTEXTO`);
  console.log(`  ${nTurnos} turnos  |  ventana ${ventana.toLocaleString()} tokens  |  precios sonnet mayo 2026`);
  console.log("=".repeat(66));

  const maxTok = Math.max(...resultados.map((r) => r.tokensEnviados));
  const maxCost = Math.max(...resultados.map((r) => r.costoUsd));

  console.log(`\n${"Técnica".padEnd(16)} ${"Tokens env.".padStart(11)} ${"Compact.".padStart(9)} ${"Ahorr. tok".padStart(11)} ${"Desbord.".padStart(9)} ${"USD".padStart(8)}`);
  console.log("-".repeat(66));
  for (const r of resultados) {
    console.log(
      `${r.tecnica.padEnd(16)} ${r.tokensEnviados.toLocaleString().padStart(11)} ${r.compactaciones.toString().padStart(9)} ` +
      `${r.tokensAhorrados.toLocaleString().padStart(11)} ${r.desbordamientos.toString().padStart(9)} $${r.costoUsd.toFixed(4).padStart(7)}`
    );
  }

  console.log(`\n${"─".repeat(66)}`);
  console.log("  Tokens enviados (barra relativa al máximo)");
  console.log("─".repeat(66));
  for (const r of resultados) {
    console.log(`  ${r.tecnica.padEnd(14)} ${barra(r.tokensEnviados, maxTok)}  ${r.tokensEnviados.toLocaleString()}`);
  }

  console.log(`\n${"─".repeat(66)}`);
  console.log("  Costo USD (barra relativa al máximo)");
  console.log("─".repeat(66));
  const base = resultados.find((r) => r.tecnica === "ninguna");
  for (const r of resultados) {
    let ahorroStr = "";
    if (base && r.tecnica !== "ninguna") {
      const ahorro = (1 - r.costoUsd / base.costoUsd) * 100;
      ahorroStr = `  (${ahorro >= 0 ? "+" : ""}${ahorro.toFixed(1)}% vs sin compactación)`;
    }
    console.log(`  ${r.tecnica.padEnd(14)} ${barra(r.costoUsd, maxCost)}  $${r.costoUsd.toFixed(4)}${ahorroStr}`);
  }

  console.log(`\n[Estimación ±10% — conteo exacto con tiktoken (Python)]`);
  console.log(`[Snapshot precios mayo 2026 — verificar en docs del proveedor]`);
}

// ── main ──────────────────────────────────────────────────────────────────────

function main(): void {
  const args = process.argv.slice(2);
  const getArg = (flag: string, def: string) => {
    const i = args.indexOf(flag);
    return i >= 0 && args[i + 1] ? args[i + 1] : def;
  };

  const ventana = parseInt(getArg("--ventana", String(VENTANA_DEFAULT)));
  const turnos = parseInt(getArg("--turnos", "40"));
  const tecnicaArg = getArg("--tecnica", "todos");

  const historial = generarHistorial(turnos);
  const tokensBruto = tokensHistorial(historial);

  console.log(`\n[Historial generado: ${turnos} turnos, ${tokensBruto.toLocaleString()} tokens bruto]`);
  console.log(`[Ventana configurada: ${ventana.toLocaleString()} tokens  |  umbral: ${Math.floor(ventana * 0.85).toLocaleString()}]`);

  const tecnicas = tecnicaArg === "todos"
    ? ["ninguna", "clearing", "head_tail", "sumarizacion"]
    : [tecnicaArg];

  const resultados = tecnicas.map((t) => simular([...historial], t, ventana));
  imprimirResultados(resultados, turnos, ventana);
}

main();
