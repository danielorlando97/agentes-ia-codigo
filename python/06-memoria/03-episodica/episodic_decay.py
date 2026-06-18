"""
Memoria episódica con decay exponencial, refuerzo y ciclo de vida.

Requiere: sqlite-vec (pip install sqlite-vec), sentence-transformers o cualquier
función de embedding. El ejemplo usa un vector ficticio para que el código corra
sin dependencias externas — reemplaza `_embed` con tu modelo real.

Cómo ejecutar:
    make py SCRIPT=python/06-memoria/03-episodica/episodic_decay.py

Qué esperar:
    Episodios que decaen con el tiempo y se refuerzan al ser recuperados.
    Similaridad coseno + decay exponencial para scoring de relevancia.
    Nota: usa vectores ficticios — reemplaza _embed() con tu modelo real.

Variables de entorno:
    MODEL — modelo a usar (default: claude-sonnet-4-6)
"""
from __future__ import annotations

import math
import sqlite3
import time
import uuid
from dataclasses import dataclass, field
from typing import Optional

# ---------------------------------------------------------------------------
# Esquema
# ---------------------------------------------------------------------------

CREATE_SQL = """
CREATE TABLE IF NOT EXISTS episodios (
    id             TEXT PRIMARY KEY,
    contenido      TEXT NOT NULL,
    timestamp      REAL NOT NULL,
    sesion_id      TEXT,
    fuerza         REAL NOT NULL DEFAULT 1.0,
    half_life_dias REAL NOT NULL DEFAULT 7.0,
    accesos        INTEGER NOT NULL DEFAULT 0,
    ultimo_acceso  REAL NOT NULL,
    estado         TEXT NOT NULL DEFAULT 'activo',  -- activo|olvidado|consolidado|tombstone
    reemplaza_a    TEXT REFERENCES episodios(id)
);
CREATE INDEX IF NOT EXISTS idx_episodios_estado   ON episodios(estado);
CREATE INDEX IF NOT EXISTS idx_episodios_fuerza   ON episodios(fuerza);
CREATE INDEX IF NOT EXISTS idx_episodios_timestamp ON episodios(timestamp);
"""

UMBRAL_OLVIDO    = 0.05   # fuerza mínima antes de marcar como olvidado
DELTA_REFUERZO   = 0.15   # incremento de fuerza por cada recuperación con uso
UMBRAL_CONSOLIDAR = 0.85  # similitud coseno para agrupar episodios en cluster


@dataclass
class Episodio:
    id:             str
    contenido:      str
    timestamp:      float
    sesion_id:      Optional[str]
    fuerza:         float = 1.0
    half_life_dias: float = 7.0
    accesos:        int   = 0
    ultimo_acceso:  float = field(default_factory=time.time)
    estado:         str   = "activo"
    reemplaza_a:    Optional[str] = None


# ---------------------------------------------------------------------------
# Función de embedding (placeholder — reemplazar con modelo real)
# ---------------------------------------------------------------------------

def _embed(texto: str) -> list[float]:
    """
    Embedding ficticio de dimensión 4 para demostración.
    En producción: sentence_transformers, openai.embeddings.create, etc.
    """
    words = texto.lower().split()
    vec = [0.0, 0.0, 0.0, 0.0]
    for i, w in enumerate(words[:4]):
        vec[i % 4] += hash(w) % 100 / 100.0
    norm = math.sqrt(sum(x * x for x in vec)) or 1.0
    return [x / norm for x in vec]


def _coseno(a: list[float], b: list[float]) -> float:
    dot  = sum(x * y for x, y in zip(a, b))
    na   = math.sqrt(sum(x * x for x in a)) or 1e-9
    nb   = math.sqrt(sum(x * x for x in b)) or 1e-9
    return dot / (na * nb)


# ---------------------------------------------------------------------------
# AlmacenEpisodico
# ---------------------------------------------------------------------------

