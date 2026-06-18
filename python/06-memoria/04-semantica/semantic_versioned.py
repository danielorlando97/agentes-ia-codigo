"""
Memoria semántica con tombstone+versionado y detección explícita de conflictos.

Los hechos nunca se sobrescriben: una corrección crea un tombstone del hecho
anterior y una nueva versión que lo referencia. Las contradicciones con
certeza similar generan un objeto de conflicto en lugar de resolverse silenciosamente.

Cómo ejecutar:
    make py SCRIPT=python/06-memoria/04-semantica/semantic_versioned.py

Qué esperar:
    Demo de tombstones y versionado: cada corrección crea una nueva versión.
    Las contradicciones detectadas generan un objeto de conflicto en lugar de
    resolverse silenciosamente.

Variables de entorno:
    MODEL — modelo a usar (default: claude-sonnet-4-6)
"""
from __future__ import annotations

import sqlite3
import time
import uuid
from dataclasses import dataclass
from enum import Enum
from typing import Optional


# ---------------------------------------------------------------------------
# Esquema
# ---------------------------------------------------------------------------

CREATE_SQL = """
CREATE TABLE IF NOT EXISTS hechos (
    id           TEXT PRIMARY KEY,
    sujeto       TEXT NOT NULL,
    predicado    TEXT NOT NULL,
    objeto       TEXT NOT NULL,
    certeza      REAL NOT NULL DEFAULT 1.0,
    fuente       TEXT NOT NULL DEFAULT 'usuario_directo',
    creado       REAL NOT NULL,
    estado       TEXT NOT NULL DEFAULT 'activo',   -- activo|tombstone
    version      INTEGER NOT NULL DEFAULT 1,
    reemplaza_a  TEXT REFERENCES hechos(id)
);

CREATE TABLE IF NOT EXISTS conflictos (
    id           TEXT PRIMARY KEY,
    hecho_a_id   TEXT REFERENCES hechos(id),
    hecho_b_id   TEXT REFERENCES hechos(id),
    creado       REAL NOT NULL,
    resuelto     INTEGER NOT NULL DEFAULT 0,   -- 0=pendiente, 1=resuelto
    resolucion   TEXT                          -- 'a_gana'|'b_gana'|'ambos_validos'
);

CREATE INDEX IF NOT EXISTS idx_hechos_sujeto    ON hechos(sujeto);
CREATE INDEX IF NOT EXISTS idx_hechos_predicado ON hechos(predicado);
CREATE INDEX IF NOT EXISTS idx_hechos_estado    ON hechos(estado);
"""

# Umbral de diferencia de certeza para resolución automática.
# Si |certeza_nueva - certeza_existente| > DELTA_CERTEZA → resolución automática.
# Si ≤ DELTA_CERTEZA → conflicto explícito.
DELTA_CERTEZA = 0.20


class Fuente(str, Enum):
    USUARIO_DIRECTO     = "usuario_directo"     # 0.95-1.0
    TOOL_RESULTADO      = "tool_resultado"       # 0.80-0.90
    AUTO_EXTRACT        = "auto_extract"         # 0.50-0.70
    INFERENCIA          = "inferencia"           # 0.40-0.60


CERTEZA_POR_FUENTE = {
    Fuente.USUARIO_DIRECTO: 0.97,
    Fuente.TOOL_RESULTADO:  0.85,
    Fuente.AUTO_EXTRACT:    0.60,
    Fuente.INFERENCIA:      0.50,
}


@dataclass
class Hecho:
    id:          str
    sujeto:      str
    predicado:   str
    objeto:      str
    certeza:     float
    fuente:      str
    creado:      float
    estado:      str
    version:     int
    reemplaza_a: Optional[str] = None


@dataclass
class Conflicto:
    id:         str
    hecho_a_id: str
    hecho_b_id: str
    creado:     float
    resuelto:   bool
    resolucion: Optional[str]


# ---------------------------------------------------------------------------
# AlmacenSemantico
# ---------------------------------------------------------------------------

