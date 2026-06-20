"""Vector store con SQL como fuente de verdad y búsqueda semántica como ranking.

Arquitectura:
  - SQLite: almacén canónico de todos los recuerdos (integridad garantizada)
  - Embeddings en memoria (numpy): índice de recuperación semántica
  - Orquestación con degradación: si los vectores fallan, SQLite con FTS sigue funcionando

Cómo ejecutar:
    make py SCRIPT=python/06-memoria/10-tecnicas/03-vector-store.py

Requiere: numpy (pip install numpy)

Qué esperar:
    Inserta 8 recuerdos. Busca por similitud semántica (mock embeddings).
    Demuestra la degradación cuando el índice vectorial falla.
"""

import json
import math
import sqlite3
import random
from dataclasses import dataclass, field
from typing import Optional


def _mock_embedding(texto: str, dim: int = 64) -> list[float]:
    """Embedding determinista mock. En producción: llamada a API de embeddings."""
    random.seed(hash(texto) % (2**32))
    vec = [random.gauss(0, 1) for _ in range(dim)]
    norma = math.sqrt(sum(x**2 for x in vec))
    return [x / norma for x in vec]


def _cosine_sim(a: list[float], b: list[float]) -> float:
    return sum(x * y for x, y in zip(a, b))


@dataclass
class VectorStore:
    db_path: str = ":memory:"
    embedding_dim: int = 64
    _conn: sqlite3.Connection = field(init=False, repr=False)
    _indice: dict = field(default_factory=dict, init=False, repr=False)
    _indice_activo: bool = field(default=True, init=False, repr=False)

    def __post_init__(self) -> None:
        self._conn = sqlite3.connect(self.db_path)
        self._conn.execute("PRAGMA journal_mode=WAL")
        self._conn.execute("""
            CREATE TABLE IF NOT EXISTS memorias (
                id      TEXT PRIMARY KEY,
                texto   TEXT NOT NULL,
                tipo    TEXT NOT NULL DEFAULT 'hecho',
                fuente  TEXT,
                creado  REAL NOT NULL,
                metadatos TEXT DEFAULT '{}'
            )
        """)
        self._conn.execute(
            "CREATE VIRTUAL TABLE IF NOT EXISTS memorias_fts USING fts5(id, texto)"
        )
        self._conn.commit()

    def insertar(self, id_: str, texto: str, tipo: str = "hecho", fuente: Optional[str] = None) -> None:
        import time
        self._conn.execute(
            "INSERT OR REPLACE INTO memorias VALUES (?,?,?,?,?,?)",
            (id_, texto, tipo, fuente, time.time(), "{}"),
        )
        self._conn.execute("INSERT OR REPLACE INTO memorias_fts VALUES (?,?)", (id_, texto))
        self._conn.commit()
        self._indice[id_] = _mock_embedding(texto)

    def buscar(self, query: str, k: int = 5) -> list[dict]:
        """Búsqueda con orquestación y degradación automática."""
        if self._indice_activo and self._indice:
            try:
                return self._buscar_semantico(query, k)
            except Exception as e:
                print(f"  [degradación] índice vectorial falló: {e} → usando FTS")
                self._indice_activo = False

        return self._buscar_fts(query, k)

    def _buscar_semantico(self, query: str, k: int) -> list[dict]:
        q_emb = _mock_embedding(query)
        scores = [
            (id_, _cosine_sim(q_emb, emb))
            for id_, emb in self._indice.items()
        ]
        scores.sort(key=lambda x: -x[1])
        top_ids = [id_ for id_, _ in scores[:k]]
        score_map = {id_: s for id_, s in scores[:k]}

        placeholders = ",".join("?" * len(top_ids))
        rows = self._conn.execute(
            f"SELECT id, texto, tipo, fuente FROM memorias WHERE id IN ({placeholders})",
            top_ids,
        ).fetchall()

        resultados = [
            {"id": r[0], "texto": r[1], "tipo": r[2], "fuente": r[3], "score": score_map[r[0]]}
            for r in rows
        ]
        return sorted(resultados, key=lambda x: -x["score"])

    def _buscar_fts(self, query: str, k: int) -> list[dict]:
        # FTS5: unir palabras con OR para búsqueda inclusiva
        fts_query = " OR ".join(query.split())
        filas = self._conn.execute(
            """SELECT m.id, m.texto, m.tipo, m.fuente
               FROM memorias_fts f
               JOIN memorias m ON m.id = f.id
               WHERE memorias_fts MATCH ?
               LIMIT ?""",
            (fts_query, k),
        ).fetchall()
        return [{"id": r[0], "texto": r[1], "tipo": r[2], "fuente": r[3], "score": None} for r in filas]

    def simular_fallo_indice(self) -> None:
        self._indice_activo = False
        print("  [simulación] índice vectorial desactivado")

    def total(self) -> int:
        return self._conn.execute("SELECT COUNT(*) FROM memorias").fetchone()[0]


if __name__ == "__main__":
    store = VectorStore()

    recuerdos = [
        ("m1", "Ana es la directora de producto de Acme Corp", "hecho"),
        ("m2", "El proyecto Pegasus tiene deadline en junio", "hecho"),
        ("m3", "El presupuesto del Q3 fue aprobado por Ana", "decision"),
        ("m4", "La integración con Stripe es parte de Pegasus", "hecho"),
        ("m5", "El equipo usa Python y TypeScript como lenguajes principales", "hecho"),
        ("m6", "La reunión de lanzamiento fue el 15 de marzo", "evento"),
        ("m7", "El bug en el módulo de auth afecta a usuarios admin", "hallazgo"),
        ("m8", "Ana aprobó el roadmap del Q4 en la reunión del viernes", "decision"),
    ]

    for id_, texto, tipo in recuerdos:
        store.insertar(id_, texto, tipo)

    print(f"Total recuerdos insertados: {store.total()}")
    print()

    print("Búsqueda semántica: 'proyectos con presupuesto aprobado'")
    resultados = store.buscar("proyectos con presupuesto aprobado", k=3)
    for r in resultados:
        print(f"  [{r['score']:.3f}] {r['texto']}")

    print()
    store.simular_fallo_indice()
    print("Búsqueda con degradación FTS: 'Ana proyecto'")
    resultados = store.buscar("Ana proyecto", k=3)
    for r in resultados:
        print(f"  [FTS] {r['texto']}")
