// Cómo ejecutar: make ts SCRIPT=typescript/13-hitl/interrupcion.ts
import Anthropic from "@anthropic-ai/sdk";
import { Database } from "bun:sqlite";
import { randomUUID } from "crypto";

const cliente = new Anthropic();

interface EstadoAgenteData {
  thread_id:        string;
  tarea:            string;
  mensajes:         object[];
  paso_actual:      number;
  estado:           string;
  accion_pendiente: object | null;
  resultado_final:  string | null;
  timestamp:        number;
  expira_en:        number | null;
}

class EstadoAgente implements EstadoAgenteData {
  thread_id:        string;
  tarea:            string;
  mensajes:         object[];
  paso_actual:      number;
  estado:           string;
  accion_pendiente: object | null;
  resultado_final:  string | null;
  timestamp:        number;
  expira_en:        number | null;

  constructor(data: EstadoAgenteData) {
    this.thread_id        = data.thread_id;
    this.tarea            = data.tarea;
    this.mensajes         = data.mensajes;
    this.paso_actual      = data.paso_actual;
    this.estado           = data.estado;
    this.accion_pendiente = data.accion_pendiente ?? null;
    this.resultado_final  = data.resultado_final  ?? null;
    this.timestamp        = data.timestamp        ?? Date.now() / 1000;
    this.expira_en        = data.expira_en        ?? null;
  }
}

class Checkpointer {
  private db: Database;

  constructor(dbPath = ":memory:") {
    this.db = new Database(dbPath);
    this.db.exec(`
      CREATE TABLE IF NOT EXISTS checkpoints (
        thread_id   TEXT PRIMARY KEY,
        estado_json TEXT,
        ts          REAL
      )
    `);
  }

  guardar(estado: EstadoAgente): string {
    this.db
      .prepare("INSERT OR REPLACE INTO checkpoints (thread_id, estado_json, ts) VALUES (?, ?, ?)")
      .run(estado.thread_id, JSON.stringify(estado), Date.now() / 1000);
    return estado.thread_id;
  }

  restaurar(threadId: string): EstadoAgente | null {
    const row = this.db
      .prepare("SELECT estado_json FROM checkpoints WHERE thread_id = ?")
      .get(threadId) as { estado_json: string } | undefined;
    if (!row) return null;
    return new EstadoAgente(JSON.parse(row.estado_json));
  }

  listarPendientes(): { thread_id: string; ts: number }[] {
    const rows = this.db
      .prepare("SELECT thread_id, ts FROM checkpoints WHERE estado_json LIKE '%esperando_aprobacion%'")
      .all() as { thread_id: string; ts: number }[];
    return rows;
  }
}

const HERRAMIENTAS: Anthropic.Tool[] = [
  {
    name:        "buscar_datos",
    description: "Busca datos. Seguro, no requiere aprobación.",
    input_schema: {
      type:       "object",
      properties: { query: { type: "string" } },
      required:   ["query"],
    },
  },
  {
    name:        "borrar_registros",
    description: "Borra registros de producción. REQUIERE APROBACIÓN HUMANA.",
    input_schema: {
      type:       "object",
      properties: {
        tabla:              { type: "string" },
        condicion:          { type: "string" },
        registros_afectados: { type: "number" },
      },
      required: ["tabla", "condicion", "registros_afectados"],
    },
  },
];

const HERRAMIENTAS_ALTO_RIESGO = new Set(["borrar_registros"]);

function ejecutarHerramientaReal(nombre: string, params: Record<string, unknown>): string {
  if (nombre === "buscar_datos")     return `Datos encontrados para '${params["query"]}': 42 registros activos.`;
  if (nombre === "borrar_registros") return `[SIMULADO] ${params["registros_afectados"]} registros borrados de '${params["tabla"]}'.`;
  return `Herramienta '${nombre}' no reconocida.`;
}

