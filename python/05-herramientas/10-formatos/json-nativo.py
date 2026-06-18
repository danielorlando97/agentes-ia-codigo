# Tool calling con JSON nativo (Anthropic).
#
# El formato Anthropic serializa la llamada como un bloque tool_use con
# `input` como objeto ya parseado (no string JSON). El resultado vuelve
# como tool_result en un mensaje de role "user".
#
# Diferencias clave vs OpenAI Chat Completions:
#   Anthropic: stop_reason="tool_use", input=objeto, role="user", is_error
#   OpenAI:    finish_reason="tool_calls", arguments=string, role="tool", sin is_error
#
# Cómo ejecutar:
#   make py SCRIPT=python/05-herramientas/10-formatos/json-nativo.py
#
# Qué esperar:
#   El modelo llama una herramienta y el input llega ya parseado como dict.
#   No hay serialización/deserialización manual — el SDK lo maneja.
#
# Variables de entorno:
#   MODEL — modelo a usar (default: claude-sonnet-4-6)

import os
import json
import anthropic

MODEL = os.environ.get("MODEL", "claude-haiku-4-5-20251001")
client = anthropic.Anthropic()

# --- Definición de tools ---

TOOLS = [
    {
        "name": "get_weather",
        "description": (
            "Get current weather for a city. "
            "Use when the user asks about weather conditions, temperature, or forecast. "
            "Do NOT use for historical weather — use get_weather_history instead."
        ),
        "input_schema": {
            "type": "object",
            "properties": {
                "location": {
                    "type": "string",
                    "description": "City and country, e.g. 'Madrid, Spain'",
                },
                "unit": {
                    "type": "string",
                    "enum": ["celsius", "fahrenheit"],
                    "description": "Temperature unit. Default: celsius.",
                },
            },
            "required": ["location"],
            "additionalProperties": False,
        },
    },
    {
        "name": "get_time",
        "description": "Get current local time for a timezone or city.",
        "input_schema": {
            "type": "object",
            "properties": {
                "timezone": {
                    "type": "string",
                    "description": "IANA timezone string, e.g. 'Europe/Madrid'",
                },
            },
            "required": ["timezone"],
            "additionalProperties": False,
        },
    },
]


# --- Mock de ejecución ---

def ejecutar_herramienta(nombre: str, args: dict) -> str:
    if nombre == "get_weather":
        return json.dumps({
            "location": args["location"],
            "temperature": 22,
            "unit": args.get("unit", "celsius"),
            "conditions": "parcialmente nublado",
        })
    if nombre == "get_time":
        return json.dumps({"timezone": args["timezone"], "local_time": "14:35:00"})
    return json.dumps({"error": f"herramienta desconocida: {nombre}"})


# --- Loop de tool use ---

def tool_use_loop(pregunta: str) -> str:
    mensajes = [{"role": "user", "content": pregunta}]

    for _ in range(10):  # límite de seguridad
        resp = client.messages.create(
            model=MODEL,
            max_tokens=1024,
            tools=TOOLS,
            messages=mensajes,
        )

        if resp.stop_reason == "end_turn":
            return "".join(b.text for b in resp.content if b.type == "text")

        if resp.stop_reason == "tool_use":
            # Añadir respuesta del asistente (puede incluir texto + tool_use blocks)
            mensajes.append({"role": "assistant", "content": resp.content})

            # Ejecutar todas las tool calls del turno (pueden ser paralelas)
            resultados = []
            for bloque in resp.content:
                if bloque.type == "tool_use":
                    # input es un objeto Python ya parseado, no un string
                    resultado = ejecutar_herramienta(bloque.name, bloque.input)
                    print(f"  → {bloque.name}({bloque.input}) = {resultado[:60]}")
                    resultados.append({
                        "type": "tool_result",
                        "tool_use_id": bloque.id,   # mismo ID del tool_use block
                        "content": resultado,
                        "is_error": False,           # campo exclusivo de Anthropic
                    })

            # Todos los resultados en UN solo mensaje de role "user"
            mensajes.append({"role": "user", "content": resultados})

    return "[límite de pasos alcanzado]"


def main() -> None:
    print("=== Tool calling JSON nativo (Anthropic) ===\n")

    # Caso 1: tool call simple
    print("Pregunta: ¿Qué tiempo hace en Madrid?")
    respuesta = tool_use_loop("¿Qué tiempo hace en Madrid?")
    print(f"Respuesta: {respuesta}\n")

    # Caso 2: parallel tool calls — el modelo genera múltiples bloques en un turno
    print("Pregunta: ¿Qué tiempo y hora es en Tokio?")
    respuesta = tool_use_loop("¿Qué tiempo y hora es en Tokio ahora mismo?")
    print(f"Respuesta: {respuesta}\n")


if __name__ == "__main__":
    main()
