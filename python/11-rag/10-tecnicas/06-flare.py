# FLARE: Forward-Looking Active Retrieval Augmented Generation (Jiang et al., 2023).
#
# FLARE genera texto en segmentos y, cuando la probabilidad de un token cae bajo
# un umbral, usa el texto tentativo como query de búsqueda y regenera el segmento
# con el contexto recuperado. Sin fine-tuning — solo logprobs nativos de la API.
#
# IMPORTANTE: Claude y Gemini no exponen logprobs — este archivo usa OpenAI.
# Compatible con: OpenAI API, modelos locales vía Ollama, HuggingFace.
#
# Requisito: pip install openai
#
# Cómo ejecutar:
#   export OPENAI_API_KEY=sk-...
#   make py SCRIPT=python/11-rag/10-tecnicas/06-flare.py
#
# Qué observar:
#   Cada segmento muestra su confianza mínima.
#   Los segmentos bajo el umbral disparan retrieval y regeneración.
#   La respuesta final concatena solo segmentos de alta confianza.

import math
import os
from collections import Counter

from openai import OpenAI

MODEL    = os.environ.get("MODEL", "gpt-4o-mini")
UMBRAL   = float(os.environ.get("FLARE_UMBRAL", "0.2"))   # logprob mínimo (negativo)
MAX_ITER = int(os.environ.get("FLARE_MAX_ITER", "6"))

client = OpenAI()

# ── Corpus y retriever mock ────────────────────────────────────────────────

CORPUS = [
    "RAG combina recuperación de documentos con generación del LLM para reducir alucinaciones.",
    "Self-RAG fine-tunea el modelo para emitir tokens especiales que controlan el retrieval.",
    "FLARE activa el retrieval cuando la probabilidad de un token cae bajo un umbral configurable.",
    "BM25 es una función de recuperación léxica basada en frecuencia de término e IDF.",
    "Advanced RAG usa BM25 + búsqueda semántica + RRF para mejorar el recall.",
    "La ventana de contexto de Claude 3 llega a 200 000 tokens.",
    "Los modelos de lenguaje tienden a alucinar hechos fuera de su distribución de entrenamiento.",
    "GraphRAG construye un grafo de entidades sobre el corpus antes de recuperar.",
    "Los logprobs permiten medir la incertidumbre del modelo token a token.",
    "FLARE-direct usa la tentativa de generación como query de búsqueda sin reformulación.",
]


def tokenizar(texto: str) -> list[str]:
    return texto.lower().split()


def bm25_top_k(query: str, k: int = 2) -> list[str]:
    tokenized = [tokenizar(doc) for doc in CORPUS]
    n = len(CORPUS)
    df: dict[str, int] = {}
    for tokens in tokenized:
        for term in set(tokens):
            df[term] = df.get(term, 0) + 1
    avgdl = sum(len(t) for t in tokenized) / max(n, 1)
    k1, b = 1.5, 0.75
    q_tokens = tokenizar(query)
    scores: list[tuple[str, float]] = []
    for doc, tokens in zip(CORPUS, tokenized):
        tf = Counter(tokens)
        dl = len(tokens)
        total = 0.0
        for term in q_tokens:
            if term not in df:
                continue
            idf = math.log((n - df[term] + 0.5) / (df[term] + 0.5) + 1)
            freq = tf.get(term, 0)
            total += idf * (freq * (k1 + 1)) / (freq + k1 * (1 - b + b * dl / avgdl))
        scores.append((doc, total))
    scores.sort(key=lambda x: -x[1])
    return [doc for doc, _ in scores[:k] if _ > 0]


# ── Generación con logprobs ────────────────────────────────────────────────

def generar_con_logprobs(prompt: str, max_tokens: int = 80) -> tuple[str, float]:
    """Devuelve (texto, min_logprob). min_logprob es el logprob más bajo del segmento."""
    resp = client.chat.completions.create(
        model=MODEL,
        messages=[{"role": "user", "content": prompt}],
        max_tokens=max_tokens,
        logprobs=True,
        temperature=0.0,
    )
    texto = resp.choices[0].message.content or ""
    tokens_lp = resp.choices[0].logprobs
    if tokens_lp and tokens_lp.content:
        min_lp = min(t.logprob for t in tokens_lp.content)
    else:
        min_lp = 0.0
    return texto, min_lp


# ── FLARE-direct ──────────────────────────────────────────────────────────

def flare(query: str) -> str:
    """Implementación de FLARE-direct (Jiang et al., 2023)."""
    segmentos_aceptados: list[str] = []
    contexto_actual: list[str] = []

    for iteracion in range(1, MAX_ITER + 1):
        contexto_str = "\n".join(contexto_actual)
        previo_str   = " ".join(segmentos_aceptados)

        prompt = (
            f"Responde en español de forma factual y concisa. "
            f"{'Contexto recuperado: ' + contexto_str + chr(10) if contexto_str else ''}"
            f"{'Respuesta parcial hasta ahora: ' + previo_str + chr(10) if previo_str else ''}"
            f"Pregunta: {query}\n"
            f"Continúa la respuesta (máximo 2 oraciones):"
        )

        tentativa, min_lp = generar_con_logprobs(prompt)

        print(f"  [{iteracion}] confianza mín: {min_lp:.3f}  |  tentativa: {tentativa[:60]!r}")

        if min_lp < UMBRAL:
            chunks = bm25_top_k(tentativa)
            if chunks:
                contexto_actual = chunks
                print(f"       → retrieval activado ({len(chunks)} chunks). Regenerando...")
                prompt_regen = (
                    f"Responde en español de forma factual y concisa. "
                    f"Contexto: {chr(10).join(chunks)}\n"
                    f"{'Respuesta parcial: ' + previo_str + chr(10) if previo_str else ''}"
                    f"Pregunta: {query}\n"
                    f"Continúa la respuesta (máximo 2 oraciones):"
                )
                segmento, _ = generar_con_logprobs(prompt_regen)
            else:
                segmento = tentativa
        else:
            segmento = tentativa

        segmentos_aceptados.append(segmento.strip())

        termina = any(c in segmento for c in [".", "!", "?"])
        if termina and len(" ".join(segmentos_aceptados).split()) > 20:
            break

    return " ".join(segmentos_aceptados)


# ── Demo ──────────────────────────────────────────────────────────────────

def main() -> None:
    preguntas = [
        "¿Qué es FLARE y cómo se diferencia de Self-RAG?",
        "¿Cuándo se activa el retrieval en FLARE y qué hace el modelo con los chunks?",
    ]

    for pregunta in preguntas:
        print(f"\nPregunta: {pregunta}")
        print(f"Umbral logprob: {UMBRAL} | Modelo: {MODEL}\n")
        respuesta = flare(pregunta)
        print(f"\nRespuesta final: {respuesta}\n")
        print("-" * 70)


if __name__ == "__main__":
    main()
