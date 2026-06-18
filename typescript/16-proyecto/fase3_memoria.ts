// Fase 3: añade memoria episódica SQLite a la Fase 2.
// Antes de revisar, consulta si ya existe una revisión cacheada del mismo código.

// Cómo ejecutar: make ts SCRIPT=typescript/16-proyecto/fase3_memoria.ts

import * as crypto from "crypto";
import * as fs from "fs";
import * as path from "path";
import { Database } from "bun:sqlite";

import { agenteRevision, RevisionOutput } from "./fase2_herramientas";

const DB_PATH = "revisiones.db";

interface RevisionCacheada extends RevisionOutput {
  fecha?: string;
  cached?: boolean;
  nota?: string;
}

function inicializarDb(dbPath: string): Database {
  const conn = new Database(dbPath);
  conn.exec(`
    CREATE TABLE IF NOT EXISTS revisiones (
      hash_archivo TEXT PRIMARY KEY,
      ruta TEXT,
      fecha TEXT,
      hallazgos_json TEXT,
      resumen TEXT
    )
  `);
  return conn;
}

function buscarRevisionPrevia(
  conn: Database,
  codigo: string
): RevisionCacheada | null {
  const hashCodigo = crypto.createHash("sha256").update(codigo).digest("hex");
  const fila = conn
    .prepare(
      "SELECT hallazgos_json, resumen, fecha FROM revisiones WHERE hash_archivo = ?"
    )
    .get(hashCodigo) as
    | { hallazgos_json: string; resumen: string; fecha: string }
    | undefined;

  if (fila) {
    return {
      hallazgos: JSON.parse(fila.hallazgos_json),
      resumen: fila.resumen,
      fecha: fila.fecha,
      cached: true,
    };
  }
  return null;
}

function guardarRevision(
  conn: Database,
  codigo: string,
  ruta: string,
  revision: RevisionOutput
): void {
  const hashCodigo = crypto.createHash("sha256").update(codigo).digest("hex");
  conn
    .prepare(
      `INSERT OR REPLACE INTO revisiones
       (hash_archivo, ruta, fecha, hallazgos_json, resumen)
       VALUES (?, ?, datetime('now'), ?, ?)`
    )
    .run(
      hashCodigo,
      ruta,
      JSON.stringify(revision.hallazgos),
      revision.resumen
    );
}

export async function agenteRevisionConMemoria(
  codigo: string,
  ruta: string,
  proyectoDir: string,
  dbPath: string = DB_PATH
): Promise<RevisionCacheada> {
  const conn = inicializarDb(dbPath);

  const revisionPrevia = buscarRevisionPrevia(conn, codigo);
  if (revisionPrevia) {
    revisionPrevia.nota = `Revisión previa del ${revisionPrevia.fecha}`;
    return revisionPrevia;
  }

  const revision = await agenteRevision(codigo, proyectoDir);
  guardarRevision(conn, codigo, ruta, revision);
  conn.close();
  return revision;
}

async function main() {
  const args = process.argv.slice(2);
  let codigo: string;
  let ruta: string;
  let proyectoDir: string;

  if (args.length > 0) {
    codigo = fs.readFileSync(args[0], "utf-8");
    ruta = args[0];
    proyectoDir = args[1] || process.cwd();
  } else {
    codigo = `
def procesar(items):
    return [item.value for item in items]  # AttributeError si item no tiene .value
`;
    ruta = "test.py";
    proyectoDir = process.cwd();
  }

  const resultado = await agenteRevisionConMemoria(codigo, ruta, proyectoDir);
  const cached = resultado.cached ?? false;
  console.error(`[${cached ? "CACHED" : "NUEVO"}]`);
  console.log(JSON.stringify(resultado, null, 2));
}

main().catch((err) => {
  console.error("Error:", err.message);
  process.exit(1);
});
