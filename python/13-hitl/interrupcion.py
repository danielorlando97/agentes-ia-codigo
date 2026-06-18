# Interrupción y reanudación: checkpointing de estado para approval asíncrono
#
# Cómo ejecutar:
#   make py SCRIPT=python/13-hitl/interrupcion.py
#
# Qué esperar:
#   El agente guarda checkpoints en SQLite antes de acciones de riesgo.
#   Si el humano rechaza, el agente se puede reanudar desde el ultimo checkpoint.
#
# Variables de entorno:
#   MODEL — modelo a usar (default: claude-sonnet-4-6)

import json
import os
import sqlite3
import time
import uuid
from dataclasses import dataclass, field, asdict
from typing import Optional
import anthropic

cliente = anthropic.Anthropic()


@dataclass
class EstadoAgente:
    thread_id: str
    tarea: str
    mensajes: list
    paso_actual: int
    estado: str  # "en_progreso" | "esperando_aprobacion" | "completado" | "rechazado"
    accion_pendiente: Optional[dict] = None  # la acción que está esperando aprobación
    resultado_final: Optional[str] = None
    timestamp: float = field(default_factory=time.time)
    expira_en: Optional[float] = None


class Checkpointer:
    """Almacén de estado en SQLite — en producción usar PostgreSQL."""

    def __init__(self, db_path: str = ":memory:"):
        self.conn = sqlite3.connect(db_path, check_same_thread=False)
        self.conn.execute("""
            CREATE TABLE IF NOT EXISTS checkpoints (
                thread_id  TEXT PRIMARY KEY,
                estado_json TEXT,
                ts         REAL
            )
        """)
        self.conn.commit()

    def guardar(self, estado: EstadoAgente) -> str:
        self.conn.execute(
            "INSERT OR REPLACE INTO checkpoints (thread_id, estado_json, ts) VALUES (?, ?, ?)",
            (estado.thread_id, json.dumps(asdict(estado)), time.time()),
        )
        self.conn.commit()
        return estado.thread_id

    def restaurar(self, thread_id: str) -> Optional[EstadoAgente]:
        fila = self.conn.execute(
            "SELECT estado_json FROM checkpoints WHERE thread_id = ?",
            (thread_id,),
        ).fetchone()
        if not fila:
            return None
        datos = json.loads(fila[0])
        return EstadoAgente(**datos)

    def listar_pendientes(self) -> list[dict]:
        filas = self.conn.execute(
            "SELECT thread_id, ts FROM checkpoints WHERE estado_json LIKE '%esperando_aprobacion%'"
        ).fetchall()
        return [{"thread_id": f[0], "ts": f[1]} for f in filas]


# ─── Herramienta de ejemplo con riesgo ────────────────────────────────────────

HERRAMIENTAS = [
    {
        "name": "buscar_datos",
        "description": "Busca datos. Seguro, no requiere aprobación.",
        "input_schema": {
            "type": "object",
            "properties": {"query": {"type": "string"}},
            "required": ["query"],
        },
    },
    {
        "name": "borrar_registros",
        "description": "Borra registros de producción. REQUIERE APROBACIÓN HUMANA.",
        "input_schema": {
            "type": "object",
            "properties": {
                "tabla": {"type": "string"},
                "condicion": {"type": "string"},
                "registros_afectados": {"type": "integer"},
            },
            "required": ["tabla", "condicion", "registros_afectados"],
        },
    },
]

HERRAMIENTAS_ALTO_RIESGO = {"borrar_registros"}


def _ejecutar_herramienta_real(nombre: str, params: dict) -> str:
    if nombre == "buscar_datos":
        return f"Datos encontrados para '{params['query']}': 42 registros activos."
    if nombre == "borrar_registros":
        n = params.get("registros_afectados", 0)
        return f"[SIMULADO] {n} registros borrados de '{params['tabla']}'."
    return f"Herramienta '{nombre}' no reconocida."


# ─── Loop principal con interrupción ─────────────────────────────────────────

