"""
Costo y latencia — metricas de una sesion multi-turn con tool calls.

Qué demuestra:
    Instrumentacion completa de una sesion de agente para entender donde va
    el tiempo y el dinero. Mide en cada turno:
    - TTFT (Time To First Token): latencia percibida por el usuario
    - TPOT (Time Per Output Token): velocidad de streaming
    - Tokens de input/output: crecimiento del contexto turn a turn
    - Costo en USD por turno y total de la sesion

Patron de medicion:
    Usa streaming (client.messages.stream) para capturar TTFT sin cambiar
    el comportamiento del agente. El primer evento de texto marca el TTFT.

Insight clave sobre el overhead:
    Los tokens de input crecen en cada turno porque el historial acumula:
    - Tool schemas: se envian en CADA llamada, no solo una vez
    - Tool results: los resultados se añaden al historial
    - Respuestas del asistente: texto completo en el historial
    En una sesion de 5 turnos con 3 herramientas, el overhead puede ser 5-10x.

Cómo ejecutar:
    make py SCRIPT=python/03-motor-llm/costo-latencia.py

Qué esperar:
    Metricas por turno en formato tabla + resumen de costo total.
    La tarea (3 calculos encadenados) usa 3-4 turnos normalmente.

Variables de entorno:
    MODEL        — modelo principal (default: claude-sonnet-4-6)
    SMALL_MODEL  — modelo para la sesion demo (default: claude-haiku-4-5-20251001)
"""
import os
import json
import time

import anthropic

MODEL = os.environ.get("MODEL", "claude-sonnet-4-6")
SMALL_MODEL = os.environ.get("SMALL_MODEL", "claude-haiku-4-5-20251001")

# Precios Haiku 4.5 (USD por millón de tokens, Mayo 2025)
PRECIO_INPUT  = 0.80   # USD por millón de tokens de input
PRECIO_OUTPUT = 4.00   # USD por millón de tokens de output

# Herramienta calculadora mock
HERRAMIENTAS = [
    {
        "name": "calcular",
        "description": (
            "Realiza operaciones matemáticas. "
            "Operaciones disponibles: suma, resta, multiplicacion, division, potencia."
        ),
        "input_schema": {
            "type": "object",
            "properties": {
                "operacion": {
                    "type": "string",
                    "enum": ["suma", "resta", "multiplicacion", "division", "potencia"],
                },
                "a": {"type": "number", "description": "Primer operando"},
                "b": {"type": "number", "description": "Segundo operando"},
            },
            "required": ["operacion", "a", "b"],
        },
    }
]


# ─── 1. Ejecutor de herramienta mock ─────────────────────────────────────────

def ejecutar_calculadora(operacion: str, a, b) -> str:
    try:
        a, b = float(a), float(b)
    except (TypeError, ValueError):
        return json.dumps({"error": f"argumentos no numéricos: a={a!r}, b={b!r}"})
    resultados = {
        "suma":          a + b,
        "resta":         a - b,
        "multiplicacion": a * b,
        "division":      a / b if b != 0 else float("inf"),
        "potencia":      a ** b,
    }
    resultado = resultados.get(operacion, "operación desconocida")
    return json.dumps({"resultado": resultado, "operacion": operacion, "a": a, "b": b})


# ─── 2. Métricas por turno ────────────────────────────────────────────────────

class MetricasTurno:
    def __init__(self, turno: int):
        self.turno = turno
        self.ttft_s: float = 0.0
        self.latencia_total_s: float = 0.0
        self.tokens_input: int = 0
        self.tokens_output: int = 0
        self.tpot_ms: float = 0.0   # milliseconds per output token
        self.tool_calls: int = 0

    @property
    def costo_usd(self) -> float:
        return (
            self.tokens_input  / 1_000_000 * PRECIO_INPUT
            + self.tokens_output / 1_000_000 * PRECIO_OUTPUT
        )


def llamar_con_metricas(
    client: anthropic.Anthropic,
    mensajes: list[dict],
    turno: int,
) -> tuple[anthropic.types.Message, MetricasTurno]:
    """Llama a la API midiendo TTFT y latencia total vía streaming."""
    metricas = MetricasTurno(turno)

    t_inicio = time.perf_counter()
    ttft_capturado = False

    with client.messages.stream(
        model=SMALL_MODEL,
        max_tokens=512,
        tools=HERRAMIENTAS,
        messages=mensajes,
    ) as stream:
        for _ in stream.text_stream:
            if not ttft_capturado:
                metricas.ttft_s = time.perf_counter() - t_inicio
                ttft_capturado = True

        final_msg = stream.get_final_message()

    metricas.latencia_total_s = time.perf_counter() - t_inicio
    metricas.tokens_input  = final_msg.usage.input_tokens
    metricas.tokens_output = final_msg.usage.output_tokens

    if metricas.tokens_output > 0 and metricas.latencia_total_s > 0:
        metricas.tpot_ms = (metricas.latencia_total_s * 1000) / metricas.tokens_output

    metricas.tool_calls = sum(
        1 for b in final_msg.content if b.type == "tool_use"
    )

    return final_msg, metricas


