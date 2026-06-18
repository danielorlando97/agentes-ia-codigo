// Versionado de prompts y A/B testing: registro inmutable, rollback, canary deployments

// Cómo ejecutar: make ts SCRIPT=typescript/17-produccion/versionado.ts

import Anthropic from "@anthropic-ai/sdk";
import crypto from "crypto";

const cliente = new Anthropic();

interface VersionPrompt {
  id: string;
  version: string;
  prompt: string;
  modelo: string;
  creado: Date;
  evaluacion: Record<string, unknown>;
  activo: boolean;
  canaryPeso: number;
}

const registro = new Map<string, VersionPrompt>();
const historialActivos: string[] = [];

function registrarPrompt(
  prompt: string,
  modelo: string,
  version: string,
  evaluacion: Record<string, unknown>
): string {
  const pid = crypto
    .createHash("sha256")
    .update(`${prompt}::${modelo}`)
    .digest("hex")
    .slice(0, 16);

  registro.set(pid, {
    id: pid,
    version,
    prompt,
    modelo,
    creado: new Date(),
    evaluacion,
    activo: false,
    canaryPeso: 0.0,
  });
  console.log(`[version] Registrado prompt ${pid} v${version} (${modelo})`);
  return pid;
}

function activarPrompt(promptId: string): void {
  for (const p of registro.values()) {
    if (p.activo) {
      p.activo = false;
      p.canaryPeso = 0.0;
    }
  }
  const p = registro.get(promptId)!;
  p.activo = true;
  historialActivos.push(promptId);
  console.log(`[version] Activado prompt ${promptId} v${p.version}`);
}

function rollback(): string | null {
  if (historialActivos.length < 2) {
    console.log("[version] No hay versión anterior para rollback");
    return null;
  }
  historialActivos.pop();
  const anteriorId = historialActivos[historialActivos.length - 1];
  activarPrompt(anteriorId);
  console.log(`[version] Rollback a ${anteriorId}`);
  return anteriorId;
}

function activarCanary(canaryId: string, peso: number = 0.1): void {
  if (!registro.has(canaryId)) throw new Error(`Prompt ${canaryId} no registrado`);
  registro.get(canaryId)!.canaryPeso = peso;
  console.log(`[canary] Prompt ${canaryId} recibe ${(peso * 100).toFixed(0)}% del tráfico`);
}

function obtenerPromptParaRequest(): VersionPrompt {
  const candidatosCanary = Array.from(registro.values()).filter((p) => p.canaryPeso > 0);

  for (const canary of candidatosCanary) {
    if (Math.random() < canary.canaryPeso) return canary;
  }

  for (const p of registro.values()) {
    if (p.activo) return p;
  }

  throw new Error("No hay prompt activo");
}

async function llamarConVersion(pregunta: string): Promise<Record<string, string>> {
  const pv = obtenerPromptParaRequest();
  const respuesta = await cliente.messages.create({
    model: pv.modelo,
    max_tokens: 256,
    system: pv.prompt,
    messages: [{ role: "user", content: pregunta }],
  });
  return {
    respuesta: (respuesta.content[0] as Anthropic.TextBlock).text,
    promptVersion: pv.version,
    promptId: pv.id,
  };
}

async function main(): Promise<void> {
  const VERSIONES_MODELO = {
    haiku:  "claude-haiku-4-5-20251001",
    sonnet: "claude-sonnet-4-6-20250219",
    opus:   "claude-opus-4-7-20250219",
  };

  console.log("=== Registro y activación ===");
  const v1 = registrarPrompt(
    "Eres un asistente técnico conciso.",
    VERSIONES_MODELO.sonnet,
    "1.0.0",
    { pass_rate: 0.82, casos: 50 }
  );
  activarPrompt(v1);

  const v2 = registrarPrompt(
    "Eres un asistente técnico conciso. Usa ejemplos de código cuando sea útil.",
    VERSIONES_MODELO.sonnet,
    "1.1.0",
    { pass_rate: 0.87, casos: 50 }
  );

  console.log("\n=== Canary deployment (10% tráfico a v1.1.0) ===");
  activarCanary(v2, 0.10);

  console.log("\n=== Llamadas con selección automática de versión ===");
  for (let i = 0; i < 5; i++) {
    const r = await llamarConVersion("¿Qué es un agente ReAct?");
    console.log(`Request ${i + 1}: version=${r.promptVersion} | respuesta=${r.respuesta.slice(0, 60)}...`);
  }

  console.log("\n=== Rollback ===");
  activarPrompt(v2);
  rollback();
}

main().catch(console.error);