async function ejecutarOInterrumpir(
  tarea: string,
  threadId: string | null,
  checkpointer: Checkpointer
): Promise<EstadoAgente> {
  let estado: EstadoAgente;

  if (threadId) {
    const restaurado = checkpointer.restaurar(threadId);
    if (restaurado && restaurado.estado === "esperando_aprobacion") {
      return restaurado;
    }
    estado = restaurado!;
  } else {
    const newId = randomUUID().slice(0, 8);
    estado = new EstadoAgente({
      thread_id:        newId,
      tarea,
      mensajes:         [{ role: "user", content: tarea }],
      paso_actual:      0,
      estado:           "en_progreso",
      accion_pendiente: null,
      resultado_final:  null,
      timestamp:        Date.now() / 1000,
      expira_en:        null,
    });
  }

  for (let i = 0; i < 15; i++) {
    const respuesta = await cliente.messages.create({
      model:      process.env["MODEL"] ?? "claude-sonnet-4-6",
      max_tokens: 512,
      tools:      HERRAMIENTAS,
      messages:   estado.mensajes as Anthropic.MessageParam[],
    });

    estado.mensajes.push({ role: "assistant", content: respuesta.content });
    estado.paso_actual += 1;

    if (respuesta.stop_reason === "end_turn") {
      const texto = respuesta.content.find((b): b is Anthropic.TextBlock => b.type === "text")?.text ?? "";
      estado.estado          = "completado";
      estado.resultado_final = texto;
      checkpointer.guardar(estado);
      return estado;
    }

    if (respuesta.stop_reason === "tool_use") {
      const toolResults: object[] = [];

      for (const bloque of respuesta.content) {
        if (bloque.type !== "tool_use") continue;

        if (HERRAMIENTAS_ALTO_RIESGO.has(bloque.name)) {
          estado.estado = "esperando_aprobacion";
          estado.accion_pendiente = {
            tool_use_id: bloque.id,
            nombre:      bloque.name,
            params:      bloque.input,
          };
          estado.expira_en = Date.now() / 1000 + 72 * 3600;
          checkpointer.guardar(estado);
          console.log(`\n[INTERRUPCION] Acción de alto riesgo detectada:`);
          console.log(`  Herramienta: ${bloque.name}`);
          console.log(`  Parámetros: ${JSON.stringify(bloque.input)}`);
          console.log(`  Thread ID para reanudar: ${estado.thread_id}`);
          return estado;
        }

        const resultado = ejecutarHerramientaReal(bloque.name, bloque.input as Record<string, unknown>);
        toolResults.push({ type: "tool_result", tool_use_id: bloque.id, content: resultado });
      }

      if (toolResults.length > 0) {
        estado.mensajes.push({ role: "user", content: toolResults });
      }
    }
  }

  estado.estado          = "completado";
  estado.resultado_final = "[max iteraciones]";
  checkpointer.guardar(estado);
  return estado;
}

async function reanudar(
  threadId: string,
  decision: string,
  checkpointer: Checkpointer,
  motivo = ""
): Promise<EstadoAgente> {
  const estado = checkpointer.restaurar(threadId);
  if (!estado) throw new Error(`Checkpoint ${threadId} no encontrado o expirado`);
  if (estado.estado !== "esperando_aprobacion") {
    throw new Error(`Thread ${threadId} no está esperando aprobación (estado=${estado.estado})`);
  }

  const accion = estado.accion_pendiente as Record<string, unknown>;
  estado.accion_pendiente = null;

  let resultadoDecision: string;
  if (decision === "rechazar") {
    resultadoDecision = `Acción rechazada por el usuario. Motivo: ${motivo || "no especificado"}`;
  } else {
    resultadoDecision = ejecutarHerramientaReal(accion["nombre"] as string, accion["params"] as Record<string, unknown>);
  }

  estado.mensajes.push({
    role:    "user",
    content: [{ type: "tool_result", tool_use_id: accion["tool_use_id"], content: resultadoDecision }],
  });
  estado.estado = "en_progreso";
  console.log(`\n[REANUDACION] Thread ${threadId} | decisión=${decision}`);

  return ejecutarOInterrumpir(estado.tarea, threadId, checkpointer);
}

async function main() {
  const cp = new Checkpointer();

  console.log("=== Ejecución con interrupción ===");
  let estado = await ejecutarOInterrumpir(
    "Primero busca los usuarios inactivos, luego borra los que llevan más de 2 años sin actividad.",
    null,
    cp
  );
  console.log(`\nEstado: ${estado.estado}`);

  if (estado.estado === "esperando_aprobacion") {
    console.log("\n=== Simulando aprobación humana ===");
    const estadoFinal = await reanudar(estado.thread_id, "aprobar", cp);
    console.log(`\nEstado final: ${estadoFinal.estado}`);
    console.log(`Resultado: ${estadoFinal.resultado_final}`);
  }
}

main();
