/**
 * Mini-proyecto: El simulador de incidentes de producción (TypeScript).
 *
 * Uso:
 *   npx ts-node mini-incident-sim.ts
 *   npx ts-node mini-incident-sim.ts --fallo timeout
 *   npx ts-node mini-incident-sim.ts --fallo todos
 */

interface Resultado {
  exito: boolean;
  intentos: number;
  duracionMs: number;
  error: string;
  estrategia: string;
  detalles: string[];
}

// ── circuit breaker ────────────────────────────────────────────────────────────

// Cómo ejecutar: make ts SCRIPT=typescript/17-produccion/mini-incident-sim.ts


class CircuitBreaker {
  private fallos = 0;
  private estado: "closed" | "open" | "half-open" = "closed";
  private ultimoFallo = 0;

  constructor(readonly nombre: string, readonly umbralFallos = 3, readonly timeoutS = 30) {}

  puedesPasar(): boolean {
    if (this.estado === "closed") return true;
    if (this.estado === "open") {
      if (Date.now() / 1000 - this.ultimoFallo > this.timeoutS) { this.estado = "half-open"; return true; }
      return false;
    }
    return true;
  }

  registrarExito(): void { this.fallos = 0; this.estado = "closed"; }

  registrarFallo(): void {
    this.fallos++;
    this.ultimoFallo = Date.now() / 1000;
    if (this.fallos >= this.umbralFallos) this.estado = "open";
  }

  get estadoActual(): string { return this.estado; }
  get fallosAcumulados(): number { return this.fallos; }
  set fallosExterno(n: number) { this.fallos = n; if (n >= this.umbralFallos) this.estado = "open"; }
}

// ── generador aleatorio simple ────────────────────────────────────────────────

let seed = 42;
function rng(): number { seed = (seed * 1664525 + 1013904223) >>> 0; return seed / 0xffffffff; }

// ── estrategias ───────────────────────────────────────────────────────────────

function jitter(baseMs: number, intento: number): number {
  const espera = baseMs * Math.pow(2, intento);
  return Math.max(100, espera + espera * 0.25 * (rng() * 2 - 1));
}

function recuperarTimeout(maxIntentos = 3): Resultado {
  const detalles: string[] = [];
  for (let i = 0; i < maxIntentos; i++) {
    const espera = jitter(500, i);
    detalles.push(`  Intento ${i + 1}: espera ${espera.toFixed(0)}ms antes de reintentar`);
    if (rng() > 0.4) {
      detalles.push(`  ✓ Llamada LLM completada en intento ${i + 1}`);
      return { exito: true, intentos: i + 1, duracionMs: 1200 + i * 800, error: "", estrategia: "retry_con_jitter", detalles };
    }
  }
  detalles.push("  ✗ Todos los reintentos agotados");
  return { exito: false, intentos: maxIntentos, duracionMs: 5000, error: "LLM timeout tras 3 reintentos", estrategia: "retry_con_jitter", detalles };
}

function recuperarOutputMalformado(): Resultado {
  const detalles = [
    "  Output recibido: '{\"hallazgos\": [broken json...'",
    "  Detección: JSONParseError",
    "  Estrategia: feedback al modelo con el error exacto",
    "  Prompt: 'Tu respuesta anterior no era JSON válido. Error: JSONParseError. Responde SOLO con JSON...'",
  ];
  if (rng() > 0.2) {
    detalles.push("  ✓ Segundo intento produjo JSON válido");
    return { exito: true, intentos: 2, duracionMs: 1400, error: "", estrategia: "feedback_al_modelo", detalles };
  }
  detalles.push("  ✗ Segundo intento también malformado");
  return { exito: false, intentos: 2, duracionMs: 1800, error: "Output malformado en 2 intentos", estrategia: "feedback_al_modelo", detalles };
}

function recuperarContextOverflow(tokensActuales: number, ventana: number): Resultado {
  const usoPct = tokensActuales / ventana * 100;
  const detalles = [`  Contexto: ${tokensActuales.toLocaleString()} tokens (${usoPct.toFixed(1)}% de ${ventana.toLocaleString()})`];
  if (usoPct > 75) {
    const objetivo = Math.floor(ventana * 0.6);
    const liberados = tokensActuales - objetivo;
    detalles.push(`  Umbral 75% superado — clearing de tool results`);
    detalles.push(`  Tokens liberados: ~${liberados.toLocaleString()}`);
    detalles.push(`  Contexto resultante: ${objetivo.toLocaleString()} (${(objetivo / ventana * 100).toFixed(1)}%)`);
    return { exito: true, intentos: 1, duracionMs: 50, error: "", estrategia: "compresion_proactiva_75pct", detalles };
  }
  detalles.push("  Dentro de límites — no requiere compresión.");
  return { exito: true, intentos: 1, duracionMs: 10, error: "", estrategia: "ninguna", detalles };
}

