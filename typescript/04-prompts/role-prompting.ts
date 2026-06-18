// Comparación de outputs con: sin rol, rol corto (~20 tokens), rol largo (~150 tokens).
//
// Demuestra:
// - Cómo el rol afecta el formato, nivel de detalle y tokens usados
// - Tarea: categorizar tickets de soporte por prioridad
// - Variante 1: sin rol (solo instrucción directa)
// - Variante 2: rol corto — "Eres un agente de soporte técnico."
// - Variante 3: rol largo — persona detallada con experiencia, estilo y valores

// Cómo ejecutar: make ts SCRIPT=typescript/04-prompts/role-prompting.ts

import Anthropic from "@anthropic-ai/sdk";

const MODEL = process.env["MODEL"] ?? "claude-sonnet-4-6";

// ─── 1. Tickets de prueba ─────────────────────────────────────────────────────

interface Ticket {
  id: string;
  text: string;
  expectedPriority: string;
}

const TICKETS: Ticket[] = [
  {
    id: "T-001",
    text: "Nuestro sistema de pagos dejó de funcionar hace 10 minutos. No podemos procesar ninguna transacción. Perdemos dinero cada segundo.",
    expectedPriority: "urgente",
  },
  {
    id: "T-002",
    text: "El botón de exportar a CSV en el módulo de reportes no funciona. Aparece un error 500. Lo necesitamos para el informe mensual del lunes.",
    expectedPriority: "alta",
  },
  {
    id: "T-003",
    text: "¿Podrían añadir un modo oscuro a la interfaz? Sería más cómodo para trabajar de noche.",
    expectedPriority: "baja",
  },
  {
    id: "T-004",
    text: "Necesito cambiar el correo de mi cuenta. He seguido los pasos de la documentación pero el botón de confirmar no aparece.",
    expectedPriority: "media",
  },
  {
    id: "T-005",
    text: "La aplicación móvil se cierra inesperadamente al intentar adjuntar archivos de más de 10 MB. Ocurre en iOS 17 y Android 14. Varios clientes nos han reportado esto.",
    expectedPriority: "alta",
  },
];

// ─── 2. System prompts por variante ──────────────────────────────────────────

const SYSTEM_NO_ROLE = `Categoriza tickets de soporte técnico por prioridad.

Las prioridades posibles son:
- urgente: el sistema está caído o hay pérdida económica inmediata
- alta: funcionalidad crítica bloqueada, afecta a múltiples usuarios
- media: funcionalidad degradada, hay workaround disponible
- baja: mejoras, preguntas o problemas menores sin impacto operativo

Responde con el siguiente formato JSON exacto:
{"prioridad": "<urgente|alta|media|baja>", "razon": "<una oración breve>"}`;

const SYSTEM_SHORT_ROLE = `Eres un agente de soporte técnico.

Categoriza tickets por prioridad.

Las prioridades posibles son:
- urgente: el sistema está caído o hay pérdida económica inmediata
- alta: funcionalidad crítica bloqueada, afecta a múltiples usuarios
- media: funcionalidad degradada, hay workaround disponible
- baja: mejoras, preguntas o problemas menores sin impacto operativo

Responde con el siguiente formato JSON exacto:
{"prioridad": "<urgente|alta|media|baja>", "razon": "<una oración breve>"}`;

const SYSTEM_LONG_ROLE = `Eres Elena Martínez, ingeniera de soporte técnico senior con 8 años de experiencia en plataformas SaaS B2B. Especialista en triaging de incidencias críticas, llevas el registro de tiempo de resolución más bajo del equipo. Tu filosofía: priorizar con precisión quirúrgica porque una mala priorización ralentiza todo el equipo. Eres directa, metódica y nunca escatimas en claridad al justificar una decisión. Conoces de memoria las SLAs del equipo: urgente=1h, alta=4h, media=24h, baja=72h.

Categoriza tickets por prioridad siguiendo los criterios SLA:
- urgente: el sistema está caído o hay pérdida económica inmediata (SLA: 1h)
- alta: funcionalidad crítica bloqueada, afecta a múltiples usuarios (SLA: 4h)
- media: funcionalidad degradada, hay workaround disponible (SLA: 24h)
- baja: mejoras, preguntas o problemas menores sin impacto operativo (SLA: 72h)

Responde con el siguiente formato JSON exacto:
{"prioridad": "<urgente|alta|media|baja>", "razon": "<una oración breve>"}`;

// ─── 3. Clasificación ────────────────────────────────────────────────────────

interface ClassResult {
  priority: string;
  reason: string;
  rawOutput: string;
  tokensInput: number;
  tokensOutput: number;
}

