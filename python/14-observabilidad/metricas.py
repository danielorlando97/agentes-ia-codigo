# Métricas de agentes: task completion rate, latencia P50/P95, cost per task
#
# Cómo ejecutar:
#   make py SCRIPT=python/14-observabilidad/metricas.py
#
# Qué esperar:
#   Calcula task completion rate, latencia P50/P95, costo por tarea.
#   Simula una sesion de agente y muestra el dashboard de metricas.
#
# Variables de entorno:
#   MODEL — modelo a usar (default: claude-sonnet-4-6)

import os
import time
import uuid
from dataclasses import dataclass, field
from typing import Optional
import anthropic

MODEL = os.environ.get("MODEL", "claude-haiku-4-5-20251001")
PRECIO_INPUT_POR_MTOK = 0.80    # claude-haiku-4-5-20251001, USD por millón de tokens
PRECIO_OUTPUT_POR_MTOK = 4.00

cliente = anthropic.Anthropic()


# ─── Resultado de una tarea ───────────────────────────────────────────────────

@dataclass
class ResultadoTarea:
    task_id: str
    completada: bool
    latencia_ms: float
    input_tokens: int
    output_tokens: int
    tool_calls_exitosos: int = 0
    tool_calls_fallidos: int = 0
    error: Optional[str] = None

    @property
    def coste_usd(self) -> float:
        return (
            self.input_tokens * PRECIO_INPUT_POR_MTOK / 1_000_000
            + self.output_tokens * PRECIO_OUTPUT_POR_MTOK / 1_000_000
        )


# ─── Agregador de métricas ───────────────────────────────────────────────────

@dataclass
class MetricasAgente:
    _resultados: list[ResultadoTarea] = field(default_factory=list)

    def registrar(self, resultado: ResultadoTarea) -> None:
        self._resultados.append(resultado)

    def resumen(self) -> dict:
        r = self._resultados
        if not r:
            return {}

        total = len(r)
        completadas = sum(1 for t in r if t.completada)
        latencias = sorted(t.latencia_ms for t in r)
        n = len(latencias)
        costes = [t.coste_usd for t in r]
        tool_ok = sum(t.tool_calls_exitosos for t in r)
        tool_err = sum(t.tool_calls_fallidos for t in r)

        return {
            "task_completion_rate": completadas / total,
            "error_rate": (total - completadas) / total,
            "latencia_p50_ms": latencias[n // 2],
            "latencia_p95_ms": latencias[int(n * 0.95)],
            "cost_per_task_usd": sum(costes) / total,
            "cost_total_usd": sum(costes),
            "tool_success_rate": tool_ok / (tool_ok + tool_err) if (tool_ok + tool_err) > 0 else 1.0,
            "total_tareas": total,
        }

    def alertas(self, umbral_completion: float = 0.95, umbral_p95_ms: float = 30_000) -> list[str]:
        s = self.resumen()
        problemas = []
        if s.get("task_completion_rate", 1) < umbral_completion:
            problemas.append(
                f"task_completion_rate {s['task_completion_rate']:.1%} < {umbral_completion:.0%}"
            )
        if s.get("latencia_p95_ms", 0) > umbral_p95_ms:
            problemas.append(
                f"P95 latencia {s['latencia_p95_ms']:.0f}ms > {umbral_p95_ms:.0f}ms"
            )
        return problemas


# ─── Agente de demo ──────────────────────────────────────────────────────────

TOOLS = [
    {
        "name": "calcular",
        "description": "Evalúa una expresión matemática simple.",
        "input_schema": {
            "type": "object",
            "properties": {"expresion": {"type": "string"}},
            "required": ["expresion"],
        },
    }
]


def ejecutar_herramienta(nombre: str, params: dict) -> tuple[str, bool]:
    if nombre == "calcular":
        try:
            resultado = eval(params["expresion"], {"__builtins__": {}})  # noqa: S307
            return str(resultado), True
        except Exception as e:
            return str(e), False
    return "desconocida", False


def ejecutar_tarea_con_metricas(tarea: str) -> ResultadoTarea:
    task_id = uuid.uuid4().hex[:8]
    t0 = time.time()
    input_tokens = 0
    output_tokens = 0
    tool_ok = 0
    tool_err = 0

    mensajes: list[dict] = [{"role": "user", "content": tarea}]

    try:
        for _ in range(10):
            resp = cliente.messages.create(
                model=MODEL,
                max_tokens=256,
                tools=TOOLS,
                messages=mensajes,
            )
            input_tokens += resp.usage.input_tokens
            output_tokens += resp.usage.output_tokens
            mensajes.append({"role": "assistant", "content": resp.content})

            if resp.stop_reason == "end_turn":
                latencia = (time.time() - t0) * 1000
                return ResultadoTarea(
                    task_id=task_id,
                    completada=True,
                    latencia_ms=latencia,
                    input_tokens=input_tokens,
                    output_tokens=output_tokens,
                    tool_calls_exitosos=tool_ok,
                    tool_calls_fallidos=tool_err,
                )

            tool_results = []
            for bloque in resp.content:
                if bloque.type != "tool_use":
                    continue
                resultado, ok = ejecutar_herramienta(bloque.name, bloque.input)
                if ok:
                    tool_ok += 1
                else:
                    tool_err += 1
                tool_results.append({
                    "type": "tool_result",
                    "tool_use_id": bloque.id,
                    "content": resultado,
                })
            mensajes.append({"role": "user", "content": tool_results})

        latencia = (time.time() - t0) * 1000
        return ResultadoTarea(
            task_id=task_id,
            completada=False,
            latencia_ms=latencia,
            input_tokens=input_tokens,
            output_tokens=output_tokens,
            tool_calls_exitosos=tool_ok,
            tool_calls_fallidos=tool_err,
            error="max iteraciones",
        )

    except Exception as e:
        latencia = (time.time() - t0) * 1000
        return ResultadoTarea(
            task_id=task_id,
            completada=False,
            latencia_ms=latencia,
            input_tokens=input_tokens,
            output_tokens=output_tokens,
            error=str(e)[:200],
        )


if __name__ == "__main__":
    print("=== Métricas de agente ===\n")
    metricas = MetricasAgente()

    TAREAS = [
        "¿Cuánto es 15 * 23?",
        "Calcula la raíz cuadrada de 144.",
        "¿Cuántos días tiene un año normal?",
    ]

    for tarea in TAREAS:
        print(f"Ejecutando: {tarea[:60]}")
        resultado = ejecutar_tarea_con_metricas(tarea)
        metricas.registrar(resultado)
        estado = "✓" if resultado.completada else "✗"
        print(f"  [{estado}] {resultado.latencia_ms:.0f}ms | ${resultado.coste_usd:.5f}")

    print("\n─── Resumen de métricas ───")
    s = metricas.resumen()
    for k, v in s.items():
        if isinstance(v, float):
            print(f"  {k}: {v:.3f}")
        else:
            print(f"  {k}: {v}")

    alertas = metricas.alertas()
    if alertas:
        print(f"\n[ALERTA] {alertas}")
    else:
        print("\n[OK] Todas las métricas dentro de umbral")
