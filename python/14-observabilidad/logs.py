# Logging estructurado para agentes: campos obligatorios, correlation IDs, niveles
#
# Cómo ejecutar:
#   make py SCRIPT=python/14-observabilidad/logs.py
#
# Qué esperar:
#   Logs estructurados en JSON con campos obligatorios: trace_id, span_id,
#   timestamp, level, event. Correlation IDs para rastrear sesiones completas.
#
# Variables de entorno:
#   MODEL — modelo a usar (default: claude-sonnet-4-6)

import os
import json
import time
import uuid
from dataclasses import dataclass, field
from typing import Any, Optional
import anthropic

MODEL = os.environ.get("MODEL", "claude-haiku-4-5-20251001")
cliente = anthropic.Anthropic()


# ─── Logger estructurado mínimo (sin dependencia de structlog) ───────────────

class StructLogger:
    """Emite logs como JSON con campos de correlación fijos."""

    def __init__(self, **contexto: Any) -> None:
        self._ctx = contexto

    def bind(self, **extra: Any) -> "StructLogger":
        return StructLogger(**{**self._ctx, **extra})

    def _emit(self, nivel: str, evento: str, **campos: Any) -> None:
        registro = {
            "ts": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
            "nivel": nivel,
            "evento": evento,
            **self._ctx,
            **campos,
        }
        print(json.dumps(registro, ensure_ascii=False))

    def info(self, evento: str, **campos: Any) -> None:
        self._emit("INFO", evento, **campos)

    def error(self, evento: str, **campos: Any) -> None:
        self._emit("ERROR", evento, **campos)

    def warn(self, evento: str, **campos: Any) -> None:
        self._emit("WARN", evento, **campos)


_base_logger = StructLogger(agente_version="1.0.0", entorno="demo")


def crear_logger_sesion(thread_id: str, user_id: str, session_id: str) -> StructLogger:
    return _base_logger.bind(
        thread_id=thread_id,
        user_id=user_id,
        session_id=session_id,
    )


# ─── Herramientas de demo ────────────────────────────────────────────────────

TOOLS = [
    {
        "name": "buscar_info",
        "description": "Busca información sobre un tema.",
        "input_schema": {
            "type": "object",
            "properties": {"tema": {"type": "string"}},
            "required": ["tema"],
        },
    }
]


def ejecutar_herramienta(nombre: str, params: dict) -> tuple[str, bool]:
    time.sleep(0.03)
    if nombre == "buscar_info":
        return f"Información sobre {params['tema']}: dato relevante de ejemplo.", True
    return "Herramienta no reconocida.", False


# ─── Agente con logging estructurado ─────────────────────────────────────────

def ejecutar_agente(tarea: str, user_id: str) -> str:
    thread_id = uuid.uuid4().hex
    session_id = uuid.uuid4().hex
    log = crear_logger_sesion(thread_id, user_id, session_id)

    log.info("task.started", tarea=tarea[:200], modelo=MODEL)
    t_inicio = time.time()

    mensajes: list[dict] = [{"role": "user", "content": tarea}]
    step = 0
    tokens_input = 0
    tokens_output = 0

    try:
        for _ in range(10):
            log.info("llm.call.started", step=step, modelo=MODEL)
            t0 = time.time()

            try:
                resp = cliente.messages.create(
                    model=MODEL,
                    max_tokens=512,
                    tools=TOOLS,
                    messages=mensajes,
                )
                latencia = round((time.time() - t0) * 1000)
                tokens_input += resp.usage.input_tokens
                tokens_output += resp.usage.output_tokens
                log.info(
                    "llm.call.completed",
                    step=step,
                    input_tokens=resp.usage.input_tokens,
                    output_tokens=resp.usage.output_tokens,
                    finish_reason=resp.stop_reason,
                    latencia_ms=latencia,
                )
            except Exception as e:
                log.error(
                    "llm.call.failed",
                    step=step,
                    error_type=type(e).__name__,
                    error_msg=str(e)[:500],
                )
                raise

            mensajes.append({"role": "assistant", "content": resp.content})

            if resp.stop_reason == "end_turn":
                break

            tool_results = []
            for bloque in resp.content:
                if bloque.type != "tool_use":
                    continue

                log.info(
                    "tool.execution.started",
                    step=step,
                    tool=bloque.name,
                    params=str(bloque.input)[:300],
                )
                t0 = time.time()

                resultado, ok = ejecutar_herramienta(bloque.name, bloque.input)
                latencia_tool = round((time.time() - t0) * 1000)

                if ok:
                    log.info(
                        "tool.execution.completed",
                        step=step,
                        tool=bloque.name,
                        success=True,
                        latencia_ms=latencia_tool,
                    )
                else:
                    log.error(
                        "tool.execution.failed",
                        step=step,
                        tool=bloque.name,
                        error=resultado[:300],
                        latencia_ms=latencia_tool,
                    )

                tool_results.append({
                    "type": "tool_result",
                    "tool_use_id": bloque.id,
                    "content": resultado,
                })

            mensajes.append({"role": "user", "content": tool_results})
            step += 1

        texto = next((b.text for b in resp.content if hasattr(b, "text")), "")
        duracion = round((time.time() - t_inicio) * 1000)
        log.info(
            "task.completed",
            duracion_ms=duracion,
            steps=step + 1,
            tokens_input=tokens_input,
            tokens_output=tokens_output,
        )
        return texto

    except Exception as e:
        duracion = round((time.time() - t_inicio) * 1000)
        log.error(
            "task.failed",
            error_type=type(e).__name__,
            error_msg=str(e)[:500],
            duracion_ms=duracion,
            steps=step,
        )
        raise


if __name__ == "__main__":
    print("=== Logging estructurado ===\n")
    resultado = ejecutar_agente("¿Qué es la computación cuántica?", user_id="user_demo")
    print(f"\nRespuesta: {resultado[:300]}")
