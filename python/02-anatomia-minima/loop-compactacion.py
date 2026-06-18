"""
Loop con compactacion de contexto — sesiones largas sin agotar la ventana.

Qué demuestra:
    Cuando el historial supera CONTEXT_THRESHOLD tokens, un modelo ligero
    (COMPACT_MODEL) comprime los mensajes intermedios en un resumen.
    Permite sesiones de horas sin llegar al limite de la ventana de contexto.

Estrategia de compactacion:
    - Conserva SIEMPRE: primeros 2 mensajes (tarea original) + ultimos 6 (estado reciente)
    - Comprime: todo lo intermedio en un resumen que preserva tool calls y decisiones
    - Usa COMPACT_MODEL (haiku) para comprimir, no el modelo principal — ahorra costo

Por qué importa la estructura conservada:
    Si se pierde la tarea original, el modelo pierde el objetivo.
    Si se pierden los ultimos mensajes, el modelo pierde el contexto inmediato.
    Solo se puede comprimir el "medio" con seguridad.

Cómo ejecutar:
    make py SCRIPT=python/02-anatomia-minima/loop-compactacion.py

Qué esperar:
    Logs de cada iteracion con estimacion de tokens. En tasks cortas no hay
    compactacion; para ver la compactacion reducir CONTEXT_THRESHOLD.

Variables de entorno:
    MODEL         — modelo principal (default: claude-sonnet-4-6)
    COMPACT_MODEL — modelo de compactacion (default: claude-haiku-4-5-20251001)
"""
import os
import json
import anthropic
from datetime import datetime, timezone, timedelta

MODEL = os.environ.get("MODEL", "claude-sonnet-4-6")
COMPACT_MODEL = os.environ.get("COMPACT_MODEL", "claude-haiku-4-5-20251001")   # modelo barato para compactar
CONTEXT_THRESHOLD = 40_000   # tokens; umbral conservador para este ejemplo
MAX_ITERATIONS = 50

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


def estimate_tokens(messages: list) -> int:
    """Estimación rápida: ~4 chars por token."""
    return sum(len(json.dumps(m)) for m in messages) // 4


def compact(client: anthropic.Anthropic, messages: list) -> list:
    """Comprime el historial intermedio en un resumen.

    Conserva los primeros 2 mensajes (tarea original) y los últimos 6 (estado reciente).
    El intermedio se resume en una llamada al modelo barato.
    """
    if len(messages) <= 8:
        return messages

    first = messages[:2]     # tarea original — siempre conservada
    recent = messages[-6:]   # estado reciente — siempre conservado
    to_compress = messages[2:-6]

    if not to_compress:
        return messages

    print(f"  [compactación] comprimiendo {len(to_compress)} mensajes intermedios...")

    summary_response = client.messages.create(
        model=COMPACT_MODEL,
        max_tokens=1500,
        messages=[{
            "role": "user",
            "content": (
                "Resume este historial de un agente. Preserva exactamente:\n"
                "- Cada herramienta llamada y su resultado\n"
                "- Cada archivo leído o modificado\n"
                "- Cada decisión tomada y por qué\n"
                "- El estado actual de la tarea\n\n"
                f"Historial: {json.dumps(to_compress)[:15000]}"
            ),
        }]
    )

    summary_text = summary_response.content[0].text
    compressed = {"role": "user", "content": f"[HISTORIAL COMPRIMIDO]\n{summary_text}\n[FIN]"}
    return first + [compressed] + recent


def run_compact_agent(task: str) -> str:
    """Loop con compactación automática cuando el contexto crece."""
    client = anthropic.Anthropic()
    messages = [{"role": "user", "content": task}]

    for iteration in range(MAX_ITERATIONS):
        # Compactar si el contexto supera el umbral
        current_tokens = estimate_tokens(messages)
        if current_tokens > CONTEXT_THRESHOLD:
            messages = compact(client, messages)
            print(f"  [iter={iteration+1}] contexto compactado → ~{estimate_tokens(messages)} tokens")
        else:
            print(f"  [iter={iteration+1}] contexto ~{current_tokens} tokens")

        response = client.messages.create(
            model=MODEL,
            max_tokens=4096,
            tools=TOOLS,
            messages=messages,
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
            messages.append({"role": "assistant", "content": [b.model_dump() for b in response.content]})
            messages.append({"role": "user", "content": tool_results})
            continue

        break

    return "[max iteraciones]"


if __name__ == "__main__":
    result = run_compact_agent("¿Qué hora es en Tokio (UTC+9) y cuánto es 47 + 89?")
    print(f"\nRespuesta: {result}")
