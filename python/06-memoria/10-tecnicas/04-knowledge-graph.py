"""Knowledge graph sobre SQLite: entidades, relaciones y recuperación por expansión de grafo.

Demuestra:
  - Esquema mínimo: entidades + relaciones tipadas + índice episodio↔entidad
  - Indexación incremental de episodios (extracción mock de entidades)
  - Recuperación por expansión de grafo hasta H saltos (BFS)

Cómo ejecutar:
    make py SCRIPT=python/06-memoria/10-tecnicas/04-knowledge-graph.py

Qué esperar:
    Índice de 4 episodios. Consulta relacional "proyectos con presupuesto aprobado"
    navega el grafo y recupera los episodios alcanzables desde las entidades mencionadas.
"""

import sqlite3
import uuid
from dataclasses import dataclass, field


ESQUEMA = """
CREATE TABLE IF NOT EXISTS entidades (
    id     TEXT PRIMARY KEY,
    nombre TEXT NOT NULL,
    tipo   TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS relaciones (
    id          TEXT PRIMARY KEY,
    desde_id    TEXT NOT NULL REFERENCES entidades(id),
    hasta_id    TEXT NOT NULL REFERENCES entidades(id),
    tipo        TEXT NOT NULL,
    memoria_id  TEXT,
    certeza     REAL DEFAULT 1.0
);

CREATE TABLE IF NOT EXISTS episodios (
    id       TEXT PRIMARY KEY,
    texto    TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS episodio_entidades (
    episodio_id  TEXT NOT NULL,
    entidad_id   TEXT NOT NULL,
    PRIMARY KEY (episodio_id, entidad_id)
);
"""


def _extraer_entidades_mock(texto: str) -> list[tuple[str, str]]:
    """Mock de NER. En producción: llamada a LLM o modelo de NER."""
    mapa = {
        "Ana": "persona",
        "Acme Corp": "organización",
        "Pegasus": "proyecto",
        "Q3": "periodo",
        "Stripe": "servicio",
        "Q4": "periodo",
    }
    return [(nombre, tipo) for nombre, tipo in mapa.items() if nombre.lower() in texto.lower()]


def _extraer_relaciones_mock(texto: str, entidades: list[tuple[str, str]]) -> list[tuple[str, str, str]]:
    """Mock de extracción de relaciones. Formato: (sujeto, verbo, objeto)."""
    relaciones = []
    nombres = {e[0] for e in entidades}
    if "Ana" in nombres and "Acme Corp" in nombres:
        relaciones.append(("Ana", "trabaja_en", "Acme Corp"))
    if "Ana" in nombres and "Q3" in nombres and "aprobó" in texto.lower():
        relaciones.append(("Ana", "aprobó", "Q3"))
    if "Pegasus" in nombres and "Stripe" in nombres:
        relaciones.append(("Pegasus", "incluye", "Stripe"))
    if "Q3" in nombres and "Pegasus" in nombres and "presupuesto" in texto.lower():
        relaciones.append(("Q3", "financia", "Pegasus"))
    if "Ana" in nombres and "Q4" in nombres:
        relaciones.append(("Ana", "aprobó", "Q4"))
    return relaciones


