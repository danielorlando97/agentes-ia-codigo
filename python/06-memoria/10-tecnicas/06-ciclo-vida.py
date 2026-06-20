"""Ciclo de vida de un recuerdo: decay, tombstone, consolidación y olvido auditado.

Implementa los cuatro mecanismos que cierran el ciclo de vida de la memoria:
  1. Decaimiento exponencial configurable por tipo (media vida en días)
  2. Corrección con tombstone — el recuerdo anterior no se borra, se archiva
  3. Consolidación de clusters similares (union-find sobre embeddings mock)
  4. Olvido auditado — soft delete que preserva el historial para auditoría

Cómo ejecutar:
    make py SCRIPT=python/06-memoria/10-tecnicas/06-ciclo-vida.py

Qué esperar:
    Inserta 8 recuerdos, simula el paso del tiempo (decay), corrige un hecho
    con tombstone, consolida duplicados y muestra el estado final del almacén.
    No requiere API key — toda la lógica es local con SQLite en memoria.
"""

import math
import sqlite3
import time
import uuid
import random
from dataclasses import dataclass

# ── Configuración de media vida por tipo ──────────────────────────────────

MEDIO_VIDA_DIAS = {
    "episodio_sesion": 7,
    "preferencia": 30,
    "hecho_usuario": 180,
}

UMBRAL_OLVIDO = 0.05   # por debajo de esto → candidato a olvido
UMBRAL_CONSOLIDAR = 0.92  # similitud coseno mínima para consolidar


# ── Esquema SQL ───────────────────────────────────────────────────────────

ESQUEMA = """
CREATE TABLE IF NOT EXISTS memorias (
    id               TEXT PRIMARY KEY,
    contenido        TEXT NOT NULL,
    tipo             TEXT NOT NULL DEFAULT 'hecho_usuario',
    estado           TEXT NOT NULL DEFAULT 'activo',
    fuerza_base      REAL NOT NULL DEFAULT 1.0,
    fuerza_actual    REAL NOT NULL DEFAULT 1.0,
    ultimo_uso       REAL NOT NULL,
    veces_usado      INTEGER NOT NULL DEFAULT 0,
    medio_vida_dias  REAL NOT NULL DEFAULT 90,
    reemplaza_a      TEXT,
    reemplazado_por  TEXT,
    razon_correccion TEXT,
    creado           REAL NOT NULL,
    procesado_en     REAL
);
CREATE INDEX IF NOT EXISTS idx_activos ON memorias(estado, fuerza_actual DESC);
"""


# ── Funciones de ciclo de vida ────────────────────────────────────────────

def _calcular_fuerza(fuerza_base: float, ultimo_uso: float, medio_vida_dias: float) -> float:
    delta_dias = (time.time() - ultimo_uso) / 86400
    return fuerza_base * math.exp(-0.693 * delta_dias / medio_vida_dias)


def _mock_embedding(texto: str, dim: int = 32) -> list[float]:
    random.seed(hash(texto) % (2**32))
    vec = [random.gauss(0, 1) for _ in range(dim)]
    norma = math.sqrt(sum(x**2 for x in vec))
    return [x / norma for x in vec]


def _cosine_sim(a: list[float], b: list[float]) -> float:
    return sum(x * y for x, y in zip(a, b))


