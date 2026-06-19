# Function calling sin soporte nativo — constrained via system prompt + retry.
#
# Cuando el modelo no tiene fine-tuning para tool calling, se describe
# el formato JSON esperado en el system prompt y se valida la respuesta.
# Si el JSON es inválido, se reintenta con el error acumulado en el prompt
# (máx 3 intentos).
#
# Tasa de fallo sin fine-tuning: 15-40%. Con retry x3 y 80% accuracy/intento,
# la probabilidad de fallo total ≈ 0.8%.
#
# Cómo ejecutar:
#   make py FILE=python/05-herramientas/10-formatos/sin-soporte-nativo.py

from __future__ import annotations

import json
import os
import re
from dataclasses import dataclass
from typing import Any

import anthropic

MODEL = os.environ.get("MODEL", "claude-sonnet-4-6")
MAX_RETRIES = 3

# --- Descripción de herramientas en el system prompt ---

TOOLS_DESCRIPTION = """
Tienes acceso a las siguientes herramientas:

- search_database(query: str, limit: int = 10)
  Busca en la base de datos. limit debe ser entre 1 y 100.

- calculate(expression: str)
  Evalúa una expresión matemática. Solo operadores +, -, *, /.

Para usar una herramienta, responde ÚNICAMENTE con JSON válido en este formato:
{
  "tool": "nombre_herramienta",
  "arguments": {
    "param1": "valor1",
    "param2": valor2
  }
}

Si no necesitas una herramienta, responde con texto normal.
NO incluyas texto adicional antes o después del JSON cuando uses una herramienta.
""".strip()


# --- Validación del JSON ---

@dataclass
class ValidationError:
    message: str
    hint: str = ""


def validar_tool_call(texto: str) -> tuple[bool, dict[str, Any] | ValidationError]:
    # Extraer JSON del texto (puede estar rodeado de markdown)
    json_match = re.search(r"```(?:json)?\s*([\s\S]*?)\s*```", texto) or re.search(r"(\{[\s\S]*\})", texto)
    json_str = json_match.group(1).strip() if json_match else texto.strip()

    try:
        parsed = json.loads(json_str)
    except json.JSONDecodeError as e:
        return False, ValidationError(
            f"JSON inválido: {e}",
            "Asegúrate de que la respuesta sea JSON puro sin texto adicional.",
        )

    if not isinstance(parsed, dict):
        return False, ValidationError("La respuesta debe ser un objeto JSON, no un valor primitivo.")

    if "tool" not in parsed:
        return False, ValidationError(
            'Campo requerido faltante: "tool"',
            'El JSON debe tener un campo "tool" con el nombre de la herramienta.',
        )

    if not isinstance(parsed["tool"], str):
        return False, ValidationError(f'Campo "tool" debe ser string, recibido: {type(parsed["tool"]).__name__}')

    herramientas_validas = ["search_database", "calculate"]
    if parsed["tool"] not in herramientas_validas:
        return False, ValidationError(
            f'Herramienta desconocida: "{parsed["tool"]}"',
            f"Herramientas disponibles: {', '.join(herramientas_validas)}",
        )

    if "arguments" not in parsed:
        return False, ValidationError(
            'Campo requerido faltante: "arguments"',
            'El JSON debe tener un campo "arguments" con los parámetros de la herramienta.',
        )

    if not isinstance(parsed["arguments"], dict):
        return False, ValidationError('"arguments" debe ser un objeto.')

    args = parsed["arguments"]

    if parsed["tool"] == "search_database":
        if "query" not in args or not isinstance(args["query"], str):
            return False, ValidationError(
                'search_database requiere "query" (string)',
                'Ejemplo: {"tool": "search_database", "arguments": {"query": "usuarios activos", "limit": 10}}',
            )
        if "limit" in args:
            limit = args["limit"]
            if not isinstance(limit, int) or not (1 <= limit <= 100):
                return False, ValidationError('"limit" debe ser un entero entre 1 y 100.')

    if parsed["tool"] == "calculate":
        if "expression" not in args or not isinstance(args["expression"], str):
            return False, ValidationError(
                'calculate requiere "expression" (string)',
                'Ejemplo: {"tool": "calculate", "arguments": {"expression": "15 * 8 + 3"}}',
            )

    return True, parsed