def ejecutar_o_interrumpir(
    tarea: str,
    thread_id: Optional[str],
    checkpointer: Checkpointer,
) -> EstadoAgente:
    """Ejecuta el agente. Si llega a una acción de alto riesgo, para y checkpointea."""

    if thread_id:
        estado = checkpointer.restaurar(thread_id)
        if estado and estado.estado == "esperando_aprobacion":
            # Alguien llamó a esta función sin reanudar — devolver estado actual
            return estado
    else:
        thread_id = str(uuid.uuid4())[:8]
        estado = EstadoAgente(
            thread_id=thread_id,
            tarea=tarea,
            mensajes=[{"role": "user", "content": tarea}],
            paso_actual=0,
            estado="en_progreso",
        )

    for _ in range(15):
        respuesta = cliente.messages.create(
            model=os.environ.get("MODEL", "claude-sonnet-4-6"),
            max_tokens=512,
            tools=HERRAMIENTAS,
            messages=estado.mensajes,
        )
        estado.mensajes.append({"role": "assistant", "content": [b.model_dump() for b in respuesta.content]})
        estado.paso_actual += 1

        if respuesta.stop_reason == "end_turn":
            texto = next((b.text for b in respuesta.content if hasattr(b, "text")), "")
            estado.estado = "completado"
            estado.resultado_final = texto
            checkpointer.guardar(estado)
            return estado

        if respuesta.stop_reason == "tool_use":
            tool_results = []
            for bloque in respuesta.content:
                if bloque.type != "tool_use":
                    continue

                if bloque.name in HERRAMIENTAS_ALTO_RIESGO:
                    # INTERRUPCIÓN: guardar estado y pedir aprobación
                    estado.estado = "esperando_aprobacion"
                    estado.accion_pendiente = {
                        "tool_use_id": bloque.id,
                        "nombre": bloque.name,
                        "params": bloque.input,
                    }
                    estado.expira_en = time.time() + 72 * 3600
                    checkpointer.guardar(estado)
                    print(f"\n[INTERRUPCION] Acción de alto riesgo detectada:")
                    print(f"  Herramienta: {bloque.name}")
                    print(f"  Parámetros: {bloque.input}")
                    print(f"  Thread ID para reanudar: {estado.thread_id}")
                    return estado

                # Acción segura — ejecutar directamente
                resultado = _ejecutar_herramienta_real(bloque.name, bloque.input)
                tool_results.append({
                    "type": "tool_result",
                    "tool_use_id": bloque.id,
                    "content": resultado,
                })

            if tool_results:
                estado.mensajes.append({"role": "user", "content": tool_results})

    estado.estado = "completado"
    estado.resultado_final = "[max iteraciones]"
    checkpointer.guardar(estado)
    return estado


def reanudar(
    thread_id: str,
    decision: str,  # "aprobar" | "rechazar"
    checkpointer: Checkpointer,
    motivo: str = "",
) -> EstadoAgente:
    """Reanuda el agente después de que el humano toma una decisión."""
    estado = checkpointer.restaurar(thread_id)
    if not estado:
        raise ValueError(f"Checkpoint {thread_id} no encontrado o expirado")

    if estado.estado != "esperando_aprobacion":
        raise ValueError(f"Thread {thread_id} no está esperando aprobación (estado={estado.estado})")

    accion = estado.accion_pendiente
    estado.accion_pendiente = None

    if decision == "rechazar":
        resultado_decision = f"Acción rechazada por el usuario. Motivo: {motivo or 'no especificado'}"
    else:
        resultado_decision = _ejecutar_herramienta_real(accion["nombre"], accion["params"])

    # Inyectar el resultado de la decisión y continuar
    estado.mensajes.append({
        "role": "user",
        "content": [{
            "type": "tool_result",
            "tool_use_id": accion["tool_use_id"],
            "content": resultado_decision,
        }],
    })
    estado.estado = "en_progreso"
    print(f"\n[REANUDACION] Thread {thread_id} | decisión={decision}")

    return ejecutar_o_interrumpir(estado.tarea, thread_id, checkpointer)


if __name__ == "__main__":
    cp = Checkpointer()

    print("=== Ejecución con interrupción ===")
    estado = ejecutar_o_interrumpir(
        tarea="Primero busca los usuarios inactivos, luego borra los que llevan más de 2 años sin actividad.",
        thread_id=None,
        checkpointer=cp,
    )
    print(f"\nEstado: {estado.estado}")

    if estado.estado == "esperando_aprobacion":
        print("\n=== Simulando aprobación humana ===")
        estado_final = reanudar(
            thread_id=estado.thread_id,
            decision="aprobar",
            checkpointer=cp,
        )
        print(f"\nEstado final: {estado_final.estado}")
        print(f"Resultado: {estado_final.resultado_final}")
