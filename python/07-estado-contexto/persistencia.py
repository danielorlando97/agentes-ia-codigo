"""Checkpointing del estado del agente con SQLite.

Checkpoint: snapshot del estado en un punto semántico.
SQLiteCheckpointStore: implementación concreta con interfaz abstracta.
schema_version: previene deserialización incorrecta cuando el agente evoluciona.
downgrade_checkpoint: convierte un checkpoint de versión antigua al formato actual.
should_checkpoint: decide qué eventos justifican un checkpoint (semánticos, no mecánicos).

Cómo ejecutar:
    make py SCRIPT=python/07-estado-contexto/persistencia.py

Qué esperar:
    Demo de checkpointing en SQLite: crear, guardar, restaurar y migrar
    checkpoints entre versiones del agente. Los checkpoints son semanticos,
    no mecanicos (no se guarda en cada iteracion sino en puntos clave).

Variables de entorno:
    MODEL — modelo a usar (default: claude-sonnet-4-6)
"""
import json
import sqlite3
import time
import uuid
from abc import ABC, abstractmethod
from dataclasses import asdict, dataclass, field
from enum import Enum
from typing import Optional


SCHEMA_VERSION = 1  # incrementar en cada cambio estructural del Checkpoint


class CheckpointTrigger(Enum):
    PHASE_COMPLETE   = "phase_complete"    # el agente completó una fase del plan
    IRREVERSIBLE_ACT = "irreversible_act"  # acción que no se puede deshacer
    USER_REQUEST     = "user_request"      # el usuario pidió pausar
    BUDGET_THRESHOLD = "budget_threshold"  # contexto llegó al umbral de compactación
    PERIODIC         = "periodic"          # fallback si han pasado N tool calls sin checkpoint


@dataclass
class Checkpoint:
    task_id:        str
    messages:       list[dict]
    trigger:        str  = CheckpointTrigger.PHASE_COMPLETE.value
    schema_version: int  = SCHEMA_VERSION
    metadata:       dict = field(default_factory=dict)  # estado específico de la app
    id:             str  = field(default_factory=lambda: str(uuid.uuid4()))
    timestamp:      float = field(default_factory=time.time)

    def to_json(self) -> str:
        return json.dumps(asdict(self), ensure_ascii=False)

    @classmethod
    def from_json(cls, data: str) -> "Checkpoint":
        return cls(**json.loads(data))


class CheckpointStore(ABC):
    @abstractmethod
    def save(self, checkpoint: Checkpoint) -> str: ...

    @abstractmethod
    def load(self, checkpoint_id: str) -> Optional[Checkpoint]: ...

    @abstractmethod
    def list(self, task_id: str) -> list[Checkpoint]: ...

    @abstractmethod
    def latest(self, task_id: str) -> Optional[Checkpoint]: ...


class SQLiteCheckpointStore(CheckpointStore):
    def __init__(self, db_path: str = ":memory:"):
        self.conn = sqlite3.connect(db_path, check_same_thread=False)
        self.conn.execute("""
            CREATE TABLE IF NOT EXISTS checkpoints (
                id             TEXT PRIMARY KEY,
                task_id        TEXT NOT NULL,
                schema_version INTEGER NOT NULL,
                trigger        TEXT NOT NULL,
                timestamp      REAL NOT NULL,
                data           TEXT NOT NULL
            )
        """)
        self.conn.execute(
            "CREATE INDEX IF NOT EXISTS idx_task ON checkpoints(task_id, timestamp)"
        )
        self.conn.commit()

    def save(self, checkpoint: Checkpoint) -> str:
        self.conn.execute(
            "INSERT INTO checkpoints VALUES (?,?,?,?,?,?)",
            (
                checkpoint.id,
                checkpoint.task_id,
                checkpoint.schema_version,
                checkpoint.trigger,
                checkpoint.timestamp,
                checkpoint.to_json(),
            ),
        )
        self.conn.commit()
        return checkpoint.id

    def load(self, checkpoint_id: str) -> Optional[Checkpoint]:
        row = self.conn.execute(
            "SELECT data, schema_version FROM checkpoints WHERE id=?",
            (checkpoint_id,),
        ).fetchone()
        if not row:
            return None
        data, stored_version = row
        cp = Checkpoint.from_json(data)
        if cp.schema_version != SCHEMA_VERSION:
            cp = downgrade_checkpoint(cp, SCHEMA_VERSION)
        return cp

    def list(self, task_id: str) -> list[Checkpoint]:
        rows = self.conn.execute(
            "SELECT data FROM checkpoints WHERE task_id=? ORDER BY timestamp ASC",
            (task_id,),
        ).fetchall()
        return [Checkpoint.from_json(r[0]) for r in rows]

    def latest(self, task_id: str) -> Optional[Checkpoint]:
        row = self.conn.execute(
            "SELECT data FROM checkpoints WHERE task_id=? ORDER BY timestamp DESC LIMIT 1",
            (task_id,),
        ).fetchone()
        return Checkpoint.from_json(row[0]) if row else None