# --- Herramientas mock ---

def ejecutar_tool(call: dict[str, Any]) -> str:
    if call["tool"] == "search_database":
        query = call["arguments"]["query"]
        limit = call["arguments"].get("limit", 10)
        return json.dumps({
            "results": [
                {"id": 1, "texto": f'Resultado 1 para "{query}"'},
                {"id": 2, "texto": f'Resultado 2 para "{query}"'},
            ],
            "total": 2,
            "limit": limit,
        })

    if call["tool"] == "calculate":
        expression = call["arguments"]["expression"]
        try:
            sanitized = re.sub(r"[^0-9+\-*/().\s]", "", expression)
            result = eval(sanitized, {"__builtins__": {}})  # noqa: S307
            return str(result)
        except Exception as e:
            return f"Error al evaluar: {expression}: {e}"

    return "Herramienta no encontrada"


# --- Loop con retry acumulativo ---

def llamar_con_retry(pregunta: str, client: anthropic.Anthropic) -> dict[str, Any]:
    mensajes_error: list[str] = []

    for intento in range(1, MAX_RETRIES + 1):
        user_content = pregunta
        if mensajes_error:
            errores = "\n".join(f"Intento {i+1}: {e}" for i, e in enumerate(mensajes_error))
            user_content += f"\n\n[ERRORES PREVIOS — corrige estos problemas en tu respuesta:]\n{errores}"

        response = client.messages.create(
            model=MODEL,
            max_tokens=512,
            system=TOOLS_DESCRIPTION,
            messages=[{"role": "user", "content": user_content}],
        )

        texto = "".join(b.text for b in response.content if b.type == "text")
        print(f"  [intento {intento}] respuesta: {texto[:120]}...")

        ok, resultado = validar_tool_call(texto)
        if ok:
            return {"tool_call": resultado, "intentos": intento}

        error = resultado  # ValidationError
        error_msg = f"{error.message} — {error.hint}" if error.hint else error.message
        mensajes_error.append(error_msg)
        print(f"  [intento {intento}] error de validación: {error_msg}")

        if intento == MAX_RETRIES and "{" not in texto:
            return {"respuesta": texto, "intentos": intento}

    raise RuntimeError(f"No se obtuvo JSON válido tras {MAX_RETRIES} intentos")


def main() -> None:
    print("=== Function calling sin soporte nativo (system prompt + retry) ===\n")

    client = anthropic.Anthropic()

    casos = [
        {
            "descripcion": "Caso normal: debería generar JSON válido",
            "pregunta": "Busca los usuarios que se registraron en el último mes, máximo 20 resultados.",
        },
        {
            "descripcion": "Caso aritmético: debería usar calculate",
            "pregunta": "¿Cuánto es 1234 * 56 + 789?",
        },
    ]

    for caso in casos:
        print(f"\n--- {caso['descripcion']} ---")
        print(f"Pregunta: {caso['pregunta']}")

        resultado = llamar_con_retry(caso["pregunta"], client)

        if "tool_call" in resultado:
            tc = resultado["tool_call"]
            print(f"\nTool call validada ({resultado['intentos']} intento/s):")
            print(json.dumps(tc, indent=2, ensure_ascii=False))

            tool_result = ejecutar_tool(tc)
            print(f"\nResultado de la herramienta: {tool_result}")

            response = client.messages.create(
                model=MODEL,
                max_tokens=512,
                system=TOOLS_DESCRIPTION,
                messages=[
                    {"role": "user", "content": caso["pregunta"]},
                    {"role": "assistant", "content": json.dumps(tc)},
                    {"role": "user", "content": f"Resultado de la herramienta: {tool_result}"},
                ],
            )
            respuesta_final = "".join(b.text for b in response.content if b.type == "text")
            print(f"\nRespuesta final: {respuesta_final}")
        else:
            print(f"\nRespuesta directa (sin tool call): {resultado['respuesta']}")


if __name__ == "__main__":
    main()
