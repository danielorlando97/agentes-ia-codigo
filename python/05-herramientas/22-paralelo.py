# Tool calling paralelo.
#
# El modelo puede generar múltiples bloques tool_use en un único turno.
# El ejecutor los corre concurrentemente con asyncio.gather y devuelve
# todos los tool_results en un único mensaje user.
#
# Regla crítica: todos los tool_results deben ir en un único mensaje user.
# Si se envían en mensajes separados, el modelo aprende a serializar
# tool calls en turnos futuros porque así "ve" que trabaja el sistema.
#
# Cómo ejecutar:
#   make py SCRIPT=python/05-herramientas/22-paralelo.py
#
# Qué esperar:
#   El modelo genera múltiples tool_use en un turno. El ejecutor los corre
#   concurrentemente con asyncio.gather y mide el tiempo ahorrado vs serial.
#
# Variables de entorno:
#   MODEL — modelo a usar (default: claude-sonnet-4-6)

import os
import json
import asyncio
import anthropic

MODEL = os.environ.get("MODEL", "claude-sonnet-4-6")


# --- Herramientas mock ---

async def mock_weather(city: str) -> dict:
    # Simula una llamada a API externa (~300ms)
    await asyncio.sleep(0.3)
    table = {
        "Madrid": {"temp": 24, "cond": "sunny"},
        "Paris": {"temp": 18, "cond": "cloudy"},
        "Tokyo": {"temp": 29, "cond": "humid"},
    }
    data = table.get(city, {"temp": 20, "cond": "unknown"})
    return {"city": city, "temp_c": data["temp"], "condition": data["cond"]}


async def mock_calculate(expression: str) -> dict:
    # Simula evaluación (~50ms)
    await asyncio.sleep(0.05)
    import re
    sanitized = re.sub(r"[^0-9+\-*/().\s]", "", expression)
    result = eval(sanitized)  # noqa: S307 — sanitized input
    return {"expression": expression, "result": result}


async def mock_search(query: str) -> dict:
    # Simula búsqueda (~400ms)
    await asyncio.sleep(0.4)
    return {
        "query": query,
        "hits": [
            f'Resultado 1 para "{query}"',
            f'Resultado 2 para "{query}"',
            f'Resultado 3 para "{query}"',
        ],
    }


# --- Definición de herramientas para el modelo ---

TOOLS = [
    {
        "name": "get_weather",
        "description": "Obtiene el clima actual de una ciudad.",
        "input_schema": {
            "type": "object",
            "properties": {
                "city": {"type": "string", "description": "Nombre de la ciudad"},
            },
            "required": ["city"],
        },
    },
    {
        "name": "calculate",
        "description": "Evalúa una expresión matemática y devuelve el resultado.",
        "input_schema": {
            "type": "object",
            "properties": {
                "expression": {
                    "type": "string",
                    "description": "Expresión matemática, e.g. '15 * 8 + 3'",
                },
            },
            "required": ["expression"],
        },
    },
    {
        "name": "search",
        "description": "Busca información sobre un tema.",
        "input_schema": {
            "type": "object",
            "properties": {
                "query": {"type": "string", "description": "Término de búsqueda"},
            },
            "required": ["query"],
        },
    },
]


# --- Ejecutor individual ---

async def ejecutar_tool(name: str, input_args: dict) -> object:
    if name == "get_weather":
        return await mock_weather(input_args["city"])
    if name == "calculate":
        return await mock_calculate(input_args["expression"])
    if name == "search":
        return await mock_search(input_args["query"])
    raise ValueError(f"Herramienta desconocida: {name}")


# --- Ejecutor paralelo ---

async def ejecutar_tools_paralelas(bloques: list) -> list:
    """
    Ejecuta todos los bloques tool_use concurrentemente con asyncio.gather.
    Devuelve todos los resultados listos para incluir en un único mensaje user.
    """
    t0 = asyncio.get_event_loop().time()

    async def ejecutar_uno(bloque) -> dict:
        try:
            resultado = await ejecutar_tool(bloque.name, bloque.input)
            return {
                "type": "tool_result",
                "tool_use_id": bloque.id,
                "content": json.dumps(resultado),
            }
        except Exception as e:
            return {
                "type": "tool_result",
                "tool_use_id": bloque.id,
                "content": f"{bloque.name}: {e}",
                "is_error": True,
            }

    # CLAVE: asyncio.gather ejecuta todas en paralelo
    resultados = await asyncio.gather(*[ejecutar_uno(b) for b in bloques])
    elapsed = (asyncio.get_event_loop().time() - t0) * 1000
    print(f"  [paralelo] {len(bloques)} tools → {elapsed:.0f}ms (max individual, no suma)")
    return list(resultados)


# --- Loop del agente ---

async def agent_loop(tarea: str) -> str:
    client = anthropic.Anthropic()
    messages = [{"role": "user", "content": tarea}]

    for iter_num in range(10):
        response = client.messages.create(
            model=MODEL,
            max_tokens=4096,
            tools=TOOLS,
            messages=messages,
        )

        tool_count = sum(1 for b in response.content if b.type == "tool_use")
        print(f"  [iter={iter_num + 1}] stop_reason={response.stop_reason}, tool_calls={tool_count}")

        if response.stop_reason == "end_turn":
            return "".join(b.text for b in response.content if b.type == "text")

        if response.stop_reason == "tool_use":
            bloques = [b for b in response.content if b.type == "tool_use"]

            # Ejecutar todos en paralelo
            tool_results = await ejecutar_tools_paralelas(bloques)

            # CORRECTO: todos los tool_results en UN solo mensaje user
            messages.append({"role": "assistant", "content": response.content})
            messages.append({"role": "user", "content": tool_results})
            continue

        break

    return "[max iteraciones]"


async def main():
    print("=== Tool calling paralelo ===\n")

    # Esta tarea debería generar múltiples tool_use blocks en un turno:
    # clima de 2 ciudades + un cálculo + una búsqueda — todos independientes
    tarea = (
        "Necesito: 1) el clima actual de Madrid y Paris, "
        "2) cuánto es 1234 * 56 + 789, y "
        "3) busca información sobre 'parallel tool calling LLM'. "
        "Puedes hacer todas estas búsquedas a la vez."
    )

    print(f"Tarea: {tarea}\n")

    respuesta = await agent_loop(tarea)
    print(f"\nRespuesta del modelo:\n{respuesta}")


if __name__ == "__main__":
    asyncio.run(main())
