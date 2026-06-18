"""
Loop con budget adaptativo — topes de tokens y tiempo de pared.

Qué demuestra:
    Reemplaza el max_iterations fijo por dos presupuestos mas precisos:
    1. BUDGET_TOKENS — tope de tokens totales consumidos por sesion
    2. BUDGET_SECONDS — tope de tiempo de pared (wall-clock)
    Esto protege contra costes desbocados en produccion sin bloquear tasks cortas.

Por qué no basta max_iterations:
    Una iteracion puede consumir 50 o 5000 tokens segun el contexto.
    max_iterations es ciego al coste. BUDGET_TOKENS es proporcional al gasto real.
    En produccion se usan los dos: max_iter como fallback, budget como tope principal.

Cómo ejecutar:
    make py SCRIPT=python/02-anatomia-minima/loop-budget.py

Qué esperar:
    Cada iteracion muestra tokens acumulados vs budget y tiempo transcurrido.
    La task es simple, no deberia llegar al limite; si lo hace, se muestra el mensaje.

Variables de entorno:
    MODEL          — modelo a usar (default: claude-sonnet-4-6)
    BUDGET_TOKENS  — se puede cambiar directamente en el codigo (200_000 default)
    BUDGET_SECONDS — idem (120s default)
"""
import os
import time
import anthropic
from datetime import datetime, timezone, timedelta

MODEL = os.environ.get("MODEL", "claude-sonnet-4-6")
BUDGET_TOKENS = 200_000   # tope absoluto de tokens por sesión
BUDGET_SECONDS = 120       # tope de wall-clock en segundos

TOOLS = [
    {
        "name": "get_time",
        "description": "Returns the current time in a timezone (UTC offset in hours).",
        "input_schema": {
            "type": "object",
            "properties": {"utc_offset": {"type": "number"}},
            "required": ["utc_offset"],
        },
    },
    {
        "name": "add",
        "description": "Sums two numbers.",
        "input_schema": {
            "type": "object",
            "properties": {"a": {"type": "number"}, "b": {"type": "number"}},
            "required": ["a", "b"],
        },
    },
]


def execute_tool(name: str, args: dict) -> str:
    try:
        if name == "get_time":
            tz = timezone(timedelta(hours=float(args["utc_offset"])))
            return datetime.now(tz=tz).isoformat()
        if name == "add":
            return str(float(args["a"]) + float(args["b"]))
        return f"Tool '{name}' desconocida"
    except Exception as e:
        return f"Error en {name}: {e}"


def run_budget_agent(task: str) -> str:
    """Loop con budget adaptativo: para antes de agotar tokens o tiempo."""
    client = anthropic.Anthropic()
    messages = [{"role": "user", "content": task}]

    consumed_tokens = 0
    start_time = time.monotonic()
    iteration = 0

    while True:
        iteration += 1
        elapsed = time.monotonic() - start_time

        # Verificar presupuestos ANTES de cada llamada
        if consumed_tokens >= BUDGET_TOKENS:
            return f"[budget agotado: {consumed_tokens} tokens en {iteration-1} iteraciones]"
        if elapsed >= BUDGET_SECONDS:
            return f"[timeout: {elapsed:.1f}s en {iteration-1} iteraciones]"

        response = client.messages.create(
            model=MODEL,
            max_tokens=4096,
            tools=TOOLS,
            messages=messages,
        )

        # Contabilizar tokens de esta llamada
        consumed_tokens += response.usage.input_tokens + response.usage.output_tokens
        print(
            f"  [iter={iteration}] stop={response.stop_reason} "
            f"tokens={consumed_tokens}/{BUDGET_TOKENS} "
            f"time={elapsed:.1f}s/{BUDGET_SECONDS}s"
        )

        if response.stop_reason in ("end_turn", "stop_sequence"):
            return "".join(b.text for b in response.content if b.type == "text")

        if response.stop_reason == "tool_use":
            tool_results = []
            for block in response.content:
                if block.type == "tool_use":
                    result = execute_tool(block.name, block.input)
                    tool_results.append({
                        "type": "tool_result",
                        "tool_use_id": block.id,
                        "content": result,
                    })
            messages.append({"role": "assistant", "content": response.content})
            messages.append({"role": "user", "content": tool_results})
            continue

        # stop_reason inesperado
        break

    return "[stop_reason inesperado]"


if __name__ == "__main__":
    result = run_budget_agent("¿Qué hora es en Tokio (UTC+9) y cuánto es 47 + 89?")
    print(f"\nRespuesta: {result}")
