// Memoria episódica con decay exponencial, refuerzo y ciclo de vida.
// Usa better-sqlite3 para persistencia. El embedding usa un vector ficticio
// de dimensión 4 — reemplazar con modelo real en producción.

// Cómo ejecutar: make ts SCRIPT=typescript/06-memoria/03-episodica/episodic_decay.ts


import { Database } from "bun:sqlite";

const CREATE_SQL = `
CREATE TABLE IF NOT EXISTS episodios (
    id             TEXT PRIMARY KEY,
    contenido      TEXT NOT NULL,
    timestamp      REAL NOT NULL,
    sesion_id      TEXT,
    fuerza         REAL NOT NULL DEFAULT 1.0,
    half_life_dias REAL NOT NULL DEFAULT 7.0,
    accesos        INTEGER NOT NULL DEFAULT 0,
    ultimo_acceso  REAL NOT NULL,
    estado         TEXT NOT NULL DEFAULT 'activo'
);
CREATE INDEX IF NOT EXISTS idx_episodios_estado    ON episodios(estado);
CREATE INDEX IF NOT EXISTS idx_episodios_fuerza    ON episodios(fuerza);
CREATE INDEX IF NOT EXISTS idx_episodios_timestamp ON episodios(timestamp);
`;

const UMBRAL_OLVIDO = 0.05;
const DELTA_REFUERZO = 0.15;
const UMBRAL_CONSOLIDAR = 0.85;

interface Episodio {
  id: string;
  contenido: string;
  timestamp: number;
  sesionId: string | null;
  fuerza: number;
  halfLifeDias: number;
  accesos: number;
  ultimoAcceso: number;
  estado: string;
}

function generateId(): string {
  return Math.random().toString(36).slice(2) + Date.now().toString(36);
}

function embed(texto: string): number[] {
  const words = texto.toLowerCase().split(/\s+/);
  const vec = [0.0, 0.0, 0.0, 0.0];
  for (let i = 0; i < Math.min(words.length, 4); i++) {
    let h = 0;
    for (const c of words[i]) {
      h = (Math.imul(31, h) + c.charCodeAt(0)) | 0;
    }
    vec[i % 4] += (Math.abs(h) % 100) / 100.0;
  }
  const norm = Math.sqrt(vec.reduce((s, x) => s + x * x, 0)) || 1.0;
  return vec.map((x) => x / norm);
}

function coseno(a: number[], b: number[]): number {
  const dot = a.reduce((s, x, i) => s + x * b[i], 0);
  const na = Math.sqrt(a.reduce((s, x) => s + x * x, 0)) || 1e-9;
  const nb = Math.sqrt(b.reduce((s, x) => s + x * x, 0)) || 1e-9;
  return dot / (na * nb);
}

class AlmacenEpisodico {
  private db: Database;
  private embeddings: Map<string, number[]> = new Map();

  constructor(dbPath: string = ":memory:") {
    this.db = new Database(dbPath);
    this.db.exec(CREATE_SQL);
  }

  record(contenido: string, sesionId: string | null = null, halfLifeDias: number = 7.0): Episodio {
    const id = generateId();
    const now = Date.now() / 1000;
    this.db
      .prepare(
        `INSERT INTO episodios (id, contenido, timestamp, sesion_id, fuerza, half_life_dias, accesos, ultimo_acceso, estado)
         VALUES (?,?,?,?,1.0,?,0,?,'activo')`,
      )
      .run(id, contenido, now, sesionId, halfLifeDias, now);
    this.embeddings.set(id, embed(contenido));
    return { id, contenido, timestamp: now, sesionId, fuerza: 1.0, halfLifeDias, accesos: 0, ultimoAcceso: now, estado: "activo" };
  }

  recall(query: string, topK: number = 5, skipReinforce: boolean = false): [Episodio, number][] {
    const qVec = embed(query);
    const ahora = Date.now() / 1000;

    const rows = this.db.prepare("SELECT * FROM episodios WHERE estado = 'activo'").all() as Record<string, unknown>[];
    if (!rows.length) return [];

    const timestamps = rows.map((r) => r.timestamp as number);
    const tMin = Math.min(...timestamps);
    const tMax = Math.max(...timestamps);
    const tRango = (tMax - tMin) || 1.0;

    const candidatos: [Episodio, number][] = rows.map((r) => {
      const epId = r.id as string;
      const epVec = this.embeddings.get(epId) ?? embed(r.contenido as string);
      const sim = Math.max(0, coseno(qVec, epVec));
      const recencia = ((r.timestamp as number) - tMin) / tRango;
      const fuerzaN = Math.min(1.0, r.fuerza as number);
      const score = 0.5 * sim + 0.3 * recencia + 0.2 * fuerzaN;
      const ep: Episodio = {
        id: epId, contenido: r.contenido as string, timestamp: r.timestamp as number,
        sesionId: r.sesion_id as string | null, fuerza: r.fuerza as number,
        halfLifeDias: r.half_life_dias as number, accesos: r.accesos as number,
        ultimoAcceso: r.ultimo_acceso as number, estado: r.estado as string,
      };
      return [ep, score];
    });

    candidatos.sort((a, b) => b[1] - a[1]);
    const resultado = candidatos.slice(0, topK);

    if (!skipReinforce) {
      for (const [ep] of resultado) {
        this.db
          .prepare(
            `UPDATE episodios SET fuerza = MIN(fuerza + ?, 2.0), accesos = accesos + 1, ultimo_acceso = ? WHERE id = ?`,
          )
          .run(DELTA_REFUERZO, ahora, ep.id);
      }
    }
    return resultado;
  }

