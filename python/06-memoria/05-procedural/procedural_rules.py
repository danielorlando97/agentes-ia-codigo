"""
Memoria procedural: reglas de comportamiento extraídas de feedback.

Las reglas tienen condición, acción, alcance, fuerza y origen. El agente
las inyecta en el system prompt al inicio de cada sesión, priorizadas por
fuerza y acotadas por presupuesto de tokens.

Cómo ejecutar:
    make py SCRIPT=python/06-memoria/05-procedural/procedural_rules.py

Qué esperar:
    Demo de extracción de reglas desde feedback y su inyección en el system prompt.
    Las reglas tienen fuerza (0-1) y se priorizan por fuerza al inyectar.

Variables de entorno:
    MODEL — modelo a usar (default: claude-sonnet-4-6)
"""
from __future__ import annotations

import sqlite3
import time
import uuid
from dataclasses import dataclass, field
from typing import Optional


# ---------------------------------------------------------------------------
# Esquema
# ---------------------------------------------------------------------------

CREATE_SQL = """
CREATE TABLE IF NOT EXISTS reglas (
    id           TEXT PRIMARY KEY,
    condicion    TEXT NOT NULL,
    accion       TEXT NOT NULL,
    alcance      TEXT NOT NULL DEFAULT 'global',
    fuerza       REAL NOT NULL DEFAULT 1.0,   -- 0..∞; >1 = muy establecida
    origen       TEXT NOT NULL DEFAULT 'feedback_explicito',
    estado       TEXT NOT NULL DEFAULT 'activa',   -- activa|suspendida|obsoleta
    conflicta_con TEXT,    -- id de regla con la que colisiona
    creado       REAL NOT NULL,
    ultimo_uso   REAL,
    usos         INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_reglas_alcance ON reglas(alcance);
CREATE INDEX IF NOT EXISTS idx_reglas_fuerza  ON reglas(fuerza DESC);
CREATE INDEX IF NOT EXISTS idx_reglas_estado  ON reglas(estado);
"""

# Tokens máximos dedicados a reglas procedurales en el system prompt
BUDGET_TOKENS_REGLAS = 800
# Penalización de fuerza por conflicto detectado
PENALIZACION_CONFLICTO = 0.3
# Refuerzo por uso en conversación
REFUERZO_USO = 0.1


@dataclass
class Regla:
    id:           str
    condicion:    str
    accion:       str
    alcance:      str       # 'global' | 'dominio:<tipo>' | 'usuario:<id>' | 'proyecto:<id>' | 'sesion_actual'
    fuerza:       float
    origen:       str       # 'feedback_explicito' | 'feedback_implicito' | 'instruccion_sistema' | 'patron_observado'
    estado:       str
    conflicta_con: Optional[str]
    creado:       float
    ultimo_uso:   Optional[float] = None
    usos:         int = 0


# ---------------------------------------------------------------------------
# AlmacenProcedural
# ---------------------------------------------------------------------------

