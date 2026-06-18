// Memoria procedural: reglas de comportamiento extraídas de feedback.

// Cómo ejecutar: make ts SCRIPT=typescript/06-memoria/05-procedural/procedural_rules.ts

import { Database } from "bun:sqlite";
import { randomUUID } from "crypto";

const BUDGET_TOKENS_REGLAS = 800;
const PENALIZACION_CONFLICTO = 0.3;
const REFUERZO_USO = 0.1;

const CREATE_SQL = `
CREATE TABLE IF NOT EXISTS reglas (
    id           TEXT PRIMARY KEY,
    condicion    TEXT NOT NULL,
    accion       TEXT NOT NULL,
    alcance      TEXT NOT NULL DEFAULT 'global',
    fuerza       REAL NOT NULL DEFAULT 1.0,
    origen       TEXT NOT NULL DEFAULT 'feedback_explicito',
    estado       TEXT NOT NULL DEFAULT 'activa',
    conflicta_con TEXT,
    creado       REAL NOT NULL,
    ultimo_uso   REAL,
    usos         INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_reglas_alcance ON reglas(alcance);
CREATE INDEX IF NOT EXISTS idx_reglas_fuerza  ON reglas(fuerza DESC);
CREATE INDEX IF NOT EXISTS idx_reglas_estado  ON reglas(estado);
`;

interface Regla {
  id: string;
  condicion: string;
  accion: string;
  alcance: string;
  fuerza: number;
  origen: string;
  estado: string;
  conflicta_con: string | null;
  creado: number;
  ultimo_uso: number | null;
  usos: number;
}

class AlmacenProcedural {
  private db: Database;

  constructor(dbPath: string = ":memory:") {
    this.db = new Database(dbPath);
    this.db.exec(CREATE_SQL);
  }

  addRule(
    condicion: string,
    accion: string,
    alcance: string = "global",
    fuerzaInicial: number = 1.0,
    origen: string = "feedback_explicito"
  ): Regla {
    const existente = this.buscarExistente(condicion, alcance);
    if (existente) {
      const nuevaFuerza = existente.fuerza + REFUERZO_USO;
      this.db
        .prepare("UPDATE reglas SET fuerza=?, usos=usos+1 WHERE id=?")
        .run(nuevaFuerza, existente.id);
      return { ...existente, fuerza: nuevaFuerza };
    }

    const regla: Regla = {
      id: randomUUID(),
      condicion,
      accion,
      alcance,
      fuerza: fuerzaInicial,
      origen,
      estado: "activa",
      conflicta_con: null,
      creado: Date.now() / 1000,
      ultimo_uso: null,
      usos: 0,
    };
    this.db
      .prepare(
        `INSERT INTO reglas (id, condicion, accion, alcance, fuerza, origen, estado, creado, usos)
         VALUES (?,?,?,?,?,?,?,?,0)`
      )
      .run(regla.id, regla.condicion, regla.accion, regla.alcance, regla.fuerza, regla.origen, regla.estado, regla.creado);

    this.detectarConflictos(regla);
    return regla;
  }

  onFeedback(
    feedback: string,
    contexto: string,
    tipo: "negativo" | "positivo" = "negativo",
    alcance: string = "global"
  ): Regla | null {
    if (tipo === "negativo") {
      const condicion = `cuando el contexto incluya: ${contexto.slice(0, 80)}`;
      const accion = `evitar: ${feedback.slice(0, 120)}`;
      return this.addRule(condicion, accion, alcance, 1.2, "feedback_explicito");
    }
    if (tipo === "positivo") {
      const condicion = `cuando el contexto incluya: ${contexto.slice(0, 80)}`;
      const accion = `mantener: ${feedback.slice(0, 120)}`;
      return this.addRule(condicion, accion, alcance, 0.8, "feedback_implicito");
    }
    return null;
  }

