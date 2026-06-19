// Mini-proyecto: El debate de 3.
//
// Tres agentes con perspectivas diferentes debaten una propuesta técnica.
// Al final, un juez sintetiza el consenso. Observa cómo los agentes
// construyen sobre los argumentos de los otros y cuándo convergen.
//
// Cómo ejecutar:
//   export ANTHROPIC_API_KEY=sk-ant-...
//   make ts SCRIPT=typescript/12-multi-agente/mini-debate-3.ts
//
// Variables de entorno:
//   MODEL   — modelo a usar (default: claude-haiku-4-5-20251001)
//   RONDAS  — rondas de debate (default: 2)
//   PROPUESTA — propuesta técnica a debatir

import Anthropic from "@anthropic-ai/sdk";

const MODEL    = process.env.MODEL    ?? "claude-haiku-4-5-20251001";
const RONDAS   = parseInt(process.env.RONDAS ?? "2", 10);
const PROPUESTA = process.env.PROPUESTA ??
  "Migrar el backend de Python a TypeScript para mejorar la mantenibilidad del equipo";

const client = new Anthropic();

// ── Roles ──────────────────────────────────────────────────────────────────

const ROLES: Record<string, { system: string; emoji: string }> = {
  optimista: {
    emoji: "🟢",
    system:
      "Eres el Arquitecto Optimista en un panel técnico. Tu rol es defender los beneficios " +
      "de la propuesta presentada. Argumentas con datos concretos, ejemplos reales y ROI. " +
      "Sé conciso (3-4 frases). Puedes responder a objeciones específicas de los otros panelistas.",
  },
  escéptico: {
    emoji: "🔴",
    system:
      "Eres el Ingeniero Escéptico en un panel técnico. Tu rol es identificar riesgos, " +
      "costos ocultos y casos donde la propuesta podría fallar. " +
      "Sé conciso (3-4 frases). No rechaces la propuesta en su totalidad — señala condiciones " +
      "bajo las cuales sí tendría sentido.",
  },
  pragmatico: {
    emoji: "🟡",
    system:
      "Eres el Lead Engineer Pragmático en un panel técnico. Tu rol es evaluar la propuesta " +
      "desde la perspectiva de implementación real: timeline, recursos, migration path. " +
      "Propones variantes o fases que reduzcan el riesgo. Sé conciso (3-4 frases).",
  },
};

// ── Turnos ─────────────────────────────────────────────────────────────────

interface Entrada { rol: string; argumento: string; ronda: number }

async function turnoAgente(
  rol: string,
  propuesta: string,
  historial: Entrada[],
): Promise<string> {
  const { system } = ROLES[rol];
  let contexto = `Propuesta: ${propuesta}\n\n`;
  if (historial.length > 0) {
    contexto += "Debate hasta ahora:\n";
    for (const e of historial.slice(-6)) {
      contexto += `${e.rol.toUpperCase()}: ${e.argumento}\n`;
    }
  }
  contexto += `\nTu turno como ${rol.toUpperCase()}:`;

  const resp = await client.messages.create({
    model: MODEL,
    max_tokens: 256,
    system,
    messages: [{ role: "user", content: contexto }],
  });
  return (resp.content[0] as { type: "text"; text: string }).text.trim();
}

async function sintetizarDebate(propuesta: string, historial: Entrada[]): Promise<string> {
  const systemJuez =
    "Eres el Juez Sintetizador de un debate técnico. Analiza todos los argumentos presentados " +
    "y produce una síntesis equilibrada: qué aspectos de la propuesta tienen mérito, " +
    "qué riesgos son legítimos y una recomendación final con condiciones claras. " +
    "Sé objetivo y basa la recomendación en los argumentos más sólidos del debate.";
  const resumen = historial.map(e => `${e.rol.toUpperCase()}: ${e.argumento}`).join("\n");
  const resp = await client.messages.create({
    model: MODEL,
    max_tokens: 512,
    system: systemJuez,
    messages: [{ role: "user", content: `Propuesta: ${propuesta}\n\nDebate:\n${resumen}` }],
  });
  return (resp.content[0] as { type: "text"; text: string }).text.trim();
}

// ── Main ───────────────────────────────────────────────────────────────────

async function main(): Promise<void> {
  console.log(`\n${"=".repeat(64)}`);
  console.log("  DEBATE DE 3 AGENTES");
  console.log(`  Modelo: ${MODEL}  |  Rondas: ${RONDAS}`);
  console.log(`${"=".repeat(64)}`);
  console.log(`\n  Propuesta: ${PROPUESTA}`);

  const historial: Entrada[] = [];
  const ordenRoles = ["optimista", "escéptico", "pragmatico"];

  for (let ronda = 1; ronda <= RONDAS; ronda++) {
    console.log(`\n  ── Ronda ${ronda} ──`);
    for (const rol of ordenRoles) {
      const { emoji } = ROLES[rol];
      console.log(`\n  ${emoji} ${rol.toUpperCase()}:`);
      const argumento = await turnoAgente(rol, PROPUESTA, historial);
      console.log(`  ${argumento}`);
      historial.push({ rol, argumento, ronda });
    }
  }

  console.log(`\n${"─".repeat(64)}`);
  console.log("  ⚖️  SÍNTESIS DEL JUEZ:");
  const sintesis = await sintetizarDebate(PROPUESTA, historial);
  console.log(`\n  ${sintesis}`);

  console.log(`\n${"=".repeat(64)}`);
  console.log("  Estadísticas del debate:");
  console.log(`  • ${historial.length} intervenciones (${RONDAS} rondas × 3 agentes)`);
  console.log(`  • Tokens estimados: ~${historial.length * 300} (sin contar síntesis)`);
  console.log("  • Patrón: perspectivas divergentes → síntesis convergente");
  console.log(`${"=".repeat(64)}`);
}

main().catch(console.error);
