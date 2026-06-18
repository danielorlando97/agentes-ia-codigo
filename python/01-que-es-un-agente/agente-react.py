"""
Agente ReAct — loop con razonamiento explicito (Thought → Action → Observation).

Qué demuestra:
    ReAct (Reasoning + Acting) obliga al modelo a externalizar su razonamiento
    como texto visible antes de cada tool call. Util para depuracion y para
    modelos que razonan mejor escribiendo "Thought:" explicitamente.
    Diferencia con agente-minimo: el trace muestra pensamiento + acciones.

Patron clave:
    System prompt instruye formato Thought/Action/Observation.
    El codigo captura texto de cada turno y lo muestra en el trace.
    stop_reason == "end_turn" con "Final answer:" indica conclusion.

Cómo ejecutar:
    make py SCRIPT=python/01-que-es-un-agente/agente-react.py

Qué esperar:
    Trace de razonamiento + acciones antes de la respuesta final.
    Mas verbose que agente-minimo, pero el proceso es inspeccionable.

Variables de entorno:
    MODEL — modelo a usar (default: claude-sonnet-4-6)
"""
from datetime import datetime, timezone, timedelta
import json
import re
import os
import anthropic

MODEL = os.environ.get("MODEL", "claude-sonnet-4-6")
MAX_ITERATIONS = 10

SYSTEM = (
    "Eres un agente ReAct. Antes de cada llamada a herramienta escribe una linea "
    "que empiece por 'Thought:' explicando tu razonamiento; luego usa la herramienta. "
    "Cuando tengas la respuesta final, escribela despues de un 'Final answer:'."
)

# Algunos modelos locales (ej: llama3.1 via Ollama) escriben las acciones como
# texto JSON en vez de usar tool_use blocks. Este parser los detecta y ejecuta.
def _parse_text_actions(text: str) -> list[dict]:
    """Extrae llamadas a herramientas descritas como JSON en el texto del modelo."""
    decoder = json.JSONDecoder()
    calls = []
    i = 0
    while i < len(text):
        i = text.find("{", i)
        if i == -1:
            break
        try:
            obj, end = decoder.raw_decode(text, i)
            if isinstance(obj, dict):
                name = obj.get("name") or obj.get("tool")
                args = obj.get("parameters") or obj.get("arguments") or obj.get("input") or {}
                if name and isinstance(args, dict):
                    calls.append({"name": name, "input": args})
            i = end
        except (json.JSONDecodeError, ValueError):
            i += 1
    return calls

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


def run_react(task: str) -> str:
    client = anthropic.Anthropic()
    messages = [{"role": "user", "content": task}]
    trace: list[str] = []

    for _ in range(MAX_ITERATIONS):
        response = client.messages.create(
            model=MODEL,
            max_tokens=1024,
            system=SYSTEM,
            tools=TOOLS,
            messages=messages,
        )

        for block in response.content:
            if block.type == "text" and block.text.strip():
                trace.append(block.text.strip())
            elif block.type == "tool_use":
                trace.append(f"Action: {block.name}({block.input})")

        if response.stop_reason == "tool_use":
            tool_results = []
            for block in response.content:
                if block.type == "tool_use":
                    out = execute_tool(block.name, block.input)
                    trace.append(f"Observation: {out}")
                    tool_results.append({
                        "type": "tool_result",
                        "tool_use_id": block.id,
                        "content": out,
                    })
            messages.append({"role": "assistant", "content": response.content})
            messages.append({"role": "user", "content": tool_results})
            continue

        if response.stop_reason in ("end_turn", "stop_sequence"):
            # Modelos locales a veces escriben acciones como texto JSON en vez de
            # usar tool_use blocks. Si encontramos acciones en el texto, las ejecutamos.
            full_text = " ".join(b.text for b in response.content if b.type == "text")
            text_actions = _parse_text_actions(full_text)
            if text_actions:
                obs_parts = []
                for call in text_actions:
                    out = execute_tool(call["name"], call["input"])
                    trace.append(f"Action: {call['name']}({call['input']})")
                    trace.append(f"Observation: {out}")
                    obs_parts.append(f"{call['name']} → {out}")
                messages.append({"role": "assistant", "content": full_text})
                messages.append({"role": "user", "content": "Resultados: " + "; ".join(obs_parts) + ". Ahora da la respuesta final."})
                continue
            return "\n".join(trace)

        break

    return "\n".join(trace + ["[max iteraciones]"])


if __name__ == "__main__":
    print(run_react("Que hora es en Tokio (UTC+9), y cuanto es 47 + 89?"))
