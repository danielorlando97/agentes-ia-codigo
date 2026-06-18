/**
 * Mini-proyecto: El checkpoint simulator (TypeScript).
 *
 * Uso:
 *   npx ts-node mini-checkpoint-sim.ts
 *   npx ts-node mini-checkpoint-sim.ts --auto
 *   npx ts-node mini-checkpoint-sim.ts --escenario destructivo --auto
 */

import * as readline from "readline";

// ── tipos ──────────────────────────────────────────────────────────────────────

// Cómo ejecutar: make ts SCRIPT=typescript/13-hitl/mini-checkpoint-sim.ts


type RiesgoNivel = "bajo" | "medio" | "alto" | "crítico";
type Decision = "pendiente" | "aprobado" | "rechazado" | "modificado";

interface Accion {
  nombre: string;
  descripcion: string;
  riesgo: RiesgoNivel;
  reversible: boolean;
  requiereHITL: boolean;
  payload: Record<string, unknown>;
}

interface Checkpoint {
  accion: Accion;
  contexto: string;
  decision: Decision;
  modificacion: Record<string, unknown>;
  latenciaS: number;
}

interface EstadoAgente {
  tarea: string;
  historial: string[];
  checkpoints: Checkpoint[];
  completado: boolean;
  abortado: boolean;
}

// ── escenarios ────────────────────────────────────────────────────────────────

const ACCIONES_SUAVES: Accion[] = [
  { nombre: "leer_archivos", descripcion: "Leer 47 archivos de configuración", riesgo: "bajo", reversible: true, requiereHITL: false, payload: { archivos: 47 } },
  { nombre: "analizar_dependencias", descripcion: "Ejecutar análisis estático de dependencias", riesgo: "bajo", reversible: true, requiereHITL: false, payload: { herramienta: "pip-audit" } },
  { nombre: "generar_reporte", descripcion: "Escribir reporte en reports/audit_2026.md", riesgo: "medio", reversible: true, requiereHITL: true, payload: { archivo: "reports/audit_2026.md", tamaño_kb: 12 } },
  { nombre: "enviar_notificacion", descripcion: "Enviar email al equipo de seguridad (3 destinatarios)", riesgo: "medio", reversible: false, requiereHITL: true, payload: { destinatarios: ["security@empresa.com"], asunto: "Audit 2026" } },
  { nombre: "cerrar_tarea", descripcion: "Marcar tarea como completada en el tracker", riesgo: "bajo", reversible: true, requiereHITL: false, payload: { ticket: "SEC-1247" } },
];

const ACCIONES_DESTRUCTIVAS: Accion[] = [
  { nombre: "listar_usuarios", descripcion: "Obtener usuarios inactivos hace >90 días", riesgo: "bajo", reversible: true, requiereHITL: false, payload: { filtro: "last_login < 90 días" } },
  { nombre: "revocar_tokens", descripcion: "Revocar tokens de 1,247 usuarios inactivos", riesgo: "alto", reversible: false, requiereHITL: true, payload: { usuarios: 1247, tokens: "API + OAuth" } },
  { nombre: "archivar_datos", descripcion: "Archivar datos en cold storage", riesgo: "alto", reversible: false, requiereHITL: true, payload: { gb: 23.4, destino: "s3://cold-archive/users/2026/" } },
  { nombre: "eliminar_cuentas", descripcion: "Eliminar definitivamente 1,247 cuentas de la BD", riesgo: "crítico", reversible: false, requiereHITL: true, payload: { usuarios: 1247, operacion: "DELETE FROM users WHERE ..." } },
  { nombre: "purgar_logs", descripcion: "Purgar logs de acceso de usuarios eliminados", riesgo: "alto", reversible: false, requiereHITL: true, payload: { registros: 89432, tabla: "access_logs" } },
];

type Escenario = { tarea: string; acciones: Accion[]; decisiones: Record<string, Decision> };

