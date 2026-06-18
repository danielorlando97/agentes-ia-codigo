// Cómo ejecutar: make ts SCRIPT=typescript/14-observabilidad/mini-postmortem.ts
/**
 * Mini-proyecto: El post-mortem automatizado (TypeScript).
 *
 * Uso:
 *   npx ts-node mini-postmortem.ts
 *   npx ts-node mini-postmortem.ts --incidente latencia
 *   npx ts-node mini-postmortem.ts --incidente todos
 */

type SpanTipo = "llm_call" | "tool_call";
type Severidad = "info" | "warning" | "critical";
type FinishReason = "end_turn" | "max_tokens" | "tool_use";

interface Span {
  spanId: string;
  tipo: SpanTipo;
  nombre: string;
  duracionMs: number;
  tokensInput: number;
  tokensOutput: number;
  finishReason: FinishReason;
  toolSuccess: boolean;
  turno: number;
}

interface Sesion {
  sessionId: string;
  turnos: number;
  spans: Span[];
  completada: boolean;
}

interface Hallazgo {
  tipo: string;
  severidad: Severidad;
  descripcion: string;
  metrica: string;
  umbral: string;
  valorObservado: string;
  sesionesAfectadas: number;
}

// ── generador ────────────────────────────────────────────────────────────────

let seed = 42;
function rng(): number {
  seed = (seed * 1664525 + 1013904223) >>> 0;
  return seed / 0xffffffff;
}
function rngRange(min: number, max: number): number { return min + rng() * (max - min); }
function rngInt(min: number, max: number): number { return Math.floor(rngRange(min, max + 1)); }

function generarSesionNormal(sessionId: string, n: number): Sesion {
  const turnos = rngInt(3, 8);
  const spans: Span[] = [];
  let k = 0;
  for (let t = 0; t < turnos; t++) {
    spans.push({ spanId: `span_${n * 100 + k}`, tipo: "llm_call", nombre: "claude",
      duracionMs: rngRange(800, 1800), tokensInput: rngInt(800, 2000),
      tokensOutput: rngInt(100, 600), finishReason: "end_turn", toolSuccess: true, turno: t });
    k++;
    if (rng() < 0.6) {
      spans.push({ spanId: `span_${n * 100 + k}`, tipo: "tool_call", nombre: "tool_call",
        duracionMs: rngRange(50, 300), tokensInput: 0, tokensOutput: 0,
        finishReason: "end_turn", toolSuccess: true, turno: t });
      k++;
    }
  }
  return { sessionId, turnos, spans, completada: true };
}

function generarHistorial(n: number, incidente: string): Sesion[] {
  seed = 42;
  return Array.from({ length: n }, (_, i) => {
    const s = generarSesionNormal(`sess_${i.toString().padStart(3, "0")}`, i);
    if ((incidente === "latencia" || incidente === "todos") && i >= n / 2) {
      s.spans.forEach((sp) => { if (sp.tipo === "llm_call" && rng() < 0.4) sp.duracionMs = rngRange(12000, 25000); });
    }
    if ((incidente === "costos" || incidente === "todos") && i >= n / 3) {
      s.spans.forEach((sp) => { if (sp.tipo === "llm_call") sp.tokensInput = rngInt(18000, 35000); });
    }
    if ((incidente === "loop_infinito" || incidente === "todos") && i >= (n * 2) / 3) {
      const base = s.turnos;
      for (let extra = 0; extra < 12; extra++) {
        s.spans.push({ spanId: `span_extra_${extra}`, tipo: "llm_call", nombre: "claude",
          duracionMs: rngRange(800, 1600), tokensInput: rngInt(5000, 8000), tokensOutput: 600,
          finishReason: "max_tokens", toolSuccess: true, turno: base + extra });
      }
      s.turnos += 12;
      s.completada = false;
    }
    if ((incidente === "tool_failures" || incidente === "todos") && i >= n / 4) {
      s.spans.forEach((sp) => { if (sp.tipo === "tool_call" && rng() < 0.7) sp.toolSuccess = false; });
    }
    return s;
  });
}

// ── análisis ──────────────────────────────────────────────────────────────────

function percentile(arr: number[], p: number): number {
  const sorted = [...arr].sort((a, b) => a - b);
  return sorted[Math.floor(sorted.length * p)] ?? 0;
}

