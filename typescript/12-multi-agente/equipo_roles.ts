// Patrón Equipo de Roles (MetaGPT style): PM → Architect → ProjectManager → Engineer → QA
// Coordinados por un Message Pool con campo cause_by. Engineer tiene 3 reintentos.

// Cómo ejecutar: make ts SCRIPT=typescript/12-multi-agente/equipo_roles.ts

import Anthropic from "@anthropic-ai/sdk";

const client = new Anthropic();
const MODEL = process.env["MODEL"] ?? "claude-haiku-4-5-20251001";
const MAX_ENGINEER_RETRIES = 3;

// ---- Message Pool ----

interface Message {
  content: string;
  type: string;
  causeBy: string;
}

class MessagePool {
  private messages: Message[] = [];

  publish(content: string, type: string, causeBy: string): void {
    this.messages.push({ content, type, causeBy });
  }

  latest(type: string): Message | undefined {
    return [...this.messages].reverse().find(m => m.type === type);
  }

  summary(): string {
    return this.messages
      .map(m => `  [${m.causeBy}] ${m.type}: ${m.content.slice(0, 60)}...`)
      .join("\n");
  }
}

// ---- Roles ----

const ROLES = {
  pm: {
    id: "pm",
    systemPrompt:
      "Eres un Product Manager senior. A partir del requisito del usuario, " +
      "escribe un PRD claro y estructurado. Incluye: objetivo, funcionalidades, " +
      "criterios de aceptación y restricciones técnicas. Sé específico.",
  },
  architect: {
    id: "architect",
    systemPrompt:
      "Eres un Arquitecto de Software. A partir del PRD, diseña la arquitectura. " +
      "Incluye: componentes principales, interfaces, decisiones de diseño (con justificación) " +
      "y lista de archivos/módulos a crear.",
  },
  pm_mgr: {
    id: "pm_mgr",
    systemPrompt:
      "Eres un Project Manager técnico. A partir del System Design, " +
      "crea un plan de implementación serializable con tareas y dependencias explícitas. " +
      "El ingeniero ejecutará este plan directamente.",
  },
  engineer: {
    id: "engineer",
    systemPrompt:
      "Eres un Ingeniero de Software senior. A partir del plan, " +
      "escribe el código funcional completo. Incluye todos los archivos necesarios " +
      "con sintaxis correcta. El QA ejecutará tests — asegúrate de que sea ejecutable.",
  },
  qa: {
    id: "qa",
    systemPrompt:
      "Eres un QA Engineer. Revisa el código contra el PRD y el System Design. " +
      "Busca: bugs de lógica, casos borde no cubiertos, violaciones de requisitos. " +
      'Responde con JSON: {"tiene_bugs": true/false, "bugs": [...], "veredicto": "PASA" o "FALLA"}.',
  },
};

// ---- Helper ----

async function llamarLLM(system: string, user: string, temperature = 0.0): Promise<string> {
  const resp = await client.messages.create({
    model: MODEL,
    max_tokens: 1200,
    system,
    messages: [{ role: "user", content: user }],
    temperature,
  });
  return (resp.content[0] as { type: "text"; text: string }).text.trim();
}

function parsearTestReport(raw: string): { tiene_bugs: boolean; bugs: string[]; veredicto: string } {
  const inicio = raw.indexOf("{");
  const fin = raw.lastIndexOf("}") + 1;
  if (inicio === -1 || fin === 0) return { tiene_bugs: false, bugs: [], veredicto: "PASA" };
  try {
    return JSON.parse(raw.slice(inicio, fin));
  } catch {
    return { tiene_bugs: false, bugs: [], veredicto: "PASA" };
  }
}

// ---- Pipeline principal ----