  tickLifecycle(): { olvidados: number; consolidados: number } {
    const ahora = Date.now() / 1000;
    const stats = { olvidados: 0, consolidados: 0 };

    const rows = this.db.prepare("SELECT * FROM episodios WHERE estado = 'activo'").all() as Record<string, unknown>[];
    for (const r of rows) {
      const elapsedDias = (ahora - (r.ultimo_acceso as number)) / 86400.0;
      const nuevaFuerza = (r.fuerza as number) * Math.exp((-Math.LN2 * elapsedDias) / (r.half_life_dias as number));
      if (nuevaFuerza < UMBRAL_OLVIDO) {
        this.db.prepare("UPDATE episodios SET fuerza=?, estado='olvidado' WHERE id=?").run(nuevaFuerza, r.id);
        stats.olvidados++;
      } else {
        this.db.prepare("UPDATE episodios SET fuerza=? WHERE id=?").run(nuevaFuerza, r.id);
      }
    }

    const ventanaSegundos = 24 * 3600;
    const recientes = this.db
      .prepare("SELECT * FROM episodios WHERE estado='activo' AND timestamp > ?")
      .all(ahora - ventanaSegundos) as Record<string, unknown>[];

    const visitados = new Set<string>();
    for (const r of recientes) {
      if (visitados.has(r.id as string)) continue;
      const cluster: Record<string, unknown>[] = [r];
      const vecR = this.embeddings.get(r.id as string) ?? embed(r.contenido as string);
      for (const other of recientes) {
        if (other.id === r.id || visitados.has(other.id as string)) continue;
        const vecO = this.embeddings.get(other.id as string) ?? embed(other.contenido as string);
        if (coseno(vecR, vecO) >= UMBRAL_CONSOLIDAR) {
          cluster.push(other);
          visitados.add(other.id as string);
        }
      }
      visitados.add(r.id as string);
      if (cluster.length >= 3) {
        this.consolidarCluster(cluster);
        stats.consolidados += cluster.length;
      }
    }
    return stats;
  }

  private consolidarCluster(cluster: Record<string, unknown>[]): void {
    const textos = cluster.map((r) => r.contenido as string);
    const resumen = `[Consolidado de ${cluster.length} episodios]\n` + textos.slice(0, 3).join(" | ");
    const fuerza = Math.max(...cluster.map((r) => r.fuerza as number));
    const halfLife = cluster[0].half_life_dias as number;
    const nuevo = this.record(resumen, null, halfLife);
    this.db.prepare("UPDATE episodios SET fuerza=? WHERE id=?").run(fuerza, nuevo.id);
    const ids = cluster.map((r) => r.id as string);
    this.db
      .prepare(`UPDATE episodios SET estado='consolidado' WHERE id IN (${ids.map(() => "?").join(",")})`)
      .run(...ids);
  }
}

const store = new AlmacenEpisodico();
store.record("El usuario prefiere respuestas concisas", null, 30);
store.record("Bug en auth.py línea 247: condición invertida", null, 3);
store.record("Decidimos usar PostgreSQL en lugar de SQLite para producción", null, 90);
store.record("El módulo de billing tiene deuda técnica: lógica duplicada", null, 7);

console.log("--- recall: 'base de datos producción' ---");
for (const [ep, score] of store.recall("base de datos producción", 3)) {
  console.log(`  [${score.toFixed(3)}] ${ep.contenido.slice(0, 60)}`);
}

console.log("\n--- recall exploratorio (skipReinforce) ---");
for (const [ep, score] of store.recall("preferencias usuario", 2, true)) {
  console.log(`  [${score.toFixed(3)}] ${ep.contenido.slice(0, 60)}`);
}

console.log("\n--- tick_lifecycle ---");
const stats = store.tickLifecycle();
console.log(`  olvidados: ${stats.olvidados}, consolidados: ${stats.consolidados}`);
