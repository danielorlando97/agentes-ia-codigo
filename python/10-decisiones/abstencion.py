"""
abstencion — ver comentarios inline.

Cómo ejecutar:
    make py SCRIPT=python/10-decisiones/abstencion.py

Qué esperar:
    El agente calcula una puntuacion de confianza y se abstiene de responder
    cuando la confianza cae por debajo del umbral configurado.
    Muestra los casos de abstención con su justificación.

Variables de entorno:
    MODEL — modelo a usar (default: claude-sonnet-4-6)
"""
import os
import re
from collections import Counter
from dataclasses import dataclass
import anthropic

MODEL = os.environ.get("MODEL", "claude-sonnet-4-6")
client = anthropic.Anthropic()  # lee ANTHROPIC_API_KEY del entorno

UMBRAL_RESPONDER = 0.8
UMBRAL_SOFT = 0.5


@dataclass
class PredictionResult:
    tipo: str        # "respuesta" | "soft" | "abstencion"
    contenido: str
    confianza: float


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


def selective_predict(query: str) -> PredictionResult:
    respuesta, confianza = self_consistency(query)

    if confianza >= UMBRAL_RESPONDER:
        return PredictionResult("respuesta", respuesta, confianza)
    elif confianza >= UMBRAL_SOFT:
        return PredictionResult(
            "soft",
            f"Según mi información (no verificada): {respuesta}. Recomiendo verificar.",
            confianza,
        )
    else:
        return PredictionResult(
            "abstencion",
            "No tengo suficiente certeza para responder esto correctamente.",
            confianza,
        )


if __name__ == "__main__":
    queries = [
        "¿Cuánto es 8 × 7? Da solo el número.",
        "¿Quién ganó el Premio Nobel de Literatura en 2019?",
        "¿Cuál es el precio exacto de la acción de Apple en este momento?",
    ]

    for q in queries:
        print(f"Query: {q}")
        resultado = selective_predict(q)
        print(f"  Tipo:      {resultado.tipo}")
        print(f"  Contenido: {resultado.contenido}")
        print(f"  Confianza: {resultado.confianza:.2f}")
        print()