const ESCENARIOS: Record<string, Escenario> = {
  suave: {
    tarea: "Auditoría de seguridad y notificación al equipo",
    acciones: ACCIONES_SUAVES,
    decisiones: { generar_reporte: "aprobado", enviar_notificacion: "modificado" },
  },
  destructivo: {
    tarea: "Limpieza de usuarios inactivos en producción",
    acciones: ACCIONES_DESTRUCTIVAS,
    decisiones: { revocar_tokens: "aprobado", archivar_datos: "aprobado", eliminar_cuentas: "rechazado", purgar_logs: "rechazado" },
  },
};

// ── utils ─────────────────────────────────────────────────────────────────────

const ICONOS: Record<RiesgoNivel, string> = { bajo: "🟢", medio: "🟡", alto: "🔴", "crítico": "🚨" };

function mostrarCheckpoint(cp: Checkpoint): void {
  const a = cp.accion;
  console.log(`\n${"─".repeat(60)}`);
  console.log(`  CHECKPOINT — Aprobación requerida`);
  console.log("─".repeat(60));
  console.log(`  Acción:      ${a.nombre}`);
  console.log(`  Descripción: ${a.descripcion}`);
  console.log(`  Riesgo:      ${ICONOS[a.riesgo]} ${a.riesgo.toUpperCase()}`);
  console.log(`  Reversible:  ${a.reversible ? "Sí" : "NO — irreversible"}`);
  console.log(`  Contexto:    ${cp.contexto}`);
  console.log(`  Payload:     ${JSON.stringify(a.payload)}`);
  console.log("─".repeat(60));
}

async function preguntarUsuario(prompt: string): Promise<string> {
  const rl = readline.createInterface({ input: process.stdin, output: process.stdout });
  return new Promise((resolve) => rl.question(prompt, (ans) => { rl.close(); resolve(ans); }));
}

async function solicitarDecisionInteractiva(cp: Checkpoint): Promise<Decision> {
  mostrarCheckpoint(cp);
  console.log("\n  Opciones: [A] Aprobar   [R] Rechazar   [M] Modificar   [S] Escalar");
  while (true) {
    const resp = (await preguntarUsuario("\n  Tu decisión > ")).trim().toUpperCase();
    if (resp === "A") return "aprobado";
    if (resp === "R") return "rechazado";
    if (resp === "M") { console.log("  (Modificado → dry_run: true)"); return "modificado"; }
    if (resp === "S") { console.log("  [Escalado → aprobado con flag 'escalado']"); return "aprobado"; }
    console.log("  Opción no válida.");
  }
}

function solicitarDecisionAuto(cp: Checkpoint, decisiones: Record<string, Decision>): Decision {
  mostrarCheckpoint(cp);
  const d = decisiones[cp.accion.nombre] ?? "aprobado";
  console.log(`\n  [auto] Decisión automática: ${d.toUpperCase()}`);
  return d;
}

// ── simulación ────────────────────────────────────────────────────────────────

async function simularAgente(escenario: Escenario, auto: boolean): Promise<EstadoAgente> {
  const estado: EstadoAgente = {
    tarea: escenario.tarea,
    historial: [],
    checkpoints: [],
    completado: false,
    abortado: false,
  };

  const log = (msg: string) => { estado.historial.push(msg); console.log(`  [agente] ${msg}`); };
  log(`Iniciando tarea: ${escenario.tarea}`);

  for (const accion of escenario.acciones) {
    if (estado.abortado) break;
    log(`Preparando: ${accion.nombre}`);

    if (accion.requiereHITL) {
      const cp: Checkpoint = {
        accion,
        contexto: `El agente ha completado ${estado.historial.length} pasos.`,
        decision: "pendiente",
        modificacion: {},
        latenciaS: 0,
      };
      estado.checkpoints.push(cp);

      const t0 = Date.now();
      const decision = auto
        ? solicitarDecisionAuto(cp, escenario.decisiones)
        : await solicitarDecisionInteractiva(cp);
      cp.latenciaS = (Date.now() - t0) / 1000;
      cp.decision = decision;

      if (decision === "rechazado") {
        log(`✗ ${accion.nombre} RECHAZADO — abortando`);
        estado.abortado = true;
        break;
      }
      if (decision === "modificado") cp.modificacion = { dry_run: true };
    }

    const sufijo = accion.requiereHITL && estado.checkpoints.at(-1)?.decision === "modificado"
      ? " (dry-run)" : "";
    log(`✓ ${accion.nombre} ejecutado${sufijo}`);
  }

  if (!estado.abortado) { estado.completado = true; log("Tarea completada."); }
  return estado;
}

