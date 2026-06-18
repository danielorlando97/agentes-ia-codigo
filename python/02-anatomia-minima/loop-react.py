"""
Loop ReAct — razonamiento externalizado como Thought/Action/Observation.

Qué demuestra:
    Alternativa al loop nativo de tool_use: el modelo escribe su razonamiento
    como texto visible antes de cada accion. Util cuando:
    - El modelo no soporta tool_use nativo con buena calidad
    - Se necesita trazabilidad del pensamiento para depuracion
    - Se quiere que el modelo "piense en voz alta" antes de actuar

Diferencia con el loop basico (agente-minimo):
    Loop basico: stop_reason=tool_use senaliza la accion — el razonamiento queda
                 en los tokens internos del modelo, no es visible
    Loop ReAct:  el modelo escribe "Thought:" antes de cada Action — visible e
                 inspeccionable; facilita detectar errores de razonamiento

Cómo ejecutar:
    make py SCRIPT=python/02-anatomia-minima/loop-react.py

Qué esperar:
    Cada iteracion muestra la tool llamada y su resultado.
    La respuesta final aparece despues de "FINAL ANSWER:".

Variables de entorno:
    MODEL — modelo a usar (default: claude-sonnet-4-6)
"""
import json
import os
import re
import anthropic
from datetime import datetime, timezone, timedelta

MODEL = os.environ.get("MODEL", "claude-sonnet-4-6")
MAX_ITERATIONS = 15

REACT_SYSTEM = """You are a helpful assistant. For every step you MUST follow this format:

Thought: [reason about what you know and what to do next]
Action: [the single tool you will call — write the tool name and arguments as JSON]
Observation: [STOP — the system fills this in]

Rules:
- Never skip Thought.
- One Action per turn, never two.
- If the same Action fails twice, you MUST try a completely different approach.
- When you have the final answer, write: FINAL ANSWER: <answer>
"""

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


def _parse_text_actions(text: str) -> list[dict]:
    """Detecta tool calls escritas como JSON en texto (fallback para modelos locales)."""
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


def run_react_agent(task: str) -> str:
    """Loop ReAct: el razonamiento es texto visible, no tokens privados."""
    client = anthropic.Anthropic()
    messages = [{"role": "user", "content": task}]

    for iteration in range(MAX_ITERATIONS):
        response = client.messages.create(
            model=MODEL,
            max_tokens=2048,
            system=REACT_SYSTEM,
            tools=TOOLS,
            messages=messages,
        )

        if response.stop_reason in ("end_turn", "stop_sequence"):
            text = "".join(b.text for b in response.content if b.type == "text")
            # Modelos locales a veces escriben las acciones como JSON en texto.
            text_actions = _parse_text_actions(text)
            if text_actions and "FINAL ANSWER:" not in text:
                obs_parts = []
                for call in text_actions:
                    result = execute_tool(call["name"], call["input"])
                    print(f"  [iter={iteration+1}] {call['name']}({call['input']}) → {result}")
                    obs_parts.append(f"{call['name']} → {result}")
                messages.append({"role": "assistant", "content": text})
                messages.append({"role": "user", "content": "Resultados: " + "; ".join(obs_parts) + ". Ahora escribe FINAL ANSWER: con la respuesta."})
                continue
            if "FINAL ANSWER:" in text:
                return text.split("FINAL ANSWER:")[-1].strip()
            return text

        if response.stop_reason == "tool_use":
            tool_results = []
            for block in response.content:
                if block.type == "tool_use":
                    result = execute_tool(block.name, block.input)
                    print(f"  [iter={iteration+1}] {block.name}({block.input}) → {result}")
                    tool_results.append({
                        "type": "tool_result",
                        "tool_use_id": block.id,
                        "content": result,
                    })
            messages.append({"role": "assistant", "content": response.content})
            messages.append({"role": "user", "content": tool_results})
            continue

        break

    return "[max iteraciones]"


if __name__ == "__main__":
    result = run_react_agent("¿Qué hora es en Tokio (UTC+9) y cuánto es 47 + 89?")
    print(f"\nRespuesta: {result}")