async function equipoRoles(requisitoUsuario: string): Promise<string> {
  const pool = new MessagePool();

  // PM: genera PRD
  console.log("[PM] Generando PRD...");
  const prd = await llamarLLM(ROLES.pm.systemPrompt, `Requisito del usuario: ${requisitoUsuario}`);
  pool.publish(prd, "PRD", "pm");
  console.log(`  PRD: ${prd.slice(0, 80)}...`);

  // Architect: observa PM → SystemDesign
  console.log("\n[Architect] Diseñando sistema...");
  const prdMsg = pool.latest("PRD")!;
  const systemDesign = await llamarLLM(ROLES.architect.systemPrompt, `PRD:\n${prdMsg.content}`);
  pool.publish(systemDesign, "SystemDesign", "architect");
  console.log(`  SystemDesign: ${systemDesign.slice(0, 80)}...`);

  // PM_Manager: observa Architect → Plan
  console.log("\n[ProjectManager] Creando plan...");
  const archMsg = pool.latest("SystemDesign")!;
  const plan = await llamarLLM(
    ROLES.pm_mgr.systemPrompt,
    `PRD:\n${prdMsg.content}\n\nSystem Design:\n${archMsg.content}`
  );
  pool.publish(plan, "Plan", "pm_mgr");
  console.log(`  Plan: ${plan.slice(0, 80)}...`);

  // Engineer: observa PM_Manager → Code
  console.log("\n[Engineer] Escribiendo código...");
  const planMsg = pool.latest("Plan")!;
  const contextoEng = `PRD:\n${prdMsg.content}\n\nSystem Design:\n${archMsg.content}\n\nPlan:\n${planMsg.content}`;
  let codigo = await llamarLLM(ROLES.engineer.systemPrompt, contextoEng, 0.2);
  pool.publish(codigo, "Code", "engineer");
  console.log(`  Code: ${codigo.slice(0, 80)}...`);

  // QA: revisión inicial
  console.log("\n[QA] Revisando código...");
  const codeMsg = pool.latest("Code")!;
  const qaContexto = `PRD:\n${prdMsg.content}\n\nSystem Design:\n${archMsg.content}\n\nCódigo:\n${codeMsg.content}`;
  let testReportRaw = await llamarLLM(ROLES.qa.systemPrompt, qaContexto);
  pool.publish(testReportRaw, "TestReport", "qa");
  let reporte = parsearTestReport(testReportRaw);
  console.log(`  Veredicto inicial: ${reporte.veredicto}`);

  // Bucle Engineer ↔ QA: hasta MAX_ENGINEER_RETRIES
  let intento = 0;
  while (reporte.tiene_bugs && intento < MAX_ENGINEER_RETRIES) {
    intento++;
    const bugsTexto = reporte.bugs.map(b => `- ${b}`).join("\n");
    console.log(`\n[Engineer] Intento ${intento}/${MAX_ENGINEER_RETRIES} — corrigiendo bugs:\n  ${bugsTexto.slice(0, 120)}`);

    const codigoActual = pool.latest("Code") ?? pool.latest("BugFix");
    const fix = await llamarLLM(
      ROLES.engineer.systemPrompt,
      `QA encontró bugs:\n${bugsTexto}\n\nCódigo actual:\n${codigoActual?.content}\n\nPRD:\n${prdMsg.content}\n\nCorrige todos los bugs.`,
      0.2
    );
    pool.publish(fix, "BugFix", "engineer");

    console.log(`\n[QA] Re-revisando (intento ${intento})...`);
    testReportRaw = await llamarLLM(
      ROLES.qa.systemPrompt,
      `PRD:\n${prdMsg.content}\n\nSystem Design:\n${archMsg.content}\n\nCódigo corregido:\n${fix}`
    );
    pool.publish(testReportRaw, "TestReport", "qa");
    reporte = parsearTestReport(testReportRaw);
    console.log(`  Veredicto intento ${intento}: ${reporte.veredicto}`);
  }

  const codigoFinal = pool.latest("BugFix") ?? pool.latest("Code");
  const estado = !reporte.tiene_bugs ? "VALIDADO" : "MEJOR_INTENTO";
  console.log(`\n[Pipeline] Estado final: ${estado}`);
  console.log(`\n--- Pool de mensajes ---\n${pool.summary()}`);

  return codigoFinal?.content ?? "[Sin código generado]";
}

async function main() {
  const requisito =
    "Implementa una función Python que calcule el número de Fibonacci de forma eficiente " +
    "usando memoización. Debe manejar n=0, n=1, y números grandes. " +
    "Incluye una función main() con ejemplos de uso.";
  console.log(`Requisito: ${requisito}\n${"=".repeat(60)}\n`);
  const resultado = await equipoRoles(requisito);
  console.log(`\n${"=".repeat(60)}\nCódigo final:\n${resultado}`);
}

main().catch(console.error);
