# Tracing con OpenTelemetry: spans por llamada LLM y por tool call
#
# Cómo ejecutar:
#   make py SCRIPT=python/14-observabilidad/tracing.py
#
# Qué esperar:
#   Spans anidados por llamada LLM y por tool call con correlation IDs.
#   Output compatible con OpenTelemetry (exportable a Jaeger, Zipkin, etc).
#
# Variables de entorno:
#   MODEL — modelo a usar (default: claude-sonnet-4-6)

import os
import time
import uuid
from dataclasses import dataclass, field
from typing import Any, Optional
import anthropic

MODEL = os.environ.get("MODEL", "claude-haiku-4-5-20251001")
cliente = anthropic.Anthropic()


# ─── Span simplificado (sin dependencia de OTel para demo) ───────────────────

@dataclass
class Span:
    nombre: str
    trace_id: str
    span_id: str = field(default_factory=lambda: uuid.uuid4().hex[:16])
    parent_id: Optional[str] = None
    atributos: dict = field(default_factory=dict)
    inicio_ms: float = field(default_factory=lambda: time.time() * 1000)
    fin_ms: Optional[float] = None

    def set_attribute(self, key: str, value: Any) -> None:
        self.atributos[key] = value

    def end(self) -> None:
        self.fin_ms = time.time() * 1000

    @property
    def duracion_ms(self) -> float:
        if self.fin_ms is None:
            return 0.0
        return self.fin_ms - self.inicio_ms


class Tracer:
    def __init__(self, nombre: str) -> None:
        self.nombre = nombre
        self._spans: list[Span] = []
        self._activo: Optional[Span] = None

    def start_span(self, nombre: str, trace_id: Optional[str] = None) -> Span:
        tid = trace_id or (self._activo.trace_id if self._activo else uuid.uuid4().hex)
        parent = self._activo.span_id if self._activo else None
        span = Span(nombre=nombre, trace_id=tid, parent_id=parent)
        self._spans.append(span)
        self._activo = span
        return span

    def end_span(self, span: Span) -> None:
        span.end()
        # restaurar parent como activo
        if span.parent_id:
            for s in reversed(self._spans):
                if s.span_id == span.parent_id:
                    self._activo = s
                    break
        else:
            self._activo = None

    def report(self) -> None:
        print("\n─── Trace report ───")
        for s in self._spans:
            indent = "  " if s.parent_id else ""
            print(f"{indent}[{s.nombre}] {s.duracion_ms:.0f}ms | {s.atributos}")


tracer = Tracer("agente")


# ─── Herramientas de demo ────────────────────────────────────────────────────

TOOLS = [
    {
        "name": "obtener_clima",
        "description": "Devuelve el clima actual de una ciudad.",
        "input_schema": {
            "type": "object",
            "properties": {"ciudad": {"type": "string"}},
            "required": ["ciudad"],
        },
    }
]


def ejecutar_herramienta(nombre: str, params: dict) -> str:
    time.sleep(0.05)  # simula latencia de red
    if nombre == "obtener_clima":
        return f"El clima en {params['ciudad']} es soleado, 22°C."
    return f"Herramienta '{nombre}' no reconocida."


# ─── Agente con tracing manual ───────────────────────────────────────────────

def ejecutar_agente(tarea: str, thread_id: str) -> str:
    span_raiz = tracer.start_span("agent.run")
    span_raiz.set_attribute("thread_id", thread_id)
    span_raiz.set_attribute("tarea", tarea[:200])
    span_raiz.set_attribute("gen_ai.request.model", MODEL)

    mensajes: list[dict] = [{"role": "user", "content": tarea}]
    tokens_totales = 0
    step = 0

    try:
        for _ in range(10):
            span_llm = tracer.start_span("llm.call", trace_id=span_raiz.trace_id)
            span_llm.set_attribute("step", step)
            t0 = time.time()

            resp = cliente.messages.create(
                model=MODEL,
                max_tokens=512,
                tools=TOOLS,
                messages=mensajes,
            )
            latencia = (time.time() - t0) * 1000

            span_llm.set_attribute("gen_ai.usage.input_tokens", resp.usage.input_tokens)
            span_llm.set_attribute("gen_ai.usage.output_tokens", resp.usage.output_tokens)
            span_llm.set_attribute("gen_ai.response.finish_reason", resp.stop_reason)
            span_llm.set_attribute("latencia_ms", round(latencia, 1))
            tokens_totales += resp.usage.input_tokens + resp.usage.output_tokens
            tracer.end_span(span_llm)

            mensajes.append({"role": "assistant", "content": resp.content})

            if resp.stop_reason == "end_turn":
                break

            tool_results = []
            for bloque in resp.content:
                if bloque.type != "tool_use":
                    continue

                span_tool = tracer.start_span("tool.call", trace_id=span_raiz.trace_id)
                span_tool.set_attribute("tool.name", bloque.name)
                span_tool.set_attribute("tool.input", str(bloque.input)[:300])
                t0 = time.time()

                resultado = ejecutar_herramienta(bloque.name, bloque.input)
                ok = not resultado.startswith("Herramienta")

                span_tool.set_attribute("tool.latencia_ms", round((time.time() - t0) * 1000, 1))
                span_tool.set_attribute("tool.success", ok)
                tracer.end_span(span_tool)

                tool_results.append({
                    "type": "tool_result",
                    "tool_use_id": bloque.id,
                    "content": resultado,
                })

            mensajes.append({"role": "user", "content": tool_results})
            step += 1

        span_raiz.set_attribute("tokens_totales", tokens_totales)
        span_raiz.set_attribute("steps_totales", step + 1)
        texto = next((b.text for b in resp.content if hasattr(b, "text")), "")
        return texto

    finally:
        tracer.end_span(span_raiz)
        tracer.report()


if __name__ == "__main__":
    thread_id = uuid.uuid4().hex
    print("=== Agente con tracing ===")
    resultado = ejecutar_agente("¿Qué tiempo hace en Madrid hoy?", thread_id)
    print(f"\nRespuesta: {resultado[:300]}")
