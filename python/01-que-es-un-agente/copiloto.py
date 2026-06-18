"""
Copiloto — sugerencia de codigo inline. Sin loop, sin estado, sin tools.

Qué demuestra:
    El patron de copiloto: el editor manda el buffer actual al LLM y este
    sugiere la continuacion mas probable. Una sola llamada, sin contexto
    de sesion anterior. Nivel "Procesador" del espectro de autonomia.

Patron clave:
    El buffer de codigo se inyecta directamente en el prompt de usuario.
    max_tokens=256 limita la sugerencia a algo razonable para una linea/bloque.
    El system prompt indica "solo codigo, sin explicaciones" para output limpio.

Cómo ejecutar:
    make py SCRIPT=python/01-que-es-un-agente/copiloto.py

Qué esperar:
    Muestra el buffer de entrada y la sugerencia de completado para una
    funcion fibonacci incompleta.

Variables de entorno:
    MODEL — modelo a usar (default: claude-sonnet-4-6)
"""
import os
import anthropic

MODEL = os.environ.get("MODEL", "claude-sonnet-4-6")
SYSTEM = (
    "Eres un copiloto de codigo. Dado un fragmento de codigo, "
    "sugiere la continuacion mas probable. Responde solo con el codigo sugerido, sin explicaciones."
)


def suggest(buffer: str) -> str:
    client = anthropic.Anthropic()
    response = client.messages.create(
        model=MODEL,
        max_tokens=256,
        system=SYSTEM,
        messages=[{"role": "user", "content": f"Completa:\n\n```\n{buffer}\n```"}],
    )
    return "".join(b.text for b in response.content if b.type == "text")


if __name__ == "__main__":
    code = "def fibonacci(n):\n    "
    print(f"Buffer:\n{code}")
    print(f"Sugerencia: {suggest(code)}")