function recuperarToolFallo(cb: CircuitBreaker): Resultado {
  const detalles = [`  Circuit breaker '${cb.nombre}': estado=${cb.estadoActual}, fallos=${cb.fallosAcumulados}`];
  if (!cb.puedesPasar()) {
    detalles.push("  ✗ Circuit breaker ABIERTO — herramienta no disponible");
    detalles.push("  Fallback: usando caché o resultado por defecto");
    return { exito: false, intentos: 0, duracionMs: 5, error: `Circuit breaker abierto para '${cb.nombre}'`, estrategia: "circuit_breaker_fallback", detalles };
  }
  detalles.push("  Circuit breaker cerrado — intentando llamada");
  if (rng() > 0.5) {
    cb.registrarExito();
    detalles.push(`  ✓ Herramienta '${cb.nombre}' respondió correctamente`);
    return { exito: true, intentos: 1, duracionMs: 200, error: "", estrategia: "circuit_breaker_normal", detalles };
  }
  cb.registrarFallo();
  detalles.push(`  ✗ Fallo — acumulados: ${cb.fallosAcumulados}/${cb.umbralFallos}`);
  if (cb.estadoActual === "open") detalles.push(`  Circuit breaker ahora ABIERTO`);
  return { exito: false, intentos: 1, duracionMs: 5000, error: "Tool timeout", estrategia: "circuit_breaker_normal", detalles };
}

function recuperarBudgetExcedido(costoActual: number, budget: number): Resultado {
  const exceso = costoActual - budget;
  const detalles = [
    `  Costo acumulado: $${costoActual.toFixed(4)}`,
    `  Budget de tarea: $${budget.toFixed(4)}`,
    `  Exceso: $${exceso.toFixed(4)} (${(exceso / budget * 100).toFixed(0)}% sobre budget)`,
  ];
  if (costoActual > budget) {
    detalles.push("  Estrategia: degradación a modelo económico para pasos restantes");
    detalles.push("  Haiku ($0.80/Mtok) reemplaza a Sonnet ($3.00/Mtok) — 3.75× más barato");
    return { exito: true, intentos: 1, duracionMs: 0, error: "", estrategia: "model_downgrade_budget", detalles };
  }
  detalles.push("  Budget no excedido.");
  return { exito: true, intentos: 1, duracionMs: 0, error: "", estrategia: "ninguna", detalles };
}

// ── presentación ──────────────────────────────────────────────────────────────

function imprimirResultado(tipo: string, r: Resultado): void {
  console.log(`\n  ${"─".repeat(56)}`);
  console.log(`  Fallo: ${tipo.toUpperCase()}`);
  console.log(`  Estado: ${r.exito ? "✓ RECUPERADO" : "✗ FALLIDO"}  |  Estrategia: ${r.estrategia}`);
  console.log(`  Intentos: ${r.intentos}  |  Duración: ${r.duracionMs.toFixed(0)}ms`);
  if (r.error) console.log(`  Error final: ${r.error}`);
  console.log(`\n  Traza de recuperación:`);
  for (const d of r.detalles) console.log(`  ${d}`);
}

function imprimirResumen(resultados: Map<string, Resultado>): void {
  const recuperados = [...resultados.values()].filter((r) => r.exito).length;
  console.log(`\n${"=".repeat(60)}`);
  console.log("  RESUMEN — Simulador de Incidentes de Producción");
  console.log("=".repeat(60));
  console.log(`\n  ${recuperados}/${resultados.size} fallos recuperados automáticamente`);
  console.log(`\n  ${"Fallo".padEnd(22)} ${"Estado".padEnd(16)} Estrategia`);
  console.log("  " + "─".repeat(56));
  for (const [tipo, r] of resultados) {
    console.log(`  ${tipo.padEnd(22)} ${(r.exito ? "RECUPERADO" : "FALLIDO").padEnd(16)} ${r.estrategia}`);
  }
  console.log("\n  Lecciones clave:");
  console.log("  • Timeout: retry con jitter previene thundering herd");
  console.log("  • Output malformado: feedback exacto al modelo");
  console.log("  • Context overflow: compresión al 75% de uso, no al 100%");
  console.log("  • Tool fallo: circuit breaker evita cascada de timeouts");
  console.log("  • Budget excedido: degradar modelo antes de abortar");
  console.log("=".repeat(60));
}

// ── main ──────────────────────────────────────────────────────────────────────

function main(): void {
  const args = process.argv.slice(2);
  const fallo = args.includes("--fallo") ? args[args.indexOf("--fallo") + 1] : "todos";

  console.log(`\n${"=".repeat(60)}`);
  console.log("  SIMULADOR DE INCIDENTES DE PRODUCCIÓN");
  console.log(`  Fallo: ${fallo}`);
  console.log("=".repeat(60));

  const todos = ["timeout", "output_malformado", "context_overflow", "tool_fallo", "budget_excedido"];
  const fallos = fallo === "todos" ? todos : [fallo];

  const cb = new CircuitBreaker("search_docs", 3);
  (cb as any).fallosExterno = 3; // pre-carga 3 fallos para el escenario

  const resultados = new Map<string, Resultado>();
  for (const f of fallos) {
    let r: Resultado;
    if (f === "timeout") r = recuperarTimeout();
    else if (f === "output_malformado") r = recuperarOutputMalformado();
    else if (f === "context_overflow") r = recuperarContextOverflow(7200, 8192);
    else if (f === "tool_fallo") r = recuperarToolFallo(cb);
    else if (f === "budget_excedido") r = recuperarBudgetExcedido(0.082, 0.06);
    else continue;
    imprimirResultado(f, r);
    resultados.set(f, r);
  }

  if (resultados.size > 1) imprimirResumen(resultados);
}

main();
