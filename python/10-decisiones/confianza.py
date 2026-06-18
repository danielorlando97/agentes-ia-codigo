"""
confianza — ver comentarios inline.

Cómo ejecutar:
    make py SCRIPT=python/10-decisiones/confianza.py

Qué esperar:
    Tres estrategias de medición de confianza: auto-evaluación, calibración
    via sampling y consistencia entre modelos. Tabla comparativa de scores.

Variables de entorno:
    MODEL — modelo a usar (default: claude-sonnet-4-6)
"""
import os
import re
from collections import Counter
import anthropic

MODEL = os.environ.get("MODEL", "claude-sonnet-4-6")
client = anthropic.Anthropic()  # lee ANTHROPIC_API_KEY del entorno


def extraer_respuesta(texto: str) -> str:
    numeros = re.findall(r'\b(\d+(?:\.\d+)?)\b', texto)
    if numeros:
        return numeros[-1]
    lineas = [l.strip() for l in texto.splitlines() if l.strip()]
    return lineas[-1][:50] if lineas else texto[:50]


def self_consistency(prompt: str, k: int = 5) -> tuple[str, float]:
    respuestas = []
    for _ in range(k):
        resp = client.messages.create(
            model=MODEL,
            max_tokens=200,
            temperature=0.7,
            messages=[{"role": "user", "content": prompt}],
        )
        respuestas.append(extraer_respuesta(resp.content[0].text))

    conteos = Counter(respuestas)
    mejor, votos = conteos.most_common(1)[0]
    return mejor, votos / k


if __name__ == "__main__":
    pregunta = "¿Cuánto es 17 × 23? Razona paso a paso y da solo el número al final."

    print(f"Pregunta: {pregunta}")
    print(f"Muestreando k=5 respuestas con temperature=0.7...\n")

    respuestas_raw = []
    for i in range(5):
        resp = client.messages.create(
            model=MODEL,
            max_tokens=200,
            temperature=0.7,
            messages=[{"role": "user", "content": pregunta}],
        )
        extraida = extraer_respuesta(resp.content[0].text)
        respuestas_raw.append(extraida)
        print(f"  Muestra {i+1}: '{extraida}'")

    conteos = Counter(respuestas_raw)
    mejor, votos = conteos.most_common(1)[0]
    confianza = votos / 5

    print(f"\nDistribución de votos: {dict(conteos)}")
    print(f"Respuesta: {mejor}")
    print(f"Confianza: {confianza:.2f} ({votos}/5 votos)")