class AlmacenSemantico:
    def __init__(self, db_path: str = ":memory:"):
        self.conn = sqlite3.connect(db_path, check_same_thread=False)
        self.conn.row_factory = sqlite3.Row
        self.conn.executescript(CREATE_SQL)
        self.conn.commit()

    # ------------------------------------------------------------------
    # assert_fact — afirmar un hecho, manejando versiones y conflictos
    # ------------------------------------------------------------------

    def assert_fact(
        self,
        sujeto: str,
        predicado: str,
        objeto: str,
        certeza: Optional[float] = None,
        fuente: Fuente = Fuente.USUARIO_DIRECTO,
    ) -> tuple[Hecho, Optional[Conflicto]]:
        """
        Afirma (sujeto, predicado, objeto).

        - Si no existe ningún hecho activo con (sujeto, predicado): insertar.
        - Si existe y el nuevo objeto coincide: no hacer nada (idempotente).
        - Si existe y objetos difieren:
            - Diferencia de certeza > DELTA_CERTEZA → tombstone + nueva versión.
            - Diferencia de certeza ≤ DELTA_CERTEZA → crear conflicto explícito.

        Devuelve (nuevo_hecho, conflicto_o_None).
        """
        if certeza is None:
            certeza = CERTEZA_POR_FUENTE[fuente]

        existente = self._buscar_activo(sujeto, predicado)

        if existente is None:
            nuevo = self._insertar(sujeto, predicado, objeto, certeza, fuente)
            return nuevo, None

        if existente.objeto == objeto:
            # Idempotente: actualizar certeza si la nueva es mayor
            if certeza > existente.certeza:
                self.conn.execute(
                    "UPDATE hechos SET certeza=?, fuente=? WHERE id=?",
                    (certeza, fuente.value, existente.id),
                )
                self.conn.commit()
            return existente, None

        # Los objetos difieren — decidir entre corrección o conflicto
        delta = abs(certeza - existente.certeza)

        if delta > DELTA_CERTEZA:
            # Quién gana: mayor certeza
            if certeza > existente.certeza:
                nuevo = self._corregir(existente, objeto, certeza, fuente)
                return nuevo, None
            else:
                # El existente tiene mayor certeza; el nuevo no lo desplaza
                return existente, None
        else:
            # Diferencia insuficiente → conflicto explícito
            nuevo      = self._insertar(sujeto, predicado, objeto, certeza, fuente)
            conflicto  = self._crear_conflicto(existente.id, nuevo.id)
            return nuevo, conflicto

    # ------------------------------------------------------------------
    # corregir_hecho — corrección explícita con tombstone
    # ------------------------------------------------------------------

    def corregir_hecho(
        self,
        hecho_id: str,
        nuevo_objeto: str,
        certeza: float = 0.97,
        fuente: Fuente = Fuente.USUARIO_DIRECTO,
    ) -> Hecho:
        """
        Corrección directa: tombstone del hecho existente + nueva versión.
        Usar cuando el usuario declara explícitamente que algo cambió.
        """
        existente = self._por_id(hecho_id)
        if existente is None:
            raise ValueError(f"Hecho {hecho_id} no encontrado")
        return self._corregir(existente, nuevo_objeto, certeza, fuente)

    # ------------------------------------------------------------------
    # query — recuperar hechos activos
    # ------------------------------------------------------------------

    def query(
        self,
        sujeto: Optional[str] = None,
        predicado: Optional[str] = None,
        certeza_minima: float = 0.0,
    ) -> list[Hecho]:
        """Recupera hechos activos que cumplan los filtros."""
        sql  = "SELECT * FROM hechos WHERE estado='activo' AND certeza >= ?"
        args: list = [certeza_minima]
        if sujeto:
            sql += " AND sujeto = ?"
            args.append(sujeto)
        if predicado:
            sql += " AND predicado = ?"
            args.append(predicado)
        sql += " ORDER BY certeza DESC, creado DESC"
        rows = self.conn.execute(sql, args).fetchall()
        return [self._row_to_hecho(r) for r in rows]

    def conflictos_pendientes(self) -> list[Conflicto]:
        rows = self.conn.execute(
            "SELECT * FROM conflictos WHERE resuelto=0"
        ).fetchall()
        return [self._row_to_conflicto(r) for r in rows]

    def resolver_conflicto(
        self,
        conflicto_id: str,
        resolucion: str,  # 'a_gana'|'b_gana'|'ambos_validos'
        hecho_a_ganar_id: Optional[str] = None,
    ) -> None:
        """
        Resuelve un conflicto. Si 'a_gana' o 'b_gana', hace tombstone del perdedor.
        """
        c = self.conn.execute(
            "SELECT * FROM conflictos WHERE id=?", (conflicto_id,)
        ).fetchone()
        if c is None:
            raise ValueError(f"Conflicto {conflicto_id} no encontrado")

        if resolucion == "a_gana":
            self._tombstone(c["hecho_b_id"])
        elif resolucion == "b_gana":
            self._tombstone(c["hecho_a_id"])
        # 'ambos_validos' → ambos quedan activos

        self.conn.execute(
            "UPDATE conflictos SET resuelto=1, resolucion=? WHERE id=?",
            (resolucion, conflicto_id),
        )
        self.conn.commit()

    # ------------------------------------------------------------------
    # Helpers internos
    # ------------------------------------------------------------------

    def _buscar_activo(self, sujeto: str, predicado: str) -> Optional[Hecho]:
        row = self.conn.execute(
            "SELECT * FROM hechos WHERE sujeto=? AND predicado=? AND estado='activo' LIMIT 1",
            (sujeto, predicado),
        ).fetchone()
        return self._row_to_hecho(row) if row else None

    def _por_id(self, hecho_id: str) -> Optional[Hecho]:
        row = self.conn.execute(
            "SELECT * FROM hechos WHERE id=?", (hecho_id,)
        ).fetchone()
        return self._row_to_hecho(row) if row else None

    def _insertar(
        self, sujeto: str, predicado: str, objeto: str,
        certeza: float, fuente: Fuente,
        version: int = 1, reemplaza_a: Optional[str] = None,
    ) -> Hecho:
        h = Hecho(
            id=str(uuid.uuid4()), sujeto=sujeto, predicado=predicado,
            objeto=objeto, certeza=certeza, fuente=fuente.value,
            creado=time.time(), estado="activo", version=version,
            reemplaza_a=reemplaza_a,
        )
        self.conn.execute(
            """INSERT INTO hechos
               (id, sujeto, predicado, objeto, certeza, fuente,
                creado, estado, version, reemplaza_a)
               VALUES (?,?,?,?,?,?,?,?,?,?)""",
            (h.id, h.sujeto, h.predicado, h.objeto, h.certeza, h.fuente,
             h.creado, h.estado, h.version, h.reemplaza_a),
        )
        self.conn.commit()
        return h

    def _corregir(
        self, existente: Hecho, nuevo_objeto: str,
        certeza: float, fuente: Fuente,
    ) -> Hecho:
        self._tombstone(existente.id)
        return self._insertar(
            existente.sujeto, existente.predicado, nuevo_objeto,
            certeza, fuente,
            version=existente.version + 1,
            reemplaza_a=existente.id,
        )

    def _tombstone(self, hecho_id: str) -> None:
        self.conn.execute(
            "UPDATE hechos SET estado='tombstone' WHERE id=?", (hecho_id,)
        )
        self.conn.commit()

    def _crear_conflicto(self, hecho_a_id: str, hecho_b_id: str) -> Conflicto:
        c = Conflicto(
            id=str(uuid.uuid4()),
            hecho_a_id=hecho_a_id,
            hecho_b_id=hecho_b_id,
            creado=time.time(),
            resuelto=False,
            resolucion=None,
        )
        self.conn.execute(
            "INSERT INTO conflictos (id, hecho_a_id, hecho_b_id, creado, resuelto) VALUES (?,?,?,?,0)",
            (c.id, c.hecho_a_id, c.hecho_b_id, c.creado),
        )
        self.conn.commit()
        return c

    def _row_to_hecho(self, row: sqlite3.Row) -> Hecho:
        return Hecho(
            id=row["id"], sujeto=row["sujeto"], predicado=row["predicado"],
            objeto=row["objeto"], certeza=row["certeza"], fuente=row["fuente"],
            creado=row["creado"], estado=row["estado"], version=row["version"],
            reemplaza_a=row["reemplaza_a"],
        )

    def _row_to_conflicto(self, row: sqlite3.Row) -> Conflicto:
        return Conflicto(
            id=row["id"], hecho_a_id=row["hecho_a_id"], hecho_b_id=row["hecho_b_id"],
            creado=row["creado"], resuelto=bool(row["resuelto"]),
            resolucion=row["resolucion"],
        )


