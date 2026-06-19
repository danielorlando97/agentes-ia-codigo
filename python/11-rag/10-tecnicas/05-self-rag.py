# Self-RAG: generación por segmentos con tokens de reflexión.
#
# Self-RAG (Asai et al., 2023) enseña al modelo a evaluar en tiempo de inferencia
# si necesita recuperar y si lo que recuperó es útil — sin pipeline externo.
# Los cuatro tokens de reflexión:
#   Retrieve={yes/no/continue}  — ¿necesito buscar para generar este segmento?
#   ISREL={relevant/irrelevant} — ¿el pasaje recuperado es relevante para la query?
#   ISSUP={fully/partially/no}  — ¿el pasaje apoya la afirmación generada?
#   ISUSE={1..5}                — ¿es útil esta respuesta para el usuario?
#
# Esta implementación simula los tokens de reflexión via prompting estructurado
# con Claude (el modelo Self-RAG real es un Llama 7B fine-tuneado: selfrag/selfrag_llama2_7b).
# El mecanismo de filtrado y la lógica de iteración por segmentos son idénticos.
#
# Cómo ejecutar:
#   make py SCRIPT=python/11-rag/10-tecnicas/05-self-rag.py
#
# Qué esperar:
#   Para cada segmento de la respuesta, el modelo decide si recuperar,
#   evalúa la relevancia del pasaje, y filtra segmentos no apoyados.
#
# Variables de entorno:
#   MODEL — modelo a usar (default: claude-haiku-4-5-20251001)

import math
import os
from collections import Counter
from dataclasses import dataclass, field

import anthropic

MODEL = os.environ.get("MODEL", "claude-haiku-4-5-20251001")

# ── Corpus y retriever mock ────────────────────────────────────────────────

CORPUS = [
    "RAG combina recuperación de documentos con generación del LLM para reducir alucinaciones.",
    "Self-RAG fine-tunea el modelo para generar tokens especiales que evalúan el retrieval.",
    "El token Retrieve indica si el segmento actual necesita información externa.",
    "ISREL evalúa si el pasaje recuperado es relevante para la query original.",
    "ISSUP evalúa si el pasaje apoya la afirmación que el modelo está generando.",
    "ISUSE evalúa la utilidad global de la respuesta en una escala del 1 al 5.",
    "Los modelos de lenguaje large tienden a alucinar hechos no presentes en su preentrenamiento.",
    "BM25 es una función de recuperación léxica que supera a TF-IDF en la mayoría de benchmarks.",
    "El fine-tuning de Self-RAG requiere un corpus de reflexión generado por un critic model.",
    "Advanced RAG usa BM25 + semántico para mejorar el recall en búsqueda híbrida.",
]


def tokenizar(texto: str) -> list[str]:
    return texto.lower().split()


def bm25_score(query: str, corpus: list[str], k: int = 3) -> list[str]:
    tokenized = [tokenizar(doc) for doc in corpus]
    n = len(corpus)
    df: dict[str, int] = {}
    for tokens in tokenized:
        for term in set(tokens):
            df[term] = df.get(term, 0) + 1
    avgdl = sum(len(t) for t in tokenized) / max(n, 1)
    k1, b = 1.5, 0.75

    q_tokens = tokenizar(query)
    scores = []
    for i, (doc, tokens) in enumerate(zip(corpus, tokenized)):
        tf = Counter(tokens)
        dl = len(tokens)
        total = 0.0
        for term in q_tokens:
            if term not in df:
                continue
            idf = math.log((n - df[term] + 0.5) / (df[term] + 0.5) + 1)
            freq = tf.get(term, 0)
            num = freq * (k1 + 1)
            den = freq + k1 * (1 - b + b * dl / avgdl)
            total += idf * (num / den)
        scores.append((doc, total))
    scores.sort(key=lambda x: -x[1])
    return [doc for doc, _ in scores[:k]]


# ── Tokens de reflexión via prompting ─────────────────────────────────────

@dataclass
class ReflexionResult:
    retrieve: str = "no"           # yes | no | continue
    pasaje: str = ""
    is_rel: str = "irrelevant"     # relevant | irrelevant
    segmento: str = ""
    is_sup: str = "no"             # fully | partially | no
    is_use: int = 3                # 1..5


def simular_retrieve(cliente: anthropic.Anthropic, query: str, contexto_previo: str) -> str:
    """¿Necesita este punto de la generación información externa?"""
    resp = cliente.messages.create(
        model=MODEL,
        max_tokens=10,
        system="Responde únicamente con una de estas opciones: yes | no | continue",
        messages=[{
            "role": "user",
            "content": (
                f"Query: {query}\n"
                f"Contexto generado hasta ahora: {contexto_previo!r}\n"
                "¿El siguiente segmento de la respuesta necesita recuperar documentos externos? "
                "(yes=sí necesita; no=no necesita; continue=ya hay suficiente)"
            ),
        }],
    )
    token = resp.content[0].text.strip().lower()
    return token if token in ("yes", "no", "continue") else "no"


def simular_isrel(cliente: anthropic.Anthropic, query: str, pasaje: str) -> str:
    """¿El pasaje recuperado es relevante para la query?"""
    resp = cliente.messages.create(
        model=MODEL,
        max_tokens=10,
        system="Responde únicamente con: relevant | irrelevant",
        messages=[{
            "role": "user",
            "content": f"Query: {query}\nPasaje: {pasaje}\n¿Es relevante el pasaje para la query?",
        }],
    )
    token = resp.content[0].text.strip().lower()
    return "relevant" if "relevant" in token else "irrelevant"