@dataclass
class GestorCicloVida:
    conn: sqlite3.Connection

    @classmethod
    def en_memoria(cls) -> "GestorCicloVida":
        conn = sqlite3.connect(":memory:")
        conn.row_factory = sqlite3.Row
        conn.executescript(ESQUEMA)
        conn.commit()
        return cls(conn)

    # ── Insertar ──────────────────────────────────────────────────────────

    def insertar(self, contenido: str, tipo: str = "hecho_usuario", fuerza_base: float = 1.0) -> str:
        mid = str(uuid.uuid4())[:8]
        ahora = time.time()
        medio_vida = MEDIO_VIDA_DIAS.get(tipo, 90)
        self.conn.execute(
            """INSERT INTO memorias
               (id, contenido, tipo, fuerza_base, fuerza_actual, ultimo_uso, medio_vida_dias, creado)
               VALUES (?,?,?,?,?,?,?,?)""",
            (mid, contenido, tipo, fuerza_base, fuerza_base, ahora, medio_vida, ahora),
        )
        self.conn.commit()
        return mid

    # ── Reforzar (cada vez que un recuerdo contribuye a una respuesta) ────

    def reforzar(self, mid: str) -> None:
        ahora = time.time()
        self.conn.execute(
            """UPDATE memorias SET
               fuerza_base = MIN(1.0, fuerza_base + 0.1),
               ultimo_uso = ?,
               veces_usado = veces_usado + 1
               WHERE id = ?""",
            (ahora, mid),
        )
        self.conn.commit()

    # ── Decaimiento exponencial ───────────────────────────────────────────

    def actualizar_decaimiento(self) -> int:
        """Recalcula fuerza_actual para todos los activos. Background job."""
        filas = self.conn.execute(
            "SELECT id, fuerza_base, ultimo_uso, medio_vida_dias FROM memorias WHERE estado='activo'"
        ).fetchall()
        actualizados = 0
        for f in filas:
            nueva_fuerza = _calcular_fuerza(f["fuerza_base"], f["ultimo_uso"], f["medio_vida_dias"])
            self.conn.execute(
                "UPDATE memorias SET fuerza_actual=? WHERE id=?", (nueva_fuerza, f["id"])
            )
            actualizados += 1
        self.conn.commit()
        return actualizados

    # ── Corrección con tombstone ──────────────────────────────────────────

    def corregir(self, id_anterior: str, nuevo_contenido: str, razon: str = "") -> str:
        nuevo_id = str(uuid.uuid4())[:8]
        ahora = time.time()
        # Obtener tipo del anterior
        row = self.conn.execute("SELECT tipo, fuerza_base FROM memorias WHERE id=?", (id_anterior,)).fetchone()
        tipo = row["tipo"] if row else "hecho_usuario"
        fuerza = row["fuerza_base"] if row else 1.0
        medio_vida = MEDIO_VIDA_DIAS.get(tipo, 90)

        # 1. Insertar sucesor primero (tombstone con referencia válida)
        self.conn.execute(
            """INSERT INTO memorias
               (id, contenido, tipo, fuerza_base, fuerza_actual, ultimo_uso, medio_vida_dias, reemplaza_a, creado)
               VALUES (?,?,?,?,?,?,?,?,?)""",
            (nuevo_id, nuevo_contenido, tipo, fuerza, fuerza, ahora, medio_vida, id_anterior, ahora),
        )
        # 2. Marcar el anterior como tombstone
        self.conn.execute(
            """UPDATE memorias SET estado='tombstone', reemplazado_por=?, razon_correccion=?
               WHERE id=?""",
            (nuevo_id, razon, id_anterior),
        )
        self.conn.commit()
        return nuevo_id

    # ── Consolidación de clusters ─────────────────────────────────────────

    def consolidar_clusters(self, umbral: float = UMBRAL_CONSOLIDAR) -> int:
        """Une recuerdos con alta similitud semántica. Retorna número de consolidaciones."""
        filas = self.conn.execute(
            "SELECT id, contenido FROM memorias WHERE estado='activo'"
        ).fetchall()
        if len(filas) < 2:
            return 0

        embeddings = {f["id"]: _mock_embedding(f["contenido"]) for f in filas}

        # Encontrar pares similares
        parent = {f["id"]: f["id"] for f in filas}

        def find(x: str) -> str:
            while parent[x] != x:
                parent[x] = parent[parent[x]]
                x = parent[x]
            return x

        def union(x: str, y: str) -> None:
            parent[find(x)] = find(y)

        ids = [f["id"] for f in filas]
        for i in range(len(ids)):
            for j in range(i + 1, len(ids)):
                sim = _cosine_sim(embeddings[ids[i]], embeddings[ids[j]])
                if sim >= umbral:
                    union(ids[i], ids[j])

        # Agrupar por raíz
        clusters: dict[str, list] = {}
        for f in filas:
            raiz = find(f["id"])
            clusters.setdefault(raiz, []).append(f)

        consolidaciones = 0
        for raiz, miembros in clusters.items():
            if len(miembros) < 2:
                continue
            # Resumen mock del cluster (en producción: LLM pequeño)
            resumen = f"[Consolidado de {len(miembros)} recuerdos] " + miembros[0]["contenido"]
            cid = str(uuid.uuid4())[:8]
            ahora = time.time()
            # Fuerza máxima del cluster
            fuerzas = self.conn.execute(
                f"SELECT MAX(fuerza_base) FROM memorias WHERE id IN ({','.join('?'*len(miembros))})",
                [m["id"] for m in miembros],
            ).fetchone()[0]

            self.conn.execute(
                """INSERT INTO memorias
                   (id, contenido, tipo, fuerza_base, fuerza_actual, ultimo_uso, medio_vida_dias, creado)
                   VALUES (?,?,'hecho_usuario',?,?,?,90,?)""",
                (cid, resumen, fuerzas or 1.0, fuerzas or 1.0, ahora, ahora),
            )
            for m in miembros:
                self.conn.execute(
                    "UPDATE memorias SET estado='consolidado', reemplazado_por=?, razon_correccion='consolidación' WHERE id=?",
                    (cid, m["id"]),
                )
            consolidaciones += 1
        self.conn.commit()
        return consolidaciones

    # ── Olvido auditado ───────────────────────────────────────────────────

    def olvidar_debiles(self, umbral: float = UMBRAL_OLVIDO) -> int:
        """Marca como olvidados los recuerdos con fuerza por debajo del umbral."""
        ahora = time.time()
        resultado = self.conn.execute(
            "UPDATE memorias SET estado='olvidado', procesado_en=? WHERE estado='activo' AND fuerza_actual < ?",
            (ahora, umbral),
        )
        self.conn.commit()
        return resultado.rowcount

    # ── Búsqueda (solo activos) ───────────────────────────────────────────

    def buscar(self, k: int = 5) -> list[dict]:
        filas = self.conn.execute(
            "SELECT id, contenido, tipo, estado, fuerza_actual FROM memorias WHERE estado='activo' ORDER BY fuerza_actual DESC LIMIT ?",
            (k,),
        ).fetchall()
        return [dict(f) for f in filas]

    # ── Historial de versiones de un recuerdo ─────────────────────────────

    def historial(self, mid: str) -> list[dict]:
        filas = self.conn.execute(
            """WITH RECURSIVE cadena AS (
                SELECT id, contenido, estado, reemplaza_a, reemplazado_por, razon_correccion, creado
                FROM memorias WHERE id = ?
                UNION ALL
                SELECT m.id, m.contenido, m.estado, m.reemplaza_a, m.reemplazado_por, m.razon_correccion, m.creado
                FROM memorias m
                INNER JOIN cadena c ON m.id = c.reemplaza_a
            ) SELECT * FROM cadena ORDER BY creado ASC""",
            (mid,),
        ).fetchall()
        return [dict(f) for f in filas]

    # ── Stats ─────────────────────────────────────────────────────────────

    def stats(self) -> dict:
        filas = self.conn.execute(
            "SELECT estado, COUNT(*) as n FROM memorias GROUP BY estado"
        ).fetchall()
        return {f["estado"]: f["n"] for f in filas}


