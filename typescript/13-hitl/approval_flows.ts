// Cómo ejecutar: make ts SCRIPT=typescript/13-hitl/approval_flows.ts
import Anthropic from "@anthropic-ai/sdk";
import * as readline from "readline";
import { randomUUID } from "crypto";

const cliente = new Anthropic();

const ACCIONES_ALTO_RIESGO = new Set([
  "borrar_datos", "enviar_email_masivo", "transferencia_dinero",
  "modificar_cuenta_usuario", "desplegar_produccion", "revocar_accesos",
]);

const ACCIONES_MEDIO_RIESGO = new Set([
  "escribir_produccion", "operacion_bulk", "cambiar_configuracion",
]);

const UMBRAL_REGISTROS = 100;

function clasificarAccion(nombre: string, params: Record<string, unknown>): string {
  if (ACCIONES_ALTO_RIESGO.has(nombre)) return "alto";
  if (ACCIONES_MEDIO_RIESGO.has(nombre)) return "medio";
  if ((params["registros_afectados"] as number ?? 0) > UMBRAL_REGISTROS) return "alto";
  if (params["reversible"] === false) return "alto";
  return "bajo";
}

function describirImpacto(nombre: string, params: Record<string, unknown>): string {
  const registros = params["registros_afectados"] as number ?? 0;
  const tabla     = params["tabla"] as string ?? "desconocida";
  if (nombre === "borrar_datos") {
    return `Se borrarán ${registros} registros de la tabla '${tabla}' en producción. Esta operación es irreversible.`;
  }
  if (nombre === "enviar_email_masivo") {
    const dest = params["destinatarios"] as number ?? 0;
    return `Se enviará un email a ${dest} usuarios. No puede deshacerse una vez enviado.`;
  }
  return `Acción '${nombre}' con parámetros: ${JSON.stringify(params)}`;
}

interface SolicitudAprobacion {
  id:                 string;
  nombre_accion:      string;
  params:             Record<string, unknown>;
  impacto:            string;
  timestamp:          number;
  expira_en:          number;
  decision:           string | null;
  params_modificados: Record<string, unknown> | null;
}

const _cola: Record<string, SolicitudAprobacion> = {};

async function solicitarAprobacionSincrona(
  nombre: string,
  params: Record<string, unknown>
): Promise<{ tipo: string; params?: Record<string, unknown>; params_modificados?: Record<string, unknown>; motivo?: string }> {
  const impacto = describirImpacto(nombre, params);
  console.log(`\n[APROBACIÓN REQUERIDA]`);
  console.log(`Acción: ${nombre}`);
  console.log(`Impacto: ${impacto}`);
  console.log("Opciones: [a]probar / [r]echazar / [m]odificar");

  const rl = readline.createInterface({ input: process.stdin, output: process.stdout });

  return new Promise((resolve) => {
    rl.question("Tu decisión: ", (decision) => {
      decision = decision.trim().toLowerCase();

      if (decision.startsWith("a")) {
        rl.close();
        resolve({ tipo: "aprobar", params });
        return;
      }

      if (decision.startsWith("m")) {
        rl.question("Nuevos parámetros (JSON): ", (nuevos) => {
          rl.close();
          try {
            resolve({ tipo: "modificar", params_modificados: JSON.parse(nuevos.trim()) });
          } catch {
            resolve({ tipo: "rechazar", motivo: "parámetros modificados inválidos" });
          }
        });
        return;
      }

      rl.close();
      resolve({ tipo: "rechazar", motivo: "rechazado por el usuario" });
    });
  });
}

function encolarAprobacion(nombre: string, params: Record<string, unknown>, ttlHoras = 4): string {
  const solId = randomUUID().slice(0, 8);
  const ahora = Date.now() / 1000;
  _cola[solId] = {
    id:                 solId,
    nombre_accion:      nombre,
    params,
    impacto:            describirImpacto(nombre, params),
    timestamp:          ahora,
    expira_en:          ahora + ttlHoras * 3600,
    decision:           null,
    params_modificados: null,
  };
  console.log(`[COLA] Acción '${nombre}' encolada (id=${solId}, expira en ${ttlHoras}h)`);
  return solId;
}

type HerramientaFn = (nombre: string, params: Record<string, unknown>) => string;