class AlmacenEpisodico:
    def __init__(self, db_path: str = ":memory:"):
        self.conn = sqlite3.connect(db_path, check_same_thread=False)
        self.conn.row_factory = sqlite3.Row
        self.conn.executescript(CREATE_SQL)
        self.conn.commit()
        # Caché de embeddings para búsqueda sin sqlite-vec
        self._embeddings: dict[str, list[float]] = {}

    # ------------------------------------------------------------------
    # record — insertar nuevo episodio
    # ------------------------------------------------------------------

    def record(
        self,
        contenido: str,
        sesion_id: Optional[str] = None,
        half_life_dias: float = 7.0,
    ) -> Episodio:
        ep = Episodio(
            id=str(uuid.uuid4()),
            contenido=contenido,
            timestamp=time.time(),
            sesion_id=sesion_id,
            half_life_dias=half_life_dias,
            ultimo_acceso=time.time(),
        )
        self.conn.execute(
            """INSERT INTO episodios
               (id, contenido, timestamp, sesion_id, fuerza, half_life_dias,
                accesos, ultimo_acceso, estado)
               VALUES (?,?,?,?,?,?,?,?,?)""",
            (ep.id, ep.contenido, ep.timestamp, ep.sesion_id,
             ep.fuerza, ep.half_life_dias, ep.accesos, ep.ultimo_acceso, ep.estado),
        )
        self.conn.commit()
        self._embeddings[ep.id] = _embed(contenido)
        return ep

    # ------------------------------------------------------------------
    # recall — recuperar episodios relevantes con scoring multi-señal
    # ------------------------------------------------------------------

    def recall(
        self,
        query: str,
        top_k: int = 5,
        skip_reinforce: bool = False,
    ) -> list[tuple[Episodio, float]]:
        """
        Scoring = 0.5 × similitud_coseno + 0.3 × recencia + 0.2 × fuerza_actual

        skip_reinforce=True para consultas exploratorias que no deben
        distorsionar las estadísticas de uso.
        """
        q_vec = _embed(query)
        ahora = time.time()

        rows = self.conn.execute(
            "SELECT * FROM episodios WHERE estado = 'activo'"
        ).fetchall()

        if not rows:
            return []

        # Calcular score para cada episodio activo
        candidatos: list[tuple[Episodio, float]] = []
        timestamps = [r["timestamp"] for r in rows]
        t_min, t_max = min(timestamps), max(timestamps)
        t_rango = (t_max - t_min) or 1.0

        for r in rows:
            ep_id = r["id"]
            ep_vec = self._embeddings.get(ep_id, _embed(r["contenido"]))

            sim       = max(0.0, _coseno(q_vec, ep_vec))
            recencia  = (r["timestamp"] - t_min) / t_rango          # 0..1
            fuerza_n  = min(1.0, r["fuerza"])                        # 0..1

            score = 0.5 * sim + 0.3 * recencia + 0.2 * fuerza_n

            ep = Episodio(
                id=ep_id, contenido=r["contenido"],
                timestamp=r["timestamp"], sesion_id=r["sesion_id"],
                fuerza=r["fuerza"], half_life_dias=r["half_life_dias"],
                accesos=r["accesos"], ultimo_acceso=r["ultimo_acceso"],
                estado=r["estado"],
            )
            candidatos.append((ep, score))

        candidatos.sort(key=lambda x: x[1], reverse=True)
        resultado = candidatos[:top_k]

        # Refuerzo — solo cuando el episodio contribuye a una respuesta real
        if not skip_reinforce:
            for ep, _ in resultado:
                self.conn.execute(
                    """UPDATE episodios
                       SET fuerza = MIN(fuerza + ?, 2.0),
                           accesos = accesos + 1,
                           ultimo_acceso = ?
                       WHERE id = ?""",
                    (DELTA_REFUERZO, ahora, ep.id),
                )
            self.conn.commit()

        return resultado

    # ------------------------------------------------------------------
    # tick_lifecycle — aplicar decay y consolidar; ejecutar en background
    # ------------------------------------------------------------------

    def tick_lifecycle(self) -> dict[str, int]:
        """
        Aplica decay exponencial a todos los episodios activos.
        Devuelve un resumen: cuántos olvidados y cuántos consolidados.
        """
        ahora = time.time()
        stats = {"olvidados": 0, "consolidados": 0}

        rows = self.conn.execute(
            "SELECT * FROM episodios WHERE estado = 'activo'"
        ).fetchall()

        for r in rows:
            elapsed_dias = (ahora - r["ultimo_acceso"]) / 86400.0
            nueva_fuerza = r["fuerza"] * math.exp(
                -math.log(2) * elapsed_dias / r["half_life_dias"]
            )

            if nueva_fuerza < UMBRAL_OLVIDO:
                self.conn.execute(
                    "UPDATE episodios SET fuerza=?, estado='olvidado' WHERE id=?",
                    (nueva_fuerza, r["id"]),
                )
                stats["olvidados"] += 1
            else:
                self.conn.execute(
                    "UPDATE episodios SET fuerza=? WHERE id=?",
                    (nueva_fuerza, r["id"]),
                )

        self.conn.commit()

        # Consolidación: agrupar episodios similares recientes en un resumen
        ventana_segundos = 24 * 3600
        recientes = self.conn.execute(
            "SELECT * FROM episodios WHERE estado='activo' AND timestamp > ?",
            (ahora - ventana_segundos,),
        ).fetchall()

        # Clustering por similitud coseno (naive O(n²) — suficiente para <200 eps.)
        visitados: set[str] = set()
        for r in recientes:
            if r["id"] in visitados:
                continue
            cluster = [r]
            vec_r = self._embeddings.get(r["id"], _embed(r["contenido"]))
            for other in recientes:
                if other["id"] == r["id"] or other["id"] in visitados:
                    continue
                vec_o = self._embeddings.get(other["id"], _embed(other["contenido"]))
                if _coseno(vec_r, vec_o) >= UMBRAL_CONSOLIDAR:
                    cluster.append(other)
                    visitados.add(other["id"])
            visitados.add(r["id"])

            if len(cluster) >= 3:
                self._consolidar_cluster(cluster)
                stats["consolidados"] += len(cluster)

        return stats

    def _consolidar_cluster(self, cluster: list[sqlite3.Row]) -> None:
        """Fusiona un cluster de episodios similares en uno solo."""
        textos   = [r["contenido"] for r in cluster]
        resumen  = f"[Consolidado de {len(cluster)} episodios]\n" + " | ".join(textos[:3])
        fuerza   = max(r["fuerza"] for r in cluster)
        half_life = cluster[0]["half_life_dias"]

        nuevo = self.record(resumen, half_life_dias=half_life)
        self.conn.execute(
            "UPDATE episodios SET fuerza=? WHERE id=?",
            (fuerza, nuevo.id),
        )
        ids = [r["id"] for r in cluster]
        self.conn.execute(
            f"UPDATE episodios SET estado='consolidado' WHERE id IN ({','.join('?'*len(ids))})",
            ids,
        )
        self.conn.commit()


# ---------------------------------------------------------------------------
# Demo
# ---------------------------------------------------------------------------

if __name__ == "__main__":
    store = AlmacenEpisodico()

    store.record("El usuario prefiere respuestas concisas", half_life_dias=30)
    store.record("Bug en auth.py línea 247: condición invertida", half_life_dias=3)
    store.record("Decidimos usar PostgreSQL en lugar de SQLite para producción", half_life_dias=90)
    store.record("El módulo de billing tiene deuda técnica: lógica duplicada", half_life_dias=7)

    print("--- recall: 'base de datos producción' ---")
    for ep, score in store.recall("base de datos producción", top_k=3):
        print(f"  [{score:.3f}] {ep.contenido[:60]}")

    print("\n--- recall exploratorio (skip_reinforce) ---")
    for ep, score in store.recall("preferencias usuario", top_k=2, skip_reinforce=True):
        print(f"  [{score:.3f}] {ep.contenido[:60]}")

    print("\n--- tick_lifecycle ---")
    stats = store.tick_lifecycle()
    print(f"  olvidados: {stats['olvidados']}, consolidados: {stats['consolidados']}")