class AlmacenProcedural:
    def __init__(self, db_path: str = ":memory:"):
        self.conn = sqlite3.connect(db_path, check_same_thread=False)
        self.conn.row_factory = sqlite3.Row
        self.conn.executescript(CREATE_SQL)
        self.conn.commit()

    # ------------------------------------------------------------------
    # add_rule — añadir o reforzar una regla existente
    # ------------------------------------------------------------------

    def add_rule(
        self,
        condicion: str,
        accion: str,
        alcance: str = "global",
        fuerza_inicial: float = 1.0,
        origen: str = "feedback_explicito",
    ) -> Regla:
        """
        Inserta una nueva regla. Si ya existe una con la misma (condicion, alcance),
        refuerza la fuerza en lugar de duplicar.
        """
        existente = self._buscar_existente(condicion, alcance)
        if existente:
            nueva_fuerza = existente.fuerza + REFUERZO_USO
            self.conn.execute(
                "UPDATE reglas SET fuerza=?, usos=usos+1 WHERE id=?",
                (nueva_fuerza, existente.id),
            )
            self.conn.commit()
            existente.fuerza = nueva_fuerza
            return existente

        regla = Regla(
            id=str(uuid.uuid4()),
            condicion=condicion,
            accion=accion,
            alcance=alcance,
            fuerza=fuerza_inicial,
            origen=origen,
            estado="activa",
            conflicta_con=None,
            creado=time.time(),
        )
        self.conn.execute(
            """INSERT INTO reglas
               (id, condicion, accion, alcance, fuerza, origen, estado, creado, usos)
               VALUES (?,?,?,?,?,?,?,?,0)""",
            (regla.id, regla.condicion, regla.accion, regla.alcance,
             regla.fuerza, regla.origen, regla.estado, regla.creado),
        )
        self.conn.commit()

        # Detectar conflictos con reglas existentes
        self._detectar_conflictos(regla)
        return regla

    # ------------------------------------------------------------------
    # on_feedback — reforzar o crear regla a partir de feedback del usuario
    # ------------------------------------------------------------------

    def on_feedback(
        self,
        feedback: str,
        contexto: str,
        tipo: str = "negativo",  # 'negativo' | 'positivo'
        alcance: str = "global",
    ) -> Optional[Regla]:
        """
        Extrae una regla de comportamiento a partir de feedback.

        En producción, esta extracción se delega a un LLM con structured output.
        Aquí se incluye la lógica de inserción — el paso de extracción es externo.

        Ejemplo de uso real:
            regla_extraida = llm.extraer_regla(feedback, contexto)
            store.on_feedback_extraida(regla_extraida.condicion, regla_extraida.accion, ...)
        """
        # Heurística simple de demostración: el feedback negativo genera regla con "NO"
        if tipo == "negativo":
            condicion = f"cuando el contexto incluya: {contexto[:80]}"
            accion    = f"evitar: {feedback[:120]}"
            return self.add_rule(condicion, accion, alcance=alcance,
                                  fuerza_inicial=1.2, origen="feedback_explicito")
        elif tipo == "positivo":
            condicion = f"cuando el contexto incluya: {contexto[:80]}"
            accion    = f"mantener: {feedback[:120]}"
            return self.add_rule(condicion, accion, alcance=alcance,
                                  fuerza_inicial=0.8, origen="feedback_implicito")
        return None

    # ------------------------------------------------------------------
    # build_system_prompt — inyectar reglas priorizadas con cap de tokens
    # ------------------------------------------------------------------

    def build_system_prompt(
        self,
        alcances: Optional[list[str]] = None,
        budget_tokens: int = BUDGET_TOKENS_REGLAS,
        tokens_por_caracter: float = 0.25,
    ) -> str:
        """
        Genera el bloque de reglas procedurales para el system prompt.

        Prioriza por fuerza descendente. Aplica cap de tokens para no
        superar el presupuesto asignado a esta región del contexto.

        alcances: lista de alcances a incluir. None = incluir todos los activos.
        """
        sql = "SELECT * FROM reglas WHERE estado='activa'"
        args: list = []
        if alcances:
            placeholders = ",".join("?" * len(alcances))
            sql += f" AND alcance IN ({placeholders})"
            args.extend(alcances)
        sql += " ORDER BY fuerza DESC"
        rows = self.conn.execute(sql, args).fetchall()

        if not rows:
            return ""

        lineas: list[str] = []
        tokens_usados = 0
        for row in rows:
            linea = f"- Si {row['condicion']}: {row['accion']}"
            tokens_linea = int(len(linea) * tokens_por_caracter)
            if tokens_usados + tokens_linea > budget_tokens:
                break
            lineas.append(linea)
            tokens_usados += tokens_linea

            # Registrar uso
            self.conn.execute(
                "UPDATE reglas SET ultimo_uso=?, usos=usos+1 WHERE id=?",
                (time.time(), row["id"]),
            )
        self.conn.commit()

        return "## Reglas de comportamiento\n\n" + "\n".join(lineas)

    # ------------------------------------------------------------------
    # Helpers
    # ------------------------------------------------------------------

    def _buscar_existente(self, condicion: str, alcance: str) -> Optional[Regla]:
        row = self.conn.execute(
            "SELECT * FROM reglas WHERE condicion=? AND alcance=? AND estado='activa' LIMIT 1",
            (condicion, alcance),
        ).fetchone()
        return self._row_to_regla(row) if row else None

    def _detectar_conflictos(self, nueva: Regla) -> None:
        """
        Heurística simple: si dos reglas tienen la misma condición pero acciones
        opuestas ('evitar' vs 'mantener'), marcarlas como conflictivas.
        """
        rows = self.conn.execute(
            "SELECT * FROM reglas WHERE condicion=? AND id != ? AND estado='activa'",
            (nueva.condicion, nueva.id),
        ).fetchall()

        for row in rows:
            accion_a = nueva.accion.lower()
            accion_b = row["accion"].lower()
            if (("evitar" in accion_a and "mantener" in accion_b) or
                    ("mantener" in accion_a and "evitar" in accion_b)):
                # Marcar ambas como conflictivas y penalizar fuerza
                self.conn.execute(
                    "UPDATE reglas SET conflicta_con=?, fuerza=MAX(0.1, fuerza-?) WHERE id=?",
                    (row["id"], PENALIZACION_CONFLICTO, nueva.id),
                )
                self.conn.execute(
                    "UPDATE reglas SET conflicta_con=?, fuerza=MAX(0.1, fuerza-?) WHERE id=?",
                    (nueva.id, PENALIZACION_CONFLICTO, row["id"]),
                )
        self.conn.commit()

    def listar(self, solo_activas: bool = True) -> list[Regla]:
        sql = "SELECT * FROM reglas"
        if solo_activas:
            sql += " WHERE estado='activa'"
        sql += " ORDER BY fuerza DESC"
        rows = self.conn.execute(sql).fetchall()
        return [self._row_to_regla(r) for r in rows]

    def _row_to_regla(self, row: sqlite3.Row) -> Regla:
        return Regla(
            id=row["id"], condicion=row["condicion"], accion=row["accion"],
            alcance=row["alcance"], fuerza=row["fuerza"], origen=row["origen"],
            estado=row["estado"], conflicta_con=row["conflicta_con"],
            creado=row["creado"], ultimo_uso=row["ultimo_uso"], usos=row["usos"],
        )