async function ejecutarHerramientaConApproval(
  nombre: string,
  params: Record<string, unknown>,
  fnHerramienta: HerramientaFn,
  modo = "sincrono"
): Promise<Record<string, unknown>> {
  const nivel = clasificarAccion(nombre, params);

  if (nivel === "bajo" || modo === "auto") {
    const resultado = fnHerramienta(nombre, params);
    console.log(`[AUTO] ${nombre}: ${resultado}`);
    return { estado: "ejecutado", resultado };
  }

  if (nivel === "medio" || modo === "cola") {
    const solId = encolarAprobacion(nombre, params);
    return { estado: "pendiente_revision", id: solId };
  }

  const respuesta = await solicitarAprobacionSincrona(nombre, params);

  if (respuesta.tipo === "aprobar") {
    const resultado = fnHerramienta(nombre, respuesta.params!);
    return { estado: "ejecutado", resultado };
  }
  if (respuesta.tipo === "modificar") {
    const resultado = fnHerramienta(nombre, respuesta.params_modificados!);
    return { estado: "ejecutado_modificado", resultado };
  }
  return { estado: "rechazado", motivo: respuesta.motivo ?? "" };
}

const HERRAMIENTAS: Anthropic.Tool[] = [
  {
    name:        "buscar_info",
    description: "Busca información. Acción reversible y segura.",
    input_schema: {
      type:       "object",
      properties: { query: { type: "string" } },
      required:   ["query"],
    },
  },
  {
    name:        "borrar_datos",
    description: "Borra registros de la base de datos. IRREVERSIBLE.",
    input_schema: {
      type:       "object",
      properties: {
        tabla:               { type: "string" },
        registros_afectados: { type: "number" },
      },
      required: ["tabla", "registros_afectados"],
    },
  },
];

function ejecutarToolReal(nombre: string, params: Record<string, unknown>): string {
  if (nombre === "buscar_info")  return `Información encontrada para '${params["query"]}': resultado simulado.`;
  if (nombre === "borrar_datos") return `[SIMULADO] Se habrían borrado ${params["registros_afectados"]} registros de '${params["tabla"]}'.`;
  return `Herramienta '${nombre}' no reconocida.`;
}

async function agenteConApproval(tarea: string, modoAprobacion = "sincrono"): Promise<string> {
  const mensajes: Anthropic.MessageParam[] = [{ role: "user", content: tarea }];

  for (let i = 0; i < 10; i++) {
    const respuesta = await cliente.messages.create({
      model:      process.env["MODEL"] ?? "claude-sonnet-4-6",
      max_tokens: 1024,
      tools:      HERRAMIENTAS,
      messages:   mensajes,
    });

    mensajes.push({ role: "assistant", content: respuesta.content });

    if (respuesta.stop_reason === "end_turn") {
      return respuesta.content.find((b): b is Anthropic.TextBlock => b.type === "text")?.text ?? "";
    }

    if (respuesta.stop_reason === "tool_use") {
      const toolResults: Anthropic.ToolResultBlockParam[] = [];

      for (const bloque of respuesta.content) {
        if (bloque.type !== "tool_use") continue;
        const resultado = await ejecutarHerramientaConApproval(
          bloque.name,
          bloque.input as Record<string, unknown>,
          ejecutarToolReal,
          modoAprobacion
        );
        toolResults.push({
          type:        "tool_result",
          tool_use_id: bloque.id,
          content:     JSON.stringify(resultado),
        });
      }
      mensajes.push({ role: "user", content: toolResults });
    }
  }

  return "[max iteraciones]";
}

async function main() {
  console.log("=== Clasificación de riesgo ===");
  const tests: [string, Record<string, unknown>][] = [
    ["buscar_info",        { query: "usuarios activos" }],
    ["escribir_produccion", { tabla: "users", registros_afectados: 50 }],
    ["borrar_datos",       { tabla: "users", registros_afectados: 847 }],
  ];
  for (const [nombre, params] of tests) {
    const nivel = clasificarAccion(nombre, params);
    console.log(`  ${nombre}: ${nivel}`);
  }

  console.log("\n=== Agente con approval (modo auto — sin interacción) ===");
  const resultado = await agenteConApproval(
    "Busca información sobre usuarios activos en el último mes.",
    "auto"
  );
  console.log(`Resultado: ${resultado.slice(0, 200)}`);
}

main();