# ─── 3. Sesión multi-turn con tool calls ─────────────────────────────────────

TAREA = (
    "Necesito resolver un problema en tres pasos:\n"
    "1. Calcula 347 × 89\n"
    "2. Al resultado anterior, réstale 5000\n"
    "3. Eleva el resultado al cuadrado\n"
    "Muéstrame los tres resultados."
)

def ejecutar_sesion_multiturn() -> list[MetricasTurno]:
    client = anthropic.Anthropic()
    mensajes: list[dict] = [{"role": "user", "content": TAREA}]
    metricas_sesion: list[MetricasTurno] = []
    turno = 1

    print("\n[sesión multi-turn con tool calls]")
    print(f"  Tarea: {TAREA[:80]!r}...\n")

    while turno <= 10:  # límite de seguridad
        print(f"  --- Turno {turno} ---")
        resp, metricas = llamar_con_metricas(client, mensajes, turno)
        metricas_sesion.append(metricas)

        print(f"  TTFT={metricas.ttft_s:.3f}s  "
              f"total={metricas.latencia_total_s:.3f}s  "
              f"TPOT={metricas.tpot_ms:.1f}ms/tok  "
              f"in={metricas.tokens_input}  out={metricas.tokens_output}  "
              f"tool_calls={metricas.tool_calls}  "
              f"costo=${metricas.costo_usd:.6f}")

        # Añadir respuesta del asistente al historial
        mensajes.append({"role": "assistant", "content": resp.content})

        # Procesar tool calls si hay
        tool_uses = [b for b in resp.content if b.type == "tool_use"]
        if not tool_uses:
            # No hay más tool calls → la sesión terminó
            texto_final = "".join(b.text for b in resp.content if b.type == "text")
            print(f"\n  Respuesta final: {texto_final[:200]!r}")
            break

        # Ejecutar herramientas y añadir resultados
        resultados_tools = []
        for tool_use in tool_uses:
            args = tool_use.input
            resultado = ejecutar_calculadora(
                args["operacion"], args["a"], args["b"]
            )
            print(f"  Tool: calcular({args['operacion']}, {args['a']}, {args['b']}) → {resultado}")
            resultados_tools.append({
                "type": "tool_result",
                "tool_use_id": tool_use.id,
                "content": resultado,
            })

        mensajes.append({"role": "user", "content": resultados_tools})
        turno += 1

    return metricas_sesion


# ─── 4. Tabla resumen de la sesión ───────────────────────────────────────────

def tabla_resumen_sesion(metricas_sesion: list[MetricasTurno]) -> None:
    print("\n[tabla resumen de la sesión]")

    # Cabecera
    header = (
        f"  {'Turno':>6}  {'TTFT(s)':>8}  {'Total(s)':>9}  "
        f"{'TPOT(ms)':>9}  {'In tok':>7}  {'Out tok':>8}  "
        f"{'Tool calls':>11}  {'Costo ($)':>10}"
    )
    sep = "  " + "-" * (len(header) - 2)
    print(header)
    print(sep)

    tokens_in_total  = 0
    tokens_out_total = 0
    costo_total      = 0.0

    for m in metricas_sesion:
        print(
            f"  {m.turno:>6}  {m.ttft_s:>8.3f}  {m.latencia_total_s:>9.3f}  "
            f"{m.tpot_ms:>9.1f}  {m.tokens_input:>7}  {m.tokens_output:>8}  "
            f"{m.tool_calls:>11}  {m.costo_usd:>10.6f}"
        )
        tokens_in_total  += m.tokens_input
        tokens_out_total += m.tokens_output
        costo_total      += m.costo_usd

    print(sep)
    latencia_total = sum(m.latencia_total_s for m in metricas_sesion)
    print(
        f"  {'TOTAL':>6}  {'':>8}  {latencia_total:>9.3f}  "
        f"{'':>9}  {tokens_in_total:>7}  {tokens_out_total:>8}  "
        f"{'':>11}  {costo_total:>10.6f}"
    )

    # Costo por tarea vs costo por token
    print(f"\n  Costo por tarea completa:   ${costo_total:.6f}")
    if tokens_out_total > 0:
        costo_por_token_output = costo_total / tokens_out_total * 1_000_000
        print(f"  Costo por token de output:  ${costo_por_token_output:.4f}/millón")
    if tokens_in_total > 0:
        costo_por_token_input = costo_total / tokens_in_total * 1_000_000
        print(f"  Costo por token de input:   ${costo_por_token_input:.4f}/millón")

    overhead_contexto = tokens_in_total - metricas_sesion[0].tokens_input if metricas_sesion else 0
    print(f"\n  Overhead de historial acumulado: {overhead_contexto} tokens extra de input")
    print(f"  (Los tool schemas se cuentan en cada turno)")


# ─── Main ──────────────────────────────────────────────────────────────────

if __name__ == "__main__":
    print("=== Costo y latencia: sesión multi-turn con tool calls ===")
    metricas = ejecutar_sesion_multiturn()
    tabla_resumen_sesion(metricas)