  buildSystemPrompt(
    alcances: string[] | null = null,
    budgetTokens: number = BUDGET_TOKENS_REGLAS,
    tokensPorCaracter: number = 0.25
  ): string {
    let sql = "SELECT * FROM reglas WHERE estado='activa'";
    const args: (string | number)[] = [];
    if (alcances && alcances.length > 0) {
      sql += ` AND alcance IN (${alcances.map(() => "?").join(",")})`;
      args.push(...alcances);
    }
    sql += " ORDER BY fuerza DESC";
    const rows = this.db.prepare(sql).all(...args) as Regla[];

    if (rows.length === 0) return "";

    const lineas: string[] = [];
    let tokensUsados = 0;
    for (const row of rows) {
      const linea = `- Si ${row.condicion}: ${row.accion}`;
      const tokensLinea = Math.floor(linea.length * tokensPorCaracter);
      if (tokensUsados + tokensLinea > budgetTokens) break;
      lineas.push(linea);
      tokensUsados += tokensLinea;
      this.db
        .prepare("UPDATE reglas SET ultimo_uso=?, usos=usos+1 WHERE id=?")
        .run(Date.now() / 1000, row.id);
    }

    return "## Reglas de comportamiento\n\n" + lineas.join("\n");
  }

  listar(soloActivas: boolean = true): Regla[] {
    let sql = "SELECT * FROM reglas";
    if (soloActivas) sql += " WHERE estado='activa'";
    sql += " ORDER BY fuerza DESC";
    return this.db.prepare(sql).all() as Regla[];
  }

  private buscarExistente(condicion: string, alcance: string): Regla | null {
    return (
      (this.db
        .prepare(
          "SELECT * FROM reglas WHERE condicion=? AND alcance=? AND estado='activa' LIMIT 1"
        )
        .get(condicion, alcance) as Regla) || null
    );
  }

  private detectarConflictos(nueva: Regla): void {
    const rows = this.db
      .prepare(
        "SELECT * FROM reglas WHERE condicion=? AND id != ? AND estado='activa'"
      )
      .all(nueva.condicion, nueva.id) as Regla[];

    for (const row of rows) {
      const accionA = nueva.accion.toLowerCase();
      const accionB = row.accion.toLowerCase();
      if (
        (accionA.includes("evitar") && accionB.includes("mantener")) ||
        (accionA.includes("mantener") && accionB.includes("evitar"))
      ) {
        this.db
          .prepare(
            "UPDATE reglas SET conflicta_con=?, fuerza=MAX(0.1, fuerza-?) WHERE id=?"
          )
          .run(row.id, PENALIZACION_CONFLICTO, nueva.id);
        this.db
          .prepare(
            "UPDATE reglas SET conflicta_con=?, fuerza=MAX(0.1, fuerza-?) WHERE id=?"
          )
          .run(nueva.id, PENALIZACION_CONFLICTO, row.id);
      }
    }
  }
}

if (require.main === module) {
  const store = new AlmacenProcedural();

  store.addRule("siempre", "responder en el idioma del usuario", "global", 2.0, "instruccion_sistema");
  store.addRule("siempre", "usar formato Markdown para código", "global", 1.8, "instruccion_sistema");
  store.onFeedback("no uses listas con viñetas para respuestas cortas", "respuestas de menos de 3 puntos", "negativo");
  store.onFeedback("incluye siempre ejemplos ejecutables en Python", "explicaciones técnicas con código", "positivo", "dominio:codigo");

  console.log("--- Reglas activas (por fuerza) ---");
  for (const r of store.listar()) {
    const conflicto = r.conflicta_con ? ` ⚠ conflicta con ${r.conflicta_con.slice(0, 8)}…` : "";
    console.log(`  [${r.fuerza.toFixed(2)}] ${r.alcance}: ${r.accion.slice(0, 60)}${conflicto}`);
  }

  console.log("\n--- System prompt generado ---");
  console.log(store.buildSystemPrompt(null, 400));

  console.log("\n--- Solo alcance global ---");
  console.log(store.buildSystemPrompt(["global"], 300));
}
