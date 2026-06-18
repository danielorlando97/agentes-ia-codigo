// Persistencia de estado: checkpoints en SQLite para reanudar tareas interrumpidas

// Cómo ejecutar: make ts SCRIPT=typescript/17-produccion/persistencia.ts

import Anthropic from "@anthropic-ai/sdk";
import { Database } from "bun:sqlite";
import crypto from "crypto";

const cliente = new Anthropic();

interface Checkpoint {
  tareaId: string;
  paso: number;
  mensajes: Anthropic.MessageParam[];
  tokensUsados: number;
  estado: "en_progreso" | "completado" | "fallido";
  resultado?: string;
  error?: string;
}

class AlmacenCheckpoints {
  private db: Database;

  constructor(dbPath: string = "checkpoints.db") {
    this.db = new Database(dbPath);
    this.db.exec(`
      CREATE TABLE IF NOT EXISTS checkpoints (
        tarea_id  TEXT,
        paso      INTEGER,
        mensajes  TEXT,
        tokens    INTEGER,
        estado    TEXT,
        resultado TEXT,
        error     TEXT,
        ts        TEXT DEFAULT (datetime('now')),
        PRIMARY KEY (tarea_id, paso)
      )
    `);
  }

  guardar(cp: Checkpoint): void {
    const stmt = this.db.prepare(
      "INSERT OR REPLACE INTO checkpoints (tarea_id, paso, mensajes, tokens, estado, resultado, error) VALUES (?, ?, ?, ?, ?, ?, ?)"
    );
    stmt.run(
      cp.tareaId,
      cp.paso,
      JSON.stringify(cp.mensajes),
      cp.tokensUsados,
      cp.estado,
      cp.resultado ?? null,
      cp.error ?? null
    );
  }

  cargarUltimo(tareaId: string): Checkpoint | null {
    const fila = this.db
      .prepare(
        "SELECT tarea_id, paso, mensajes, tokens, estado, resultado, error FROM checkpoints WHERE tarea_id = ? ORDER BY paso DESC LIMIT 1"
      )
      .get(tareaId) as
      | {
          tarea_id: string;
          paso: number;
          mensajes: string;
          tokens: number;
          estado: string;
          resultado: string | null;
          error: string | null;
        }
      | undefined;

    if (!fila) return null;
    return {
      tareaId: fila.tarea_id,
      paso: fila.paso,
      mensajes: JSON.parse(fila.mensajes),
      tokensUsados: fila.tokens,
      estado: fila.estado as Checkpoint["estado"],
      resultado: fila.resultado ?? undefined,
      error: fila.error ?? undefined,
    };
  }

  listar(): Array<{ tareaId: string; pasos: number; estado: string; ts: string }> {
    const filas = this.db
      .prepare(
        "SELECT tarea_id, MAX(paso) as pasos, estado, ts FROM checkpoints GROUP BY tarea_id ORDER BY ts DESC"
      )
      .all() as Array<{ tarea_id: string; pasos: number; estado: string; ts: string }>;
    return filas.map((f) => ({ tareaId: f.tarea_id, pasos: f.pasos, estado: f.estado, ts: f.ts }));
  }
}

async function ejecutarConCheckpoint(
  pregunta: string,
  tareaId?: string,
  almacen?: AlmacenCheckpoints
): Promise<Record<string, unknown>> {
  if (!almacen) almacen = new AlmacenCheckpoints();
  if (!tareaId) tareaId = crypto.randomUUID().slice(0, 8);

  const cp = almacen.cargarUltimo(tareaId);
  if (cp && cp.estado === "completado") {
    console.log(`[checkpoint] Tarea ${tareaId} ya completada — devolviendo resultado guardado`);
    return { resultado: cp.resultado, tareaId, reanudado: false };
  }

  let mensajes: Anthropic.MessageParam[];
  let tokensTotal: number;
  let pasoInicio: number;

  if (cp) {
    console.log(`[checkpoint] Reanudando tarea ${tareaId} desde paso ${cp.paso}`);
    mensajes = cp.mensajes;
    tokensTotal = cp.tokensUsados;
    pasoInicio = cp.paso + 1;
  } else {
    mensajes = [{ role: "user", content: pregunta }];
    tokensTotal = 0;
    pasoInicio = 0;
    console.log(`[checkpoint] Nueva tarea ${tareaId}`);
  }

  const MAX_PASOS = 10;
  for (let paso = pasoInicio; paso < MAX_PASOS; paso++) {
    const respuesta = await cliente.messages.create({
      model: process.env["MODEL"] ?? "claude-sonnet-4-6",
      max_tokens: 512,
      messages: mensajes,
    });
    tokensTotal += respuesta.usage.input_tokens + respuesta.usage.output_tokens;
    const textoRespuesta = (respuesta.content[0] as Anthropic.TextBlock).text;
    mensajes.push({ role: "assistant", content: textoRespuesta });

    almacen.guardar({
      tareaId: tareaId!,
      paso,
      mensajes,
      tokensUsados: tokensTotal,
      estado: "en_progreso",
    });
    console.log(`[checkpoint] Paso ${paso} guardado (tokens=${tokensTotal})`);

    if (respuesta.stop_reason === "end_turn") {
      almacen.guardar({
        tareaId: tareaId!,
        paso,
        mensajes,
        tokensUsados: tokensTotal,
        estado: "completado",
        resultado: textoRespuesta,
      });
      return { resultado: textoRespuesta, tareaId, pasos: paso + 1 };
    }
  }

  return { error: "Límite de pasos alcanzado", tareaId };
}

async function main(): Promise<void> {
  const almacen = new AlmacenCheckpoints(":memory:");

  console.log("=== Primera ejecución ===");
  const r = await ejecutarConCheckpoint(
    "¿Qué ventajas tiene SQLite frente a PostgreSQL para aplicaciones pequeñas?",
    "demo-001",
    almacen
  );
  const texto = (r.resultado as string | undefined) ?? (r.error as string) ?? "";
  console.log(`Resultado: ${texto.slice(0, 200)}`);

  console.log("\n=== Reanudación (simula crash) ===");
  await ejecutarConCheckpoint(
    "¿Qué ventajas tiene SQLite frente a PostgreSQL para aplicaciones pequeñas?",
    "demo-001",
    almacen
  );
  console.log("Reanudado: tarea ya completada, resultado cacheado");

  console.log("\n=== Tareas registradas ===");
  for (const t of almacen.listar()) {
    console.log(t);
  }
}

main().catch(console.error);
