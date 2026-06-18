// Memoria semántica con tombstone+versionado y detección explícita de conflictos.
// Los hechos nunca se sobrescriben: una corrección crea un tombstone y una nueva versión.
// Contradicciones con certeza similar generan un conflicto explícito.

// Cómo ejecutar: make ts SCRIPT=typescript/06-memoria/04-semantica/semantic_versioned.ts


import { Database } from "bun:sqlite";

const CREATE_SQL = `
CREATE TABLE IF NOT EXISTS hechos (
    id           TEXT PRIMARY KEY,
    sujeto       TEXT NOT NULL,
    predicado    TEXT NOT NULL,
    objeto       TEXT NOT NULL,
    certeza      REAL NOT NULL DEFAULT 1.0,
    fuente       TEXT NOT NULL DEFAULT 'usuario_directo',
    creado       REAL NOT NULL,
    estado       TEXT NOT NULL DEFAULT 'activo',
    version      INTEGER NOT NULL DEFAULT 1,
    reemplaza_a  TEXT REFERENCES hechos(id)
);
CREATE TABLE IF NOT EXISTS conflictos (
    id           TEXT PRIMARY KEY,
    hecho_a_id   TEXT REFERENCES hechos(id),
    hecho_b_id   TEXT REFERENCES hechos(id),
    creado       REAL NOT NULL,
    resuelto     INTEGER NOT NULL DEFAULT 0,
    resolucion   TEXT
);
CREATE INDEX IF NOT EXISTS idx_hechos_sujeto    ON hechos(sujeto);
CREATE INDEX IF NOT EXISTS idx_hechos_predicado ON hechos(predicado);
CREATE INDEX IF NOT EXISTS idx_hechos_estado    ON hechos(estado);
`;

const DELTA_CERTEZA = 0.20;

enum Fuente {
  USUARIO_DIRECTO = "usuario_directo",
  TOOL_RESULTADO = "tool_resultado",
  AUTO_EXTRACT = "auto_extract",
  INFERENCIA = "inferencia",
}

const CERTEZA_POR_FUENTE: Record<Fuente, number> = {
  [Fuente.USUARIO_DIRECTO]: 0.97,
  [Fuente.TOOL_RESULTADO]: 0.85,
  [Fuente.AUTO_EXTRACT]: 0.60,
  [Fuente.INFERENCIA]: 0.50,
};

interface Hecho {
  id: string;
  sujeto: string;
  predicado: string;
  objeto: string;
  certeza: number;
  fuente: string;
  creado: number;
  estado: string;
  version: number;
  reemplazaA: string | null;
}

interface Conflicto {
  id: string;
  hechoAId: string;
  hechoBId: string;
  creado: number;
  resuelto: boolean;
  resolucion: string | null;
}

function generateId(): string {
  return Math.random().toString(36).slice(2) + Date.now().toString(36);
}

class AlmacenSemantico {
  private db: Database;

  constructor(dbPath: string = ":memory:") {
    this.db = new Database(dbPath);
    this.db.exec(CREATE_SQL);
  }

  assertFact(
    sujeto: string,
    predicado: string,
    objeto: string,
    certeza?: number,
    fuente: Fuente = Fuente.USUARIO_DIRECTO,
  ): [Hecho, Conflicto | null] {
    const c = certeza ?? CERTEZA_POR_FUENTE[fuente];
    const existente = this.buscarActivo(sujeto, predicado);

    if (!existente) {
      return [this.insertar(sujeto, predicado, objeto, c, fuente), null];
    }
    if (existente.objeto === objeto) {
      if (c > existente.certeza) {
        this.db.prepare("UPDATE hechos SET certeza=?, fuente=? WHERE id=?").run(c, fuente, existente.id);
      }
      return [existente, null];
    }

    const delta = Math.abs(c - existente.certeza);
    if (delta > DELTA_CERTEZA) {
      if (c > existente.certeza) {
        return [this.corregir(existente, objeto, c, fuente), null];
      }
      return [existente, null];
    } else {
      const nuevo = this.insertar(sujeto, predicado, objeto, c, fuente);
      const conflicto = this.crearConflicto(existente.id, nuevo.id);
      return [nuevo, conflicto];
    }
  }

  corregirHecho(hechoId: string, nuevoObjeto: string, certeza: number = 0.97, fuente: Fuente = Fuente.USUARIO_DIRECTO): Hecho {
    const existente = this.porId(hechoId);
    if (!existente) throw new Error(`Hecho ${hechoId} no encontrado`);
    return this.corregir(existente, nuevoObjeto, certeza, fuente);
  }

  query(sujeto?: string, predicado?: string, certezaMinima: number = 0): Hecho[] {
    let sql = "SELECT * FROM hechos WHERE estado='activo' AND certeza >= ?";
    const args: unknown[] = [certezaMinima];
    if (sujeto) { sql += " AND sujeto = ?"; args.push(sujeto); }
    if (predicado) { sql += " AND predicado = ?"; args.push(predicado); }
    sql += " ORDER BY certeza DESC, creado DESC";
    return (this.db.prepare(sql).all(...args) as Record<string, unknown>[]).map(this.rowToHecho);
  }

  conflictosPendientes(): Conflicto[] {
    return (this.db.prepare("SELECT * FROM conflictos WHERE resuelto=0").all() as Record<string, unknown>[]).map(this.rowToConflicto);
  }