function analizarLatencia(sesiones: Sesion[]): Hallazgo[] {
  const duraciones = sesiones.flatMap((s) => s.spans.filter((sp) => sp.tipo === "llm_call").map((sp) => sp.duracionMs));
  if (duraciones.length === 0) return [];
  const p95 = percentile(duraciones, 0.95);
  const media = duraciones.reduce((a, b) => a + b, 0) / duraciones.length;
  const hallazgos: Hallazgo[] = [];
  if (p95 > 8000) hallazgos.push({ tipo: "latencia_p95", severidad: p95 > 15000 ? "critical" : "warning",
    descripcion: "P95 de latencia LLM supera umbral operacional.", metrica: "llm_call.duration_ms p95",
    umbral: "< 5,000ms", valorObservado: `${p95.toFixed(0)}ms`,
    sesionesAfectadas: sesiones.filter((s) => s.spans.some((sp) => sp.tipo === "llm_call" && sp.duracionMs > 10000)).length });
  if (media > 5000) hallazgos.push({ tipo: "latencia_media", severidad: "warning",
    descripcion: "Latencia media LLM elevada.", metrica: "llm_call.duration_ms mean",
    umbral: "< 2,000ms", valorObservado: `${media.toFixed(0)}ms`, sesionesAfectadas: 0 });
  return hallazgos;
}

function analizarCostos(sesiones: Sesion[]): Hallazgo[] {
  const costos = sesiones.map((s) => {
    const ti = s.spans.filter((sp) => sp.tipo === "llm_call").reduce((a, sp) => a + sp.tokensInput, 0);
    const to = s.spans.filter((sp) => sp.tipo === "llm_call").reduce((a, sp) => a + sp.tokensOutput, 0);
    return (ti * 3.0 + to * 15.0) / 1_000_000;
  });
  const tokInputs = sesiones.flatMap((s) => s.spans.filter((sp) => sp.tipo === "llm_call").map((sp) => sp.tokensInput));
  const costoMedio = costos.reduce((a, b) => a + b, 0) / Math.max(costos.length, 1);
  const tokMedio = tokInputs.reduce((a, b) => a + b, 0) / Math.max(tokInputs.length, 1);
  const hallazgos: Hallazgo[] = [];
  if (costoMedio > 0.05) hallazgos.push({ tipo: "costo_por_sesion", severidad: costoMedio > 0.10 ? "critical" : "warning",
    descripcion: "Costo por sesión supera presupuesto.", metrica: "session.cost_usd mean",
    umbral: "< $0.05", valorObservado: `$${costoMedio.toFixed(4)}`,
    sesionesAfectadas: costos.filter((c) => c > 0.05).length });
  if (tokMedio > 10000) hallazgos.push({ tipo: "contexto_inflado", severidad: "warning",
    descripcion: "Tokens de input anormalmente alto — historial sin compactar.", metrica: "llm_call.tokens_input mean",
    umbral: "< 5,000", valorObservado: `${tokMedio.toFixed(0)}`, sesionesAfectadas: tokInputs.filter((t) => t > 10000).length });
  return hallazgos;
}

function analizarLoop(sesiones: Sesion[]): Hallazgo[] {
  const conMaxTokens = sesiones.filter((s) => s.spans.filter((sp) => sp.finishReason === "max_tokens").length > 3);
  const incompletas = sesiones.filter((s) => !s.completada);
  const turnosExcesivos = sesiones.filter((s) => s.turnos > 15);
  const hallazgos: Hallazgo[] = [];
  if (conMaxTokens.length > 0) hallazgos.push({ tipo: "loop_max_tokens", severidad: "critical",
    descripcion: "Múltiples max_tokens por sesión — probable loop sin condición de salida.", metrica: "finish_reason == max_tokens",
    umbral: "< 1/sesión", valorObservado: `${conMaxTokens.length} sesiones`, sesionesAfectadas: conMaxTokens.length });
  if (incompletas.length > 0) hallazgos.push({ tipo: "sesiones_incompletas", severidad: "critical",
    descripcion: "Sesiones no completadas.", metrica: "session.completada", umbral: "100%",
    valorObservado: `${incompletas.length}/${sesiones.length}`, sesionesAfectadas: incompletas.length });
  if (turnosExcesivos.length > 0) hallazgos.push({ tipo: "turnos_excesivos", severidad: "warning",
    descripcion: "Sesiones con turnos anormalmente altos.", metrica: "session.turnos",
    umbral: "< 15", valorObservado: `max=${Math.max(...turnosExcesivos.map((s) => s.turnos))}`, sesionesAfectadas: turnosExcesivos.length });
  return hallazgos;
}