// ── reporte ───────────────────────────────────────────────────────────────────

function imprimirReporte(estado: EstadoAgente): void {
  console.log(`\n${"=".repeat(60)}`);
  console.log("  REPORTE FINAL — CHECKPOINT SIMULATOR");
  console.log("=".repeat(60));
  console.log(`\n  Tarea: ${estado.tarea}`);
  console.log(`  Estado: ${estado.completado ? "COMPLETADA" : "ABORTADA"}`);
  console.log(`  Pasos ejecutados: ${estado.historial.length}`);
  console.log(`\n  Checkpoints (${estado.checkpoints.length} total):`);
  console.log("  " + "─".repeat(56));

  let totalLatencia = 0;
  let aprobados = 0, rechazados = 0, modificados = 0;
  for (const cp of estado.checkpoints) {
    const icon = { aprobado: "✓", rechazado: "✗", modificado: "~", pendiente: "?" }[cp.decision] ?? "?";
    console.log(`  ${icon} ${cp.accion.nombre.padEnd(30)} [${cp.decision.padEnd(10)}]  ${ICONOS[cp.accion.riesgo]} ${cp.accion.riesgo}`);
    if (cp.latenciaS > 0) { console.log(`    Latencia: ${cp.latenciaS.toFixed(1)}s`); totalLatencia += cp.latenciaS; }
    if (cp.decision === "aprobado") aprobados++;
    else if (cp.decision === "rechazado") rechazados++;
    else if (cp.decision === "modificado") modificados++;
  }

  const total = estado.checkpoints.length;
  if (total > 0) {
    const tasa = ((aprobados + modificados) / total * 100).toFixed(0);
    console.log(`\n  Tasa de aprobación: ${tasa}% (${aprobados} aprobados, ${modificados} modificados, ${rechazados} rechazados)`);
    if (parseFloat(tasa) > 95) console.log("  ⚠️  Approval fatigue — revisar umbrales de riesgo");
    if (totalLatencia > 0) console.log(`  Latencia total HITL: ${totalLatencia.toFixed(1)}s`);
  }

  console.log(`\n${"=".repeat(60)}`);
  console.log("  Lecciones:");
  console.log("  • Los checkpoints bloquean — más checkpoints = mayor latencia");
  console.log("  • Un rechazo puede abortar todo el pipeline");
  console.log("  • 'Modificar' reduce abortos sin aprobar incondicionalmente");
  console.log("  • Tasa > 95% = umbrales mal calibrados");
  console.log("=".repeat(60));
}

// ── main ──────────────────────────────────────────────────────────────────────

async function main(): Promise<void> {
  const args = process.argv.slice(2);
  const auto = args.includes("--auto");
  const escenarioKey = args.includes("--escenario") ? args[args.indexOf("--escenario") + 1] : "suave";
  const escenario = ESCENARIOS[escenarioKey] ?? ESCENARIOS.suave;

  console.log(`\n${"=".repeat(60)}`);
  console.log("  CHECKPOINT SIMULATOR");
  console.log(`  Escenario: ${escenarioKey}  |  Modo: ${auto ? "automático" : "interactivo"}`);
  console.log("=".repeat(60));
  console.log(`\n  Tarea: ${escenario.tarea}`);
  console.log(`  Checkpoints HITL: ${escenario.acciones.filter((a) => a.requiereHITL).length}`);

  const estado = await simularAgente(escenario, auto);
  imprimirReporte(estado);
}

main().catch(console.error);