@dataclass
class KnowledgeGraph:
    db_path: str = ":memory:"
    _conn: sqlite3.Connection = field(init=False, repr=False)
    _entidades: dict = field(default_factory=dict, init=False, repr=False)

    def __post_init__(self) -> None:
        self._conn = sqlite3.connect(self.db_path)
        self._conn.executescript(ESQUEMA)
        self._conn.commit()

    def indexar_episodio(self, texto: str) -> str:
        ep_id = str(uuid.uuid4())[:8]
        self._conn.execute("INSERT INTO episodios VALUES (?,?)", (ep_id, texto))

        entidades = _extraer_entidades_mock(texto)
        relaciones = _extraer_relaciones_mock(texto, entidades)

        for nombre, tipo in entidades:
            ent_id = nombre.lower().replace(" ", "_")
            self._entidades[nombre] = ent_id
            self._conn.execute(
                "INSERT OR IGNORE INTO entidades VALUES (?,?,?)", (ent_id, nombre, tipo)
            )
            self._conn.execute(
                "INSERT OR IGNORE INTO episodio_entidades VALUES (?,?)", (ep_id, ent_id)
            )

        for sujeto, verbo, objeto in relaciones:
            s_id = self._entidades.get(sujeto)
            o_id = self._entidades.get(objeto)
            if s_id and o_id:
                rel_id = str(uuid.uuid4())[:8]
                self._conn.execute(
                    "INSERT OR IGNORE INTO relaciones VALUES (?,?,?,?,?,?)",
                    (rel_id, s_id, o_id, verbo, ep_id, 1.0),
                )

        self._conn.commit()
        return ep_id

    def recall_por_grafo(self, entidades_seed: list[str], hops: int = 2) -> list[dict]:
        seed_ids = {self._entidades.get(e) for e in entidades_seed if e in self._entidades}
        seed_ids.discard(None)

        visitados = set(seed_ids)
        frontera = set(seed_ids)

        for _ in range(hops):
            if not frontera:
                break
            placeholders = ",".join("?" * len(frontera))
            vecinos = self._conn.execute(
                f"SELECT hasta_id FROM relaciones WHERE desde_id IN ({placeholders})"
                f" UNION SELECT desde_id FROM relaciones WHERE hasta_id IN ({placeholders})",
                list(frontera) * 2,
            ).fetchall()
            nuevos = {v[0] for v in vecinos} - visitados
            visitados |= nuevos
            frontera = nuevos

        if not visitados:
            return []

        placeholders = ",".join("?" * len(visitados))
        ep_ids = self._conn.execute(
            f"SELECT DISTINCT episodio_id FROM episodio_entidades WHERE entidad_id IN ({placeholders})",
            list(visitados),
        ).fetchall()

        ep_id_list = [r[0] for r in ep_ids]
        if not ep_id_list:
            return []

        placeholders2 = ",".join("?" * len(ep_id_list))
        episodios = self._conn.execute(
            f"SELECT id, texto FROM episodios WHERE id IN ({placeholders2})",
            ep_id_list,
        ).fetchall()

        return [{"id": r[0], "texto": r[1], "entidades_alcanzadas": list(visitados)} for r in episodios]

    def stats(self) -> dict:
        return {
            "entidades": self._conn.execute("SELECT COUNT(*) FROM entidades").fetchone()[0],
            "relaciones": self._conn.execute("SELECT COUNT(*) FROM relaciones").fetchone()[0],
            "episodios": self._conn.execute("SELECT COUNT(*) FROM episodios").fetchone()[0],
        }


if __name__ == "__main__":
    kg = KnowledgeGraph()

    episodios = [
        "Ana es la directora de producto de Acme Corp",
        "El proyecto Pegasus tiene deadline en junio",
        "El presupuesto del Q3 fue aprobado por Ana",
        "La integración con Stripe es parte de Pegasus y financia el Q3 del proyecto",
        "Ana aprobó el roadmap del Q4 en la reunión del viernes",
    ]

    print("Indexando episodios...")
    for ep in episodios:
        ep_id = kg.indexar_episodio(ep)
        print(f"  [{ep_id}] {ep[:60]}")

    print()
    print(f"Grafo: {kg.stats()}")
    print()

    print("Consulta relacional: 'proyectos con presupuesto aprobado'")
    print("Seeds: ['Q3', 'Pegasus']  (entidades extraídas de la query)")
    resultados = kg.recall_por_grafo(["Q3", "Pegasus"], hops=2)
    print(f"Episodios recuperados: {len(resultados)}")
    for r in resultados:
        print(f"  {r['texto'][:80]}")

    print()
    print("Consulta: ¿qué sabe el sistema sobre Ana?  Seeds: ['Ana']")
    resultados = kg.recall_por_grafo(["Ana"], hops=1)
    for r in resultados:
        print(f"  {r['texto'][:80]}")