function analizarTools(sesiones: Sesion[]): Hallazgo[] {
  const toolCalls = sesiones.flatMap((s) => s.spans.filter((sp) => sp.tipo === "tool_call"));
  if (toolCalls.length === 0) return [];
  const failures = toolCalls.filter((sp) => !sp.toolSuccess);
  const tasa = failures.length / toolCalls.length * 100;
  if (tasa <= 20) return [];
  return [{ tipo: "tool_failure_rate", severidad: tasa > 50 ? "critical" : "warning",
    descripcion: "Tasa de fallos de herramientas sobre umbral.", metrica: "tool_call.success_rate",
    umbral: "> 95%", valorObservado: `${(100 - tasa).toFixed(1)}% (${failures.length}/${toolCalls.length})`,
    sesionesAfectadas: sesiones.filter((s) => s.spans.some((sp) => sp.tipo === "tool_call" && !sp.toolSuccess)).length }];
}

// ── reporte ───────────────────────────────────────────────────────────────────

const ICONOS: Record<Severidad, string> = { info: "ℹ️ ", warning: "⚠️ ", critical: "🚨" };

function imprimirReporte(sesiones: Sesion[], hallazgos: Hallazgo[], incidente: string): void {
  const totalTokens = sesiones.flatMap((s) => s.spans).reduce((a, sp) => a + sp.tokensInput + sp.tokensOutput, 0);
  const totalCosto = sesiones.reduce((a, s) => {
    const ti = s.spans.filter((sp) => sp.tipo === "llm_call").reduce((x, sp) => x + sp.tokensInput, 0);
    const to = s.spans.filter((sp) => sp.tipo === "llm_call").reduce((x, sp) => x + sp.tokensOutput, 0);
    return a + (ti * 3.0 + to * 15.0) / 1_000_000;
  }, 0);

  console.log(`\n${"=".repeat(64)}`);
  console.log("  POST-MORTEM AUTOMATIZADO");
  console.log(`  Incidente: ${incidente}  |  ${sesiones.length} sesiones analizadas`);
  console.log("=".repeat(64));
  console.log(`\n  Tokens totales: ${totalTokens.toLocaleString()}`);
  console.log(`  Costo total: $${totalCosto.toFixed(4)}`);
  console.log(`  Sesiones incompletas: ${sesiones.filter((s) => !s.completada).length}`);

  const criticos = hallazgos.filter((h) => h.severidad === "critical");
  const warnings = hallazgos.filter((h) => h.severidad === "warning");
  console.log(`\n  Hallazgos: ${criticos.length} críticos, ${warnings.length} warnings`);
  console.log("  " + "─".repeat(56));

  if (hallazgos.length === 0) {
    console.log("  ✅ Sin anomalías detectadas.");
  } else {
    const ordenados = [...hallazgos].sort((a, b) => ({ critical: 0, warning: 1, info: 2 }[a.severidad] - { critical: 0, warning: 1, info: 2 }[b.severidad]));
    for (const h of ordenados) {
      console.log(`\n  ${ICONOS[h.severidad]} [${h.severidad.toUpperCase()}] ${h.tipo}`);
      console.log(`     ${h.descripcion}`);
      console.log(`     Umbral: ${h.umbral} | Observado: ${h.valorObservado} | Afectadas: ${h.sesionesAfectadas}/${sesiones.length}`);
    }
  }

  console.log(`\n${"=".repeat(64)}`);
  console.log("  Causa raíz probable:");
  const tipos = hallazgos.filter((h) => h.severidad === "critical").map((h) => h.tipo);
  if (tipos.includes("loop_max_tokens")) console.log("  → Loop sin condición de salida — agregar max_turns");
  if (tipos.includes("latencia_p95")) console.log("  → Picos de latencia — revisar timeouts y tamaño de request");
  if (tipos.includes("costo_por_sesion") || tipos.includes("contexto_inflado")) console.log("  → Historial inflado — aplicar clearing o sumarización");
  if (tipos.includes("tool_failure_rate")) console.log("  → Servicio externo inestable — revisar circuit breaker");
  if (tipos.length === 0) console.log("  → Sin anomalías críticas.");
  console.log("=".repeat(64));
}

// ── main ──────────────────────────────────────────────────────────────────────

function main(): void {
  const args = process.argv.slice(2);
  const getArg = (flag: string, def: string) => { const i = args.indexOf(flag); return i >= 0 && args[i + 1] ? args[i + 1] : def; };
  const incidente = getArg("--incidente", "todos");
  const nSesiones = parseInt(getArg("--sesiones", "20"));

  const sesiones = generarHistorial(nSesiones, incidente);
  const hallazgos = [...analizarLatencia(sesiones), ...analizarCostos(sesiones), ...analizarLoop(sesiones), ...analizarTools(sesiones)];
  imprimirReporte(sesiones, hallazgos, incidente);
}

main();
