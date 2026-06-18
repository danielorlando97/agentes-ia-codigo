# Naive RAG con TF-IDF cosine similarity — sin dependencias externas salvo anthropic SDK
# Indexa un corpus hardcodeado, recupera top-3 chunks, genera respuesta con Claude Haiku.
#
# Cómo ejecutar:
#   make py SCRIPT=python/11-rag/rag_ingenuo.py
#
# Qué esperar:
#   Pipeline RAG minimo: indexa corpus, recupera top-3 chunks con TF-IDF,
#   genera respuesta con Claude Haiku. Sin dependencias externas.
#
# Variables de entorno:
#   MODEL       — modelo de generacion (default: claude-haiku-4-5-20251001)


import math
import os
from collections import Counter
import anthropic

CORPUS = [
    "Los modelos de lenguaje transformers usan mecanismo de atención para procesar texto.",
    "El contexto de un LLM es la ventana de tokens que puede procesar en una sola inferencia.",
    "RAG combina recuperación de documentos con generación del LLM para reducir alucinaciones.",
    "El chunking divide documentos largos en fragmentos manejables para el vector store.",
    "La similitud coseno mide el ángulo entre dos vectores en el espacio de embeddings.",
    "Los embeddings mapean texto a vectores numéricos en un espacio semántico continuo.",
    "El reranking reordena los candidatos recuperados usando un modelo más preciso.",
    "BM25 es una función de recuperación basada en TF-IDF mejorada para búsqueda exacta.",
]


def tokenizar(texto: str) -> list[str]:
    return texto.lower().split()


def tfidf_vector(tokens: list[str], df: dict[str, int], n_docs: int) -> dict[str, float]:
    tf = Counter(tokens)
    total = len(tokens) if tokens else 1
    vector: dict[str, float] = {}
    for term, count in tf.items():
        tf_score = count / total
        idf_score = math.log((n_docs + 1) / (df.get(term, 0) + 1))
        vector[term] = tf_score * idf_score
    return vector


def cosine_sim(v1: dict[str, float], v2: dict[str, float]) -> float:
    dot = sum(v1.get(t, 0.0) * v2.get(t, 0.0) for t in v2)
    norm1 = math.sqrt(sum(x * x for x in v1.values()))
    norm2 = math.sqrt(sum(x * x for x in v2.values()))
    if norm1 == 0 or norm2 == 0:
        return 0.0
    return dot / (norm1 * norm2)


def indexar(corpus: list[str]) -> tuple[list[tuple[str, list[str], dict[str, float]]], dict[str, int]]:
    tokenized = [tokenizar(doc) for doc in corpus]
    n_docs = len(corpus)

    df: dict[str, int] = {}
    for tokens in tokenized:
        for term in set(tokens):
            df[term] = df.get(term, 0) + 1

    index = []
    for chunk, tokens in zip(corpus, tokenized):
        vec = tfidf_vector(tokens, df, n_docs)
        index.append((chunk, tokens, vec))

    return index, df


def buscar(
    query: str,
    index: list[tuple[str, list[str], dict[str, float]]],
    df: dict[str, int],
    k: int = 3,
) -> list[str]:
    n_docs = len(index)
    q_tokens = tokenizar(query)
    q_vec = tfidf_vector(q_tokens, df, n_docs)

    scores = [(chunk, cosine_sim(q_vec, vec)) for chunk, _, vec in index]
    scores.sort(key=lambda x: x[1], reverse=True)
    return [chunk for chunk, _ in scores[:k]]


def rag_ingenuo(query: str, index: list, df: dict[str, int], client: anthropic.Anthropic) -> str:
    top_chunks = buscar(query, index, df, k=3)
    contexto = "\n".join(f"- {c}" for c in top_chunks)

    message = client.messages.create(
        model=os.environ.get("SMALL_MODEL", "claude-haiku-4-5-20251001"),
        max_tokens=300,
        system="Responde usando solo el contexto proporcionado. Si la respuesta no está en el contexto, dilo explícitamente.",
        messages=[
            {
                "role": "user",
                "content": f"Contexto:\n{contexto}\n\nPregunta: {query}",
            }
        ],
    )
    return message.content[0].text


def main() -> None:
    client = anthropic.Anthropic()  # lee ANTHROPIC_API_KEY del entorno
    index, df = indexar(CORPUS)

    query = "¿Qué es RAG y para qué sirve?"
    print(f"Query: {query}\n")

    top = buscar(query, index, df, k=3)
    print("Chunks recuperados:")
    for i, chunk in enumerate(top, 1):
        print(f"  {i}. {chunk}")
    print()

    respuesta = rag_ingenuo(query, index, df, client)
    print(f"Respuesta:\n{respuesta}")


if __name__ == "__main__":
    main()