  resolverConflicto(conflictoId: string, resolucion: string): void {
    const c = this.db.prepare("SELECT * FROM conflictos WHERE id=?").get(conflictoId) as Record<string, unknown> | undefined;
    if (!c) throw new Error(`Conflicto ${conflictoId} no encontrado`);
    if (resolucion === "a_gana") this.tombstone(c.hecho_b_id as string);
    else if (resolucion === "b_gana") this.tombstone(c.hecho_a_id as string);
    this.db.prepare("UPDATE conflictos SET resuelto=1, resolucion=? WHERE id=?").run(resolucion, conflictoId);
  }

  private buscarActivo(sujeto: string, predicado: string): Hecho | null {
    const row = this.db.prepare("SELECT * FROM hechos WHERE sujeto=? AND predicado=? AND estado='activo' LIMIT 1").get(sujeto, predicado) as Record<string, unknown> | undefined;
    return row ? this.rowToHecho(row) : null;
  }

  private porId(id: string): Hecho | null {
    const row = this.db.prepare("SELECT * FROM hechos WHERE id=?").get(id) as Record<string, unknown> | undefined;
    return row ? this.rowToHecho(row) : null;
  }

  private insertar(sujeto: string, predicado: string, objeto: string, certeza: number, fuente: Fuente, version: number = 1, reemplazaA: string | null = null): Hecho {
    const id = generateId();
    const creado = Date.now() / 1000;
    this.db.prepare(
      `INSERT INTO hechos (id, sujeto, predicado, objeto, certeza, fuente, creado, estado, version, reemplaza_a)
       VALUES (?,?,?,?,?,?,?,?,?,?)`,
    ).run(id, sujeto, predicado, objeto, certeza, fuente, creado, "activo", version, reemplazaA);
    return { id, sujeto, predicado, objeto, certeza, fuente, creado, estado: "activo", version, reemplazaA };
  }

  private corregir(existente: Hecho, nuevoObjeto: string, certeza: number, fuente: Fuente): Hecho {
    this.tombstone(existente.id);
    return this.insertar(existente.sujeto, existente.predicado, nuevoObjeto, certeza, fuente, existente.version + 1, existente.id);
  }

  private tombstone(hechoId: string): void {
    this.db.prepare("UPDATE hechos SET estado='tombstone' WHERE id=?").run(hechoId);
  }

  private crearConflicto(hechoAId: string, hechoBId: string): Conflicto {
    const id = generateId();
    const creado = Date.now() / 1000;
    this.db.prepare("INSERT INTO conflictos (id, hecho_a_id, hecho_b_id, creado, resuelto) VALUES (?,?,?,?,0)").run(id, hechoAId, hechoBId, creado);
    return { id, hechoAId, hechoBId, creado, resuelto: false, resolucion: null };
  }

  private rowToHecho(row: Record<string, unknown>): Hecho {
    return {
      id: row.id as string, sujeto: row.sujeto as string, predicado: row.predicado as string,
      objeto: row.objeto as string, certeza: row.certeza as number, fuente: row.fuente as string,
      creado: row.creado as number, estado: row.estado as string, version: row.version as number,
      reemplazaA: row.reemplaza_a as string | null,
    };
  }

  private rowToConflicto(row: Record<string, unknown>): Conflicto {
    return {
      id: row.id as string, hechoAId: row.hecho_a_id as string, hechoBId: row.hecho_b_id as string,
      creado: row.creado as number, resuelto: Boolean(row.resuelto), resolucion: row.resolucion as string | null,
    };
  }
}

const store = new AlmacenSemantico();

const [h1] = store.assertFact("usuario", "lenguaje_preferido", "Python", undefined, Fuente.USUARIO_DIRECTO);
const [h2] = store.assertFact("usuario", "zona_horaria", "Europe/Madrid", 0.65, Fuente.AUTO_EXTRACT);
const [h3] = store.assertFact("proyecto", "base_de_datos", "PostgreSQL", undefined, Fuente.USUARIO_DIRECTO);

console.log("--- Hechos activos ---");
for (const h of store.query(undefined, undefined, 0.5)) {
  console.log(`  (${h.sujeto}, ${h.predicado}, ${h.objeto}) certeza=${h.certeza.toFixed(2)} v${h.version}`);
}

console.log("\n--- Corrección: lenguaje Python → Go ---");
const [h4, conflicto] = store.assertFact("usuario", "lenguaje_preferido", "Go", undefined, Fuente.USUARIO_DIRECTO);
console.log(`  nuevo hecho: ${h4.objeto} v${h4.version}, reemplaza: ${h4.reemplazaA}`);
console.log(`  conflicto: ${conflicto}`);

console.log("\n--- Contradicción certeza similar → conflicto ---");
const [h5, conflicto2] = store.assertFact("usuario", "zona_horaria", "America/Mexico_City", 0.62, Fuente.AUTO_EXTRACT);
if (conflicto2) {
  console.log(`  Conflicto creado: ${conflicto2.id.slice(0, 8)}…`);
  console.log(`  Pendientes: ${store.conflictosPendientes().length}`);
}

console.log("\n--- Hechos activos finales ---");
for (const h of store.query()) {
  console.log(`  (${h.sujeto}, ${h.predicado}, ${h.objeto}) certeza=${h.certeza.toFixed(2)} v${h.version}`);
}
