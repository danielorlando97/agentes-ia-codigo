# Formato XML estilo Hermes (NousResearch) para tool calling.
#
# Los tags <tool_call> / </tool_call> son tokens únicos en el vocabulario
# del modelo Hermes — el parser detecta límites O(1) por token, no O(n).
# El output NO es XML real: se parsea con regex, no con un parser XML.
#
# Aquí se instruye a Claude a responder en formato Hermes para demostrar
# el parser. En producción se usaría Hermes 2 Pro o Hermes 3 (Llama 3.1).
#
# Cómo ejecutar:
#   make py SCRIPT=python/05-herramientas/10-formatos/xml-hermes.py
#
# Qué esperar:
#   Simulación del formato Hermes con tags <tool_call>. El parser usa regex
#   en lugar de XML real. Muestra por qué este formato es eficiente para modelos
#   entrenados con tokens especiales de tool call.
#
# Nota: Este formato requiere modelos Hermes (NousResearch). Con Claude, el
#   formato nativo JSON es preferible.

import os
import json
import re
import anthropic

MODEL = os.environ.get("MODEL", "claude-haiku-4-5-20251001")
client = anthropic.Anthropic()

# --- Definición de tools en formato Hermes (JSON dentro de <tools>) ---

TOOLS_HERMES = [
    {
        "type": "function",
        "function": {
            "name": "get_weather",
            "description": (
                "Get current weather for a city. "
                "Use when the user asks about weather conditions or temperature."
            ),
            "parameters": {
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
            },
        },
    },
    {
        "type": "function",
        "function": {
            "name": "get_time",
            "description": "Get current local time for a given timezone.",
            "parameters": {
                "type": "object",
                "properties": {
                    "timezone": {
                        "type": "string",
                        "description": "IANA timezone string, e.g. 'Europe/Madrid'",
                    },
                },
                "required": ["timezone"],
            },
        },
    },
]

SYSTEM_HERMES = f"""Eres un asistente con acceso a herramientas.

<tools>
{json.dumps(TOOLS_HERMES, indent=2, ensure_ascii=False)}
</tools>

Cuando necesites usar una herramienta, responde con este formato exacto:

<tool_call>
{{"name": "<nombre_herramienta>", "arguments": {{<argumentos en JSON>}}}}
</tool_call>

Puedes emitir múltiples <tool_call> para llamadas paralelas. El output dentro del tag
no es XML real — el sistema lo parsea con regex, no con un parser XML.
Después de recibir los resultados en <tool_response>, responde al usuario."""


# --- Parser de <tool_call> por regex ---

def extraer_tool_calls(respuesta: str) -> list[dict]:
    patron = r"<tool_call>\s*(\{.*?\})\s*</tool_call>"
    matches = re.findall(patron, respuesta, re.DOTALL)
    resultado = []
    for match in matches:
        try:
            datos = json.loads(match)
            resultado.append({
                "name": datos["name"],
                "arguments": datos.get("arguments", datos.get("parameters", {})),
            })
        except json.JSONDecodeError:
            # JSON parcialmente malformado — en producción usar json_repair
            continue
    return resultado


# --- Mock de ejecución ---

def ejecutar_herramienta(nombre: str, args: dict) -> dict:
    if nombre == "get_weather":
        return {
            "location": args["location"],
            "temperature": 22,
            "unit": args.get("unit", "celsius"),
            "conditions": "parcialmente nublado",
        }
    if nombre == "get_time":
        return {"timezone": args["timezone"], "local_time": "14:35:00"}
    return {"error": f"herramienta desconocida: {nombre}"}


def formatear_tool_response(nombre: str, resultado: dict) -> str:
    return (
        f"<tool_response>\n"
        f'{{"name": "{nombre}", "content": {json.dumps(resultado, ensure_ascii=False)}}}\n'
        f"</tool_response>"
    )


# --- Loop de tool use con formato Hermes ---

def hermes_loop(pregunta: str) -> str:
    historial = [{"role": "user", "content": pregunta}]

    for _ in range(10):
        resp = client.messages.create(
            model=MODEL,
            max_tokens=1024,
            system=SYSTEM_HERMES,
            messages=historial,
        )
        texto = resp.content[0].text.strip()
        historial.append({"role": "assistant", "content": texto})

        tool_calls = extraer_tool_calls(texto)
        if not tool_calls:
            return texto  # Respuesta final sin tool calls

        # Ejecutar todas las tool calls (pueden ser paralelas en Hermes)
        responses_xml = []
        for tc in tool_calls:
            resultado = ejecutar_herramienta(tc["name"], tc["arguments"])
            print(f"  → {tc['name']}({tc['arguments']}) = {str(resultado)[:60]}")
            responses_xml.append(formatear_tool_response(tc["name"], resultado))

        # Inyectar resultados en el historial
        tool_response_msg = "\n".join(responses_xml)
        historial.append({"role": "user", "content": tool_response_msg})

    return "[límite de pasos alcanzado]"


def main() -> None:
    print("=== Formato XML Hermes (NousResearch style) ===")
    print("Parser de <tool_call> por regex — no es XML real\n")

    # Demo del parser con respuesta simulada
    respuesta_simulada = """Voy a consultar el tiempo y la hora simultáneamente.
<tool_call>
{"name": "get_weather", "arguments": {"location": "Madrid, Spain", "unit": "celsius"}}
</tool_call><tool_call>
{"name": "get_time", "arguments": {"timezone": "Europe/Madrid"}}
</tool_call>"""

    print("Respuesta simulada del modelo:")
    print(respuesta_simulada)
    calls = extraer_tool_calls(respuesta_simulada)
    print(f"\nTool calls extraídas por regex: {calls}\n")

    # Loop completo con el modelo
    print("=" * 60)
    print("Pregunta: ¿Qué tiempo hace en Tokio?")
    respuesta = hermes_loop("¿Qué tiempo hace en Tokio?")
    # Eliminar los tags XML de la respuesta final para mostrarla limpia
    respuesta_limpia = re.sub(r"<tool_call>.*?</tool_call>", "", respuesta, flags=re.DOTALL).strip()
    print(f"Respuesta final: {respuesta_limpia}")


if __name__ == "__main__":
    main()