# ---------------------------------------------------------------------------
# Demo
# ---------------------------------------------------------------------------

if __name__ == "__main__":
    store = AlmacenProcedural()

    # Instrucciones del sistema (fuerza alta, alcance global)
    store.add_rule(
        condicion="siempre",
        accion="responder en el idioma del usuario",
        alcance="global",
        fuerza_inicial=2.0,
        origen="instruccion_sistema",
    )
    store.add_rule(
        condicion="siempre",
        accion="usar formato Markdown para código",
        alcance="global",
        fuerza_inicial=1.8,
        origen="instruccion_sistema",
    )

    # Feedback negativo del usuario
    store.on_feedback(
        feedback="no uses listas con viñetas para respuestas cortas",
        contexto="respuestas de menos de 3 puntos",
        tipo="negativo",
    )
    store.on_feedback(
        feedback="incluye siempre ejemplos ejecutables en Python",
        contexto="explicaciones técnicas con código",
        tipo="positivo",
        alcance="dominio:codigo",
    )

    print("--- Reglas activas (por fuerza) ---")
    for r in store.listar():
        conflicto = f" ⚠ conflicta con {r.conflicta_con[:8]}…" if r.conflicta_con else ""
        print(f"  [{r.fuerza:.2f}] {r.alcance}: {r.accion[:60]}{conflicto}")

    print("\n--- System prompt generado ---")
    print(store.build_system_prompt(budget_tokens=400))

    print("\n--- Solo alcance global ---")
    print(store.build_system_prompt(alcances=["global"], budget_tokens=300))
