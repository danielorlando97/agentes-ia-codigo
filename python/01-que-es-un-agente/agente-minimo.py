"""
Agente minimo — LLM + tools + loop hasta end_turn.

Qué demuestra:
    El loop de agente mas pequeno posible: el modelo recibe herramientas,
    llama las que necesita, y el codigo ejecuta los resultados y los
    devuelve hasta que el modelo termina (stop_reason == "end_turn").
    Este es el nivel "★★☆ Tool caller" del espectro de autonomia.

Patron clave:
    while True:
        response = llm(messages + tools)
        if end_turn: return text
        if tool_use: execute + append results + continue

Cómo ejecutar:
    make py SCRIPT=python/01-que-es-un-agente/agente-minimo.py

Qué esperar:
    El agente pregunta la hora en Tokio y calcula 47 + 89 usando tools,
    luego responde con ambos resultados en texto natural.

Variables de entorno:
    MODEL — modelo a usar (default: claude-sonnet-4-6)
"""
from datetime import datetime, timezone, timedelta
import os
import anthropic

MODEL = os.environ.get("MODEL", "claude-sonnet-4-6")
MAX_ITERATIONS = 10

TOOLS = [
    {
        "name": "get_time",
        "description": "Devuelve la hora actual en una zona horaria (offset UTC en horas).",
        "input_schema": {
            "type": "object",
            "properties": {"utc_offset": {"type": "number"}},
            "required": ["utc_offset"],
        },
    },
    {
        "name": "add",
        "description": "Suma dos numeros.",
        "input_schema": {
            "type": "object",
            "properties": {"a": {"type": "number"}, "b": {"type": "number"}},
            "required": ["a", "b"],
        },
    },
]


def execute_tool(name: str, args: dict) -> str:
    if name == "get_time":
        tz = timezone(timedelta(hours=args["utc_offset"]))
        return datetime.now(tz=tz).isoformat()
    if name == "add":
        return str(args["a"] + args["b"])
    return f"Tool '{name}' no existe"


def run_agent(task: str) -> str:
    client = anthropic.Anthropic()
    messages = [{"role": "user", "content": task}]

    for _ in range(MAX_ITERATIONS):
        response = client.messages.create(
            model=MODEL,
            max_tokens=1024,
            tools=TOOLS,
            messages=messages,
        )

        if response.stop_reason in ("end_turn", "stop_sequence"):
            return "".join(b.text for b in response.content if b.type == "text")

        if response.stop_reason == "tool_use":
            tool_results = []
            for block in response.content:
                if block.type == "tool_use":
                    tool_results.append({
                        "type": "tool_result",
                        "tool_use_id": block.id,
                        "content": execute_tool(block.name, block.input),
                    })
            messages.append({"role": "assistant", "content": response.content})
            messages.append({"role": "user", "content": tool_results})
            continue

        break

    return "[max iteraciones]"


if __name__ == "__main__":
    print(run_agent("Que hora es en Tokio (UTC+9), y cuanto es 47 + 89?"))
