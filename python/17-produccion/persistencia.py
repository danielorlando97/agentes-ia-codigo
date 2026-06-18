# Persistencia de estado: checkpoints en SQLite para reanudar tareas interrumpidas
#
# Cómo ejecutar:
#   make py SCRIPT=python/17-produccion/persistencia.py
#
# Qué esperar:
#   Demo de checkpoints en SQLite: guardar, restaurar y listar.
#   Simula una tarea de 5 pasos que se interrumpe y se reanuda.
#
# Variables de entorno:
#   MODEL — modelo a usar (default: claude-sonnet-4-6)

import json
import sqlite3
import uuid
from dataclasses import dataclass
from typing import Optional
import os
import anthropic

cliente = anthropic.Anthropic()


@dataclass
class Checkpoint:
    tarea_id: str
    paso: int
    mensajes: list
    tokens_usados: int
    estado: str          # "en_progreso" | "completado" | "fallido"
    resultado: Optional[str] = None
    error: Optional[str] = None


class AlmacenCheckpoints:
    def __init__(self, db_path: str = "checkpoints.db"):
        self.conn = sqlite3.connect(db_path, check_same_thread=False)
        self.conn.execute("""
            CREATE TABLE IF NOT EXISTS checkpoints (
                tarea_id    TEXT,
                paso        INTEGER,
                mensajes    TEXT,
                tokens      INTEGER,
                estado      TEXT,
                resultado   TEXT,
                error       TEXT,
                ts          TEXT DEFAULT (datetime('now')),
                PRIMARY KEY (tarea_id, paso)
            )
        """)
        self.conn.commit()

    def guardar(self, cp: Checkpoint) -> None:
        self.conn.execute(
            "INSERT OR REPLACE INTO checkpoints "
            "(tarea_id, paso, mensajes, tokens, estado, resultado, error) "
            "VALUES (?, ?, ?, ?, ?, ?, ?)",
            (
                cp.tarea_id, cp.paso,
                json.dumps(cp.mensajes),
                cp.tokens_usados, cp.estado,
                cp.resultado, cp.error,
            ),
        )
        self.conn.commit()

    def cargar_ultimo(self, tarea_id: str) -> Optional[Checkpoint]:
        fila = self.conn.execute(
            "SELECT tarea_id, paso, mensajes, tokens, estado, resultado, error "
            "FROM checkpoints WHERE tarea_id = ? ORDER BY paso DESC LIMIT 1",
            (tarea_id,),
        ).fetchone()
        if not fila:
            return None
        return Checkpoint(
            tarea_id=fila[0], paso=fila[1],
            mensajes=json.loads(fila[2]),
            tokens_usados=fila[3], estado=fila[4],
            resultado=fila[5], error=fila[6],
        )

    def listar(self) -> list[dict]:
        filas = self.conn.execute(
            "SELECT tarea_id, MAX(paso) as pasos, estado, ts "
            "FROM checkpoints GROUP BY tarea_id ORDER BY ts DESC"
        ).fetchall()
        return [{"tarea_id": f[0], "pasos": f[1], "estado": f[2], "ts": f[3]} for f in filas]


def ejecutar_con_checkpoint(
    pregunta: str,
    tarea_id: Optional[str] = None,
    almacen: Optional[AlmacenCheckpoints] = None,
) -> dict:
    """Ejecuta el agente con checkpoints. Si tarea_id existe, reanuda desde el último paso."""
    if almacen is None:
        almacen = AlmacenCheckpoints()
    if tarea_id is None:
        tarea_id = str(uuid.uuid4())[:8]

    # Intentar reanudar
    cp = almacen.cargar_ultimo(tarea_id)
    if cp and cp.estado == "completado":
        print(f"[checkpoint] Tarea {tarea_id} ya completada — devolviendo resultado guardado")
        return {"resultado": cp.resultado, "tarea_id": tarea_id, "reanudado": False}

    if cp:
        print(f"[checkpoint] Reanudando tarea {tarea_id} desde paso {cp.paso}")
        mensajes = cp.mensajes
        tokens_total = cp.tokens_usados
        paso_inicio = cp.paso + 1
    else:
        mensajes = [{"role": "user", "content": pregunta}]
        tokens_total = 0
        paso_inicio = 0
        print(f"[checkpoint] Nueva tarea {tarea_id}")

    MAX_PASOS = 10
    for paso in range(paso_inicio, MAX_PASOS):
        respuesta = cliente.messages.create(
            model=os.environ.get("MODEL", "claude-sonnet-4-6"),
            max_tokens=512,
            messages=mensajes,
        )
        tokens_total += respuesta.usage.input_tokens + respuesta.usage.output_tokens
        mensajes.append({"role": "assistant", "content": respuesta.content[0].text})

        # Guardar checkpoint inmediatamente después de cada paso
        almacen.guardar(Checkpoint(
            tarea_id=tarea_id,
            paso=paso,
            mensajes=mensajes,
            tokens_usados=tokens_total,
            estado="en_progreso",
        ))
        print(f"[checkpoint] Paso {paso} guardado (tokens={tokens_total})")

        if respuesta.stop_reason == "end_turn":
            texto_final = respuesta.content[0].text
            almacen.guardar(Checkpoint(
                tarea_id=tarea_id,
                paso=paso,
                mensajes=mensajes,
                tokens_usados=tokens_total,
                estado="completado",
                resultado=texto_final,
            ))
            return {"resultado": texto_final, "tarea_id": tarea_id, "pasos": paso + 1}

    return {"error": "Límite de pasos alcanzado", "tarea_id": tarea_id}


if __name__ == "__main__":
    almacen = AlmacenCheckpoints(":memory:")

    print("=== Primera ejecución ===")
    r = ejecutar_con_checkpoint(
        "¿Qué ventajas tiene SQLite frente a PostgreSQL para aplicaciones pequeñas?",
        tarea_id="demo-001",
        almacen=almacen,
    )
    print(f"Resultado: {r.get('resultado', r.get('error', ''))[:200]}")

    print("\n=== Reanudación (simula crash) ===")
    r2 = ejecutar_con_checkpoint(
        "¿Qué ventajas tiene SQLite frente a PostgreSQL para aplicaciones pequeñas?",
        tarea_id="demo-001",
        almacen=almacen,
    )
    print(f"Reanudado: tarea ya completada, resultado cacheado")

    print("\n=== Tareas registradas ===")
    for t in almacen.listar():
        print(t)