# ── Demo ──────────────────────────────────────────────────────────────────

if __name__ == "__main__":
    g = GestorCicloVida.en_memoria()

    print("=== Inserción inicial ===")
    ids = {}
    datos = [
        ("El usuario trabaja en Empresa A", "hecho_usuario", 0.9),
        ("El usuario prefiere Python sobre Java", "preferencia", 0.8),
        ("El usuario mencionó que hoy tiene dolor de cabeza", "episodio_sesion", 0.7),
        ("El usuario habla español como idioma nativo", "hecho_usuario", 1.0),
        ("El usuario prefiere Python como lenguaje principal", "preferencia", 0.75),  # ← duplicado semántico
        ("El usuario usa Python en todos sus proyectos", "preferencia", 0.7),         # ← duplicado semántico
        ("La reunión de ayer fue sobre el roadmap del Q3", "episodio_sesion", 0.6),
        ("El usuario es vegetariano", "hecho_usuario", 0.85),
    ]
    for contenido, tipo, fuerza in datos:
        mid = g.insertar(contenido, tipo, fuerza)
        ids[contenido[:30]] = mid
        print(f"  [{mid}] {contenido[:50]}")

    print(f"\nEstado inicial: {g.stats()}")

    # ── Simular paso del tiempo manipulando ultimo_uso ───────────────────
    print("\n=== Simulando paso del tiempo (episodio_sesion → 14 días) ===")
    hace_14_dias = time.time() - 14 * 86400
    g.conn.execute(
        "UPDATE memorias SET ultimo_uso=? WHERE tipo='episodio_sesion'", (hace_14_dias,)
    )
    g.conn.commit()
    actualizados = g.actualizar_decaimiento()
    print(f"Fuerzas recalculadas para {actualizados} recuerdos")

    fila = g.conn.execute(
        "SELECT contenido, fuerza_actual FROM memorias WHERE tipo='episodio_sesion' LIMIT 1"
    ).fetchone()
    if fila:
        print(f"  Episodio sesión tras 14 días: fuerza={fila['fuerza_actual']:.3f} (media_vida=7d → debería ser ~0.25)")

    # ── Corrección con tombstone ─────────────────────────────────────────
    print("\n=== Corrección con tombstone ===")
    mid_empresa_a = list(g.conn.execute(
        "SELECT id FROM memorias WHERE contenido LIKE '%Empresa A%'"
    ).fetchall())[0]["id"]
    nuevo_id = g.corregir(
        mid_empresa_a,
        "El usuario trabaja en Empresa B desde enero 2026",
        razon="el usuario lo comunicó explícitamente",
    )
    print(f"  Hecho corregido: {mid_empresa_a} → tombstone")
    print(f"  Nuevo hecho: {nuevo_id}")
    historial = g.historial(nuevo_id)
    for h in historial:
        print(f"    [{h['estado']}] {h['contenido'][:50]}")

    # ── Consolidación de clusters ────────────────────────────────────────
    print("\n=== Consolidación de clusters (preferencias Python) ===")
    n_consolidaciones = g.consolidar_clusters(umbral=0.6)  # umbral bajo para demo con mock embeddings
    print(f"  Clusters consolidados: {n_consolidaciones}")
    print(f"  Estado tras consolidación: {g.stats()}")

    # ── Olvido auditado ──────────────────────────────────────────────────
    print("\n=== Olvido auditado (episodios con fuerza baja) ===")
    n_olvidados = g.olvidar_debiles(umbral=0.4)
    print(f"  Recuerdos olvidados: {n_olvidados}")
    print(f"  Estado final: {g.stats()}")

    print("\n=== Recuerdos activos (por fuerza) ===")
    for r in g.buscar(k=10):
        print(f"  [{r['fuerza_actual']:.3f}] {r['contenido'][:60]}")