def simular_segmento(cliente: anthropic.Anthropic, query: str, pasaje: str, contexto_previo: str) -> str:
    """Genera el siguiente segmento de la respuesta usando el pasaje."""
    resp = cliente.messages.create(
        model=MODEL,
        max_tokens=150,
        system="Genera un segmento conciso (1-2 frases) apoyado en el pasaje proporcionado.",
        messages=[{
            "role": "user",
            "content": (
                f"Query: {query}\n"
                f"Pasaje de referencia: {pasaje}\n"
                f"Respuesta generada hasta ahora: {contexto_previo!r}\n"
                "Genera el siguiente segmento de la respuesta:"
            ),
        }],
    )
    return resp.content[0].text.strip()


def simular_issup(cliente: anthropic.Anthropic, pasaje: str, segmento: str) -> str:
    """¿El pasaje apoya la afirmación del segmento?"""
    resp = cliente.messages.create(
        model=MODEL,
        max_tokens=10,
        system="Responde únicamente con: fully | partially | no",
        messages=[{
            "role": "user",
            "content": f"Pasaje: {pasaje}\nAfirmación: {segmento}\n¿El pasaje apoya la afirmación?",
        }],
    )
    token = resp.content[0].text.strip().lower()
    if "fully" in token:
        return "fully"
    if "partial" in token:
        return "partially"
    return "no"


def simular_isuse(cliente: anthropic.Anthropic, query: str, respuesta: str) -> int:
    """¿Qué tan útil es la respuesta? Escala 1-5."""
    resp = cliente.messages.create(
        model=MODEL,
        max_tokens=5,
        system="Responde únicamente con un número del 1 al 5.",
        messages=[{
            "role": "user",
            "content": f"Query: {query}\nRespuesta: {respuesta}\n¿Cuál es la utilidad? (1=nula, 5=perfecta)",
        }],
    )
    try:
        return int(resp.content[0].text.strip()[0])
    except (ValueError, IndexError):
        return 3


# ── Pipeline Self-RAG ──────────────────────────────────────────────────────

def self_rag(query: str, cliente: anthropic.Anthropic, max_segmentos: int = 3) -> str:
    """
    Pipeline de inferencia Self-RAG:
    1. Para cada segmento, decidir si recuperar (Retrieve token)
    2. Recuperar y filtrar por relevancia (ISREL token)
    3. Generar segmento apoyado en el pasaje
    4. Filtrar segmentos no apoyados (ISSUP token)
    5. Evaluar utilidad global (ISUSE token)
    """
    print(f"\nQuery: {query!r}")
    print("─" * 60)

    respuesta_acumulada = ""
    segmentos_validos = []

    for i in range(max_segmentos):
        print(f"\n[Segmento {i+1}]")

        # Token Retrieve
        retrieve = simular_retrieve(cliente, query, respuesta_acumulada)
        print(f"  Retrieve={retrieve}")

        if retrieve == "continue":
            print("  → generación suficiente, parando")
            break

        if retrieve == "no":
            # Sin retrieval, generar directamente
            resp = cliente.messages.create(
                model=MODEL,
                max_tokens=100,
                messages=[{
                    "role": "user",
                    "content": (
                        f"Query: {query}\nContexto previo: {respuesta_acumulada!r}\n"
                        "Continúa la respuesta en 1-2 frases:"
                    ),
                }],
            )
            segmento = resp.content[0].text.strip()
            print(f"  (sin retrieval) {segmento[:80]}")
            segmentos_validos.append(segmento)
            respuesta_acumulada += " " + segmento
            continue

        # Retrieve=yes: recuperar pasaje
        pasajes = bm25_score(query, CORPUS, k=2)
        pasaje = pasajes[0] if pasajes else ""
        print(f"  Pasaje: {pasaje[:70]}")

        # Token ISREL
        is_rel = simular_isrel(cliente, query, pasaje)
        print(f"  ISREL={is_rel}")
        if is_rel == "irrelevant":
            print("  → pasaje irrelevante, saltando segmento")
            continue

        # Generar segmento con el pasaje relevante
        segmento = simular_segmento(cliente, query, pasaje, respuesta_acumulada)
        print(f"  Segmento: {segmento[:80]}")

        # Token ISSUP
        is_sup = simular_issup(cliente, pasaje, segmento)
        print(f"  ISSUP={is_sup}")
        if is_sup == "no":
            print("  → segmento no apoyado por el pasaje, descartado")
            continue

        segmentos_validos.append(segmento)
        respuesta_acumulada += " " + segmento

    respuesta_final = " ".join(segmentos_validos).strip()

    # Token ISUSE sobre la respuesta completa
    is_use = simular_isuse(cliente, query, respuesta_final)
    print(f"\nISUSE={is_use}/5")

    return respuesta_final


if __name__ == "__main__":
    cliente = anthropic.Anthropic()
    queries = [
        "¿Qué es Self-RAG y en qué se diferencia del RAG clásico?",
        "¿Cuándo conviene usar retrieval en la generación?",
    ]
    for query in queries:
        respuesta = self_rag(query, cliente, max_segmentos=3)
        print(f"\n=== Respuesta final ===\n{respuesta}\n")