# ---------------------------------------------------------------------------
# Demo
# ---------------------------------------------------------------------------

if __name__ == "__main__":
    store = AlmacenSemantico()

    # Afirmar hechos iniciales
    h1, _ = store.assert_fact("usuario", "lenguaje_preferido", "Python",
                               fuente=Fuente.USUARIO_DIRECTO)
    h2, _ = store.assert_fact("usuario", "zona_horaria", "Europe/Madrid",
                               fuente=Fuente.AUTO_EXTRACT, certeza=0.65)
    h3, _ = store.assert_fact("proyecto", "base_de_datos", "PostgreSQL",
                               fuente=Fuente.USUARIO_DIRECTO)

    print("--- Hechos activos ---")
    for h in store.query(certeza_minima=0.5):
        print(f"  ({h.sujeto}, {h.predicado}, {h.objeto}) certeza={h.certeza:.2f} v{h.version}")

    # Corrección con alta certeza → tombstone automático
    print("\n--- Corrección: lenguaje Python → Go (usuario lo declara) ---")
    h4, conflicto = store.assert_fact("usuario", "lenguaje_preferido", "Go",
                                       fuente=Fuente.USUARIO_DIRECTO)
    print(f"  nuevo hecho: {h4.objeto} v{h4.version}, reemplaza: {h4.reemplaza_a}")
    print(f"  conflicto: {conflicto}")

    # Contradicción con certeza similar → conflicto explícito
    print("\n--- Contradicción certeza similar → conflicto ---")
    h5, conflicto2 = store.assert_fact("usuario", "zona_horaria", "America/Mexico_City",
                                        fuente=Fuente.AUTO_EXTRACT, certeza=0.62)
    if conflicto2:
        print(f"  Conflicto creado: {conflicto2.id[:8]}…")
        print(f"  Pendientes: {len(store.conflictos_pendientes())}")

    print("\n--- Hechos activos finales ---")
    for h in store.query():
        print(f"  ({h.sujeto}, {h.predicado}, {h.objeto}) certeza={h.certeza:.2f} v{h.version}")