async function classifyTicket(
  client: Anthropic,
  system: string,
  ticketText: string
): Promise<ClassResult> {
  const response = await client.messages.create({
    model: MODEL,
    max_tokens: 200,
    system,
    messages: [{ role: "user", content: `Ticket: ${ticketText}` }],
  });

  const output = (response.content[0] as Anthropic.TextBlock).text.trim();
  const match = output.match(/\{.*\}/s);
  let priority = "unknown";
  let reason = output;

  if (match) {
    try {
      const data = JSON.parse(match[0]);
      priority = data.prioridad || "unknown";
      reason = data.razon || "";
    } catch {
      // parse error — usar output completo
    }
  }

  return {
    priority,
    reason,
    rawOutput: output,
    tokensInput: response.usage.input_tokens,
    tokensOutput: response.usage.output_tokens,
  };
}

// ─── 4. Análisis de estilo ────────────────────────────────────────────────────

interface StyleMarkers {
  mentionsSla: boolean;
  mentionsImpact: boolean;
  lengthChars: number;
}

function detectStyleMarkers(output: string): StyleMarkers {
  const lower = output.toLowerCase();
  return {
    mentionsSla: /sla|hora|horas|plazo/.test(lower),
    mentionsImpact: /usuario|cliente|pérdida|impacto/.test(lower),
    lengthChars: output.length,
  };
}

// ─── 5. Impresión de resultados ───────────────────────────────────────────────

function printTicketComparison(
  ticket: Ticket,
  results: [string, ClassResult][]
): void {
  console.log(`\n${"═".repeat(72)}`);
  console.log(`  TICKET ${ticket.id}: ${ticket.text.slice(0, 70)}...`);
  console.log(`  Prioridad esperada: ${ticket.expectedPriority}`);
  console.log(`${"─".repeat(72)}`);

  for (const [name, r] of results) {
    const correct = r.priority === ticket.expectedPriority ? "✓" : "✗";
    const markers = detectStyleMarkers(r.rawOutput);
    console.log(`\n  [${name}] Prioridad: ${r.priority} ${correct}`);
    console.log(`  Razón: ${r.reason}`);
    console.log(`  Tokens: ${r.tokensInput} input / ${r.tokensOutput} output`);
    console.log(
      `  Estilo → SLA: ${markers.mentionsSla}, Impacto: ${markers.mentionsImpact}, Longitud: ${markers.lengthChars} chars`
    );
  }
}

function printSummary(
  allResults: [string, ClassResult][][],
  tickets: Ticket[]
): void {
  const variantNames = allResults[0].map(([name]) => name);
  const stats: Record<string, { correct: number; tokensIn: number; tokensOut: number; withSla: number }> = {};
  for (const v of variantNames) {
    stats[v] = { correct: 0, tokensIn: 0, tokensOut: 0, withSla: 0 };
  }

  for (let i = 0; i < allResults.length; i++) {
    const ticket = tickets[i];
    for (const [name, r] of allResults[i]) {
      if (r.priority === ticket.expectedPriority) stats[name].correct++;
      stats[name].tokensIn += r.tokensInput;
      stats[name].tokensOut += r.tokensOutput;
      if (detectStyleMarkers(r.rawOutput).mentionsSla) stats[name].withSla++;
    }
  }

  const n = tickets.length;
  console.log(`\n${"═".repeat(72)}`);
  console.log("  TABLA COMPARATIVA FINAL");
  console.log(`${"═".repeat(72)}`);
  console.log(
    `  ${"Variante".padEnd(28)} ${"Accuracy".padStart(10)} ${"Tokens/in".padStart(10)} ${"Tokens/out".padStart(12)} ${"Menciona SLA".padStart(14)}`
  );
  console.log(`  ${"-".repeat(72)}`);
  for (const [v, s] of Object.entries(stats)) {
    console.log(
      `  ${v.padEnd(28)} ${(s.correct / n * 100).toFixed(0).padStart(9)}% ` +
      `${(s.tokensIn / n).toFixed(0).padStart(9)} ` +
      `${(s.tokensOut / n).toFixed(0).padStart(11)} ` +
      `${(s.withSla / n * 100).toFixed(0).padStart(13)}%`
    );
  }
  console.log(`\n  El rol largo añade tokens pero puede cambiar el lenguaje de la razón.`);
  console.log(`  Alta accuracy con rol corto = el rol no añade valor semántico al resultado.`);
}

// ─── 6. Main ──────────────────────────────────────────────────────────────────

async function main(): Promise<void> {
  const client = new Anthropic();

  const variants: [string, string][] = [
    ["Sin rol", SYSTEM_NO_ROLE],
    ["Rol corto", SYSTEM_SHORT_ROLE],
    ["Rol largo", SYSTEM_LONG_ROLE],
  ];

  const allResults: [string, ClassResult][][] = [];

  for (const ticket of TICKETS) {
    const ticketResults: [string, ClassResult][] = [];
    for (const [name, system] of variants) {
      const result = await classifyTicket(client, system, ticket.text);
      ticketResults.push([name, result]);
    }
    printTicketComparison(ticket, ticketResults);
    allResults.push(ticketResults);
  }

  printSummary(allResults, TICKETS);
}

main().catch(console.error);