def downgrade_checkpoint(checkpoint: Checkpoint, target_version: int) -> Checkpoint:
    """Convierte un checkpoint a target_version ante incompatibilidad de schema.

    En lugar de fallar, produce un checkpoint degradado con el que el agente
    puede continuar con contexto parcial. En producción: añadir cases por versión.
    """
    if checkpoint.schema_version == target_version:
        return checkpoint

    # Degradación universal: preservar messages y task_id, descartar metadata específica
    return Checkpoint(
        task_id=checkpoint.task_id,
        messages=checkpoint.messages,
        trigger="downgraded_from_v" + str(checkpoint.schema_version),
        schema_version=target_version,
        metadata={
            "downgraded": True,
            "original_version": checkpoint.schema_version,
        },
        id=checkpoint.id,
        timestamp=checkpoint.timestamp,
    )


def should_checkpoint(trigger: str, tool_calls_since_last: int = 0) -> bool:
    """Decide si el evento actual justifica un checkpoint.

    PHASE_COMPLETE e IRREVERSIBLE_ACT siempre hacen checkpoint.
    PERIODIC solo si llevamos ≥20 tool calls sin checkpoint (fallback de seguridad).
    """
    primary_triggers = {
        CheckpointTrigger.PHASE_COMPLETE.value,
        CheckpointTrigger.IRREVERSIBLE_ACT.value,
        CheckpointTrigger.USER_REQUEST.value,
    }
    if trigger in primary_triggers:
        return True
    if trigger == CheckpointTrigger.PERIODIC.value:
        return tool_calls_since_last >= 20
    return False


if __name__ == "__main__":
    store = SQLiteCheckpointStore()
    task = "analisis-repo-facturacion"

    # Guardar checkpoint al completar la primera fase
    msgs = [
        {"role": "user", "content": "Analiza el módulo de auth."},
        {"role": "assistant", "content": "Encontré 2 vulnerabilidades en auth.py."},
    ]
    cp1 = Checkpoint(
        task_id=task,
        messages=msgs,
        trigger=CheckpointTrigger.PHASE_COMPLETE.value,
        metadata={"fase": 1, "vulnerabilidades": 2},
    )
    id1 = store.save(cp1)
    print(f"Guardado: {id1[:8]}... | trigger={cp1.trigger}")

    # Cargar y verificar
    cargado = store.load(id1)
    print(f"Cargado: task={cargado.task_id} | msgs={len(cargado.messages)} | v={cargado.schema_version}")

    # Simular checkpoint de versión antigua
    cp_viejo = Checkpoint(task_id=task, messages=msgs, schema_version=0)
    cp_degradado = downgrade_checkpoint(cp_viejo, SCHEMA_VERSION)
    print(f"Degradado: v{cp_viejo.schema_version}→v{cp_degradado.schema_version} | {cp_degradado.metadata}")

    # Verificar decisión de checkpoint
    print(f"\nshould_checkpoint(phase_complete): {should_checkpoint('phase_complete')}")
    print(f"should_checkpoint(periodic, 5 tool calls): {should_checkpoint('periodic', 5)}")
    print(f"should_checkpoint(periodic, 25 tool calls): {should_checkpoint('periodic', 25)}")

    # Listar todos los checkpoints de la tarea
    todos = store.list(task)
    print(f"\nCheckpoints para '{task}': {len(todos)}")
