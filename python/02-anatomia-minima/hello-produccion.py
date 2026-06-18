"""
Hello agent de produccion — version con system prompt, error handling y logging.

Qué demuestra:
    Evolucion del agente minimo hacia un agente funcional de produccion.
    Combina tres mejoras clave sobre el loop basico:
    1. System prompt con instrucciones explicitas de comportamiento
    2. Dispatcher con validacion de argumentos y captura de excepciones
    3. Logging estructurado para depuracion (modelo, iteraciones, tokens)

Por qué importa el dispatcher con errores:
    Si execute_tool() lanza una excepcion sin capturar, el agente muere.
    Si devuelve un string de error, el modelo puede re-intentar con args distintos.
    El patron "errors as strings" es clave para agentes robustos.

Cómo ejecutar:
    make py SCRIPT=python/02-anatomia-minima/hello-produccion.py

Qué esperar:
    Logs de cada iteracion con stop_reason, tokens usados y tool calls.
    Respuesta final con la hora en Tokio y el resultado de la suma.

Variables de entorno:
    MODEL — modelo a usar (default: claude-sonnet-4-6)
"""
import os
import logging
import anthropic
from datetime import datetime, timezone, timedelta

MODEL = os.environ.get("MODEL", "claude-sonnet-4-6")
MAX_ITERATIONS = 20

logging.basicConfig(level=logging.DEBUG, format="%(message)s")
log = logging.getLogger("agent")

SYSTEM = """Eres un asistente de productividad. Responde en español, sé conciso.
Cuando el usuario pida una hora, usa get_time con el offset UTC correcto.
Cuando el usuario pida una suma, usa add.
Si no puedes completar una tarea con las herramientas disponibles, dilo claramente.
"""

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
    """Dispatcher con validación y captura de excepciones."""
    try:
        if name == "get_time":
            offset_hours = float(args["utc_offset"])
            if not (-12 <= offset_hours <= 14):
                return f"Error: utc_offset {offset_hours} fuera de rango [-12, 14]"
            tz = timezone(timedelta(hours=offset_hours))
            return datetime.now(tz=tz).isoformat()
        if name == "add":
            return str(float(args["a"]) + float(args["b"]))
        return f"Error: herramienta '{name}' desconocida"
    except (KeyError, ValueError, TypeError) as e:
        return f"Error en {name}({args}): {e}"


def run_agent(task: str) -> str:
    """Loop con system prompt, error handling en dispatcher, y logging."""
    client = anthropic.Anthropic()
    messages = [{"role": "user", "content": task}]

    for iteration in range(MAX_ITERATIONS):
        response = client.messages.create(
            model=MODEL,
            max_tokens=2048,
            system=SYSTEM,
            tools=TOOLS,
            messages=messages,
        )

        log.debug(
            f"iter={iteration+1}/{MAX_ITERATIONS} "
            f"stop={response.stop_reason} "
            f"tokens={response.usage.input_tokens}+{response.usage.output_tokens}"
        )

        if response.stop_reason in ("end_turn", "stop_sequence"):
            return "".join(b.text for b in response.content if b.type == "text")

        if response.stop_reason == "tool_use":
            tool_results = []
            for block in response.content:
                if block.type == "tool_use":
                    result = execute_tool(block.name, block.input)
                    log.debug(f"  → {block.name}({block.input}) = {result}")
                    tool_results.append({
                        "type": "tool_result",
                        "tool_use_id": block.id,
                        "content": result,
                    })
            messages.append({"role": "assistant", "content": response.content})
            messages.append({"role": "user", "content": tool_results})
            continue

        log.warning(f"stop_reason inesperado: {response.stop_reason}")
        break

    return "[max iteraciones]"


if __name__ == "__main__":
    result = run_agent("¿Qué hora es en Tokio (UTC+9) y cuánto es 47 + 89?")
    print(f"\nRespuesta: {result}")
