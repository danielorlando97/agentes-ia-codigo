# Retrieval como herramienta — el LLM decide cuándo y qué buscar con tool_use.
#
# En lugar de recuperar siempre antes de generar (RAG ingenuo), aquí el LLM
# recibe buscar_documentos como herramienta y decide si llamarla, cuántas veces,
# y con qué query. El agente itera hasta que produce texto final (end_turn)
# o alcanza el límite de seguridad de 5 iteraciones.
#
# TF-IDF cosine idéntico a rag_ingenuo.py — mismas funciones, sin dependencias
# externas salvo anthropic SDK.
#
# Cómo ejecutar:
#   make py SCRIPT=python/11-rag/retrieval_herramienta.py
#
# Qué esperar:
#   El LLM decide cuándo y qué buscar con tool_use. Puede hacer multiples
#   búsquedas iterativas antes de generar la respuesta final.
#
# Variables de entorno:
#   MODEL — modelo a usar (default: claude-sonnet-4-6)


import math
import os
from collections import Counter
import anthropic

MODEL = os.environ.get("MODEL", "claude-haiku-4-5-20251001")
MAX_ITER = 5

CORPUS = [
    "Los modelos de lenguaje transformers usan mecanismo de atención para procesar texto.",
    "El contexto de un LLM es la ventana de tokens que puede procesar en una sola inferencia.",
    "RAG combina recuperación de documentos con generación del LLM para reducir alucinaciones.",
    "El chunking divide documentos largos en fragmentos manejables para el vector store.",
    "La similitud coseno mide el ángulo entre dos vectores en el espacio de embeddings.",
    "Los embeddings mapean texto a vectores numéricos en un espacio semántico continuo.",
    "El reranking reordena los candidatos recuperados usando un modelo más preciso.",
    "BM25 es una función de recuperación basada en TF-IDF mejorada para búsqueda exacta.",
    "RAG-Anything extiende RAG a corpus multimodal con tablas, imágenes y ecuaciones.",
    "LightRAG construye un grafo de conocimiento con retrieval dual-level para multi-hop.",
]

# ── TF-IDF cosine (igual que rag_ingenuo) ─────────────────────────────────────

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


def indexar(corpus: list[str]) -> tuple[list[tuple[str, dict[str, float]]], dict[str, int]]:
    tokenized = [tokenizar(doc) for doc in corpus]
    n_docs = len(corpus)

    df: dict[str, int] = {}
    for tokens in tokenized:
        for term in set(tokens):
            df[term] = df.get(term, 0) + 1

    index = []
    for chunk, tokens in zip(corpus, tokenized):
        vec = tfidf_vector(tokens, df, n_docs)
        index.append((chunk, vec))

    return index, df


def buscar_documentos(
    query: str,
    k: int,
    index: list[tuple[str, dict[str, float]]],
    df: dict[str, int],
) -> str:
    """Recupera los k chunks más relevantes. Devuelve texto plano numerado."""
    n_docs = len(index)
    q_tokens = tokenizar(query)
    q_vec = tfidf_vector(q_tokens, df, n_docs)

    scores = [(chunk, cosine_sim(q_vec, vec)) for chunk, vec in index]
    scores.sort(key=lambda x: x[1], reverse=True)
    top = scores[:k]

    lines = [f"{i + 1}. {chunk} (score={score:.4f})" for i, (chunk, score) in enumerate(top)]
    return "\n".join(lines)


# ── Definición de la herramienta para la API de Anthropic ─────────────────────

TOOLS = [
    {
        "name": "buscar_documentos",
        "description": "Busca en la base de conocimiento interna y devuelve los fragmentos más relevantes.",
        "input_schema": {
            "type": "object",
            "properties": {
                "query": {
                    "type": "string",
                    "description": "Texto a buscar en la base de conocimiento.",
                },
                "k": {
                    "type": "integer",
                    "description": "Número de fragmentos a recuperar (por defecto 3).",
                    "default": 3,
                },
            },
            "required": ["query"],
        },
    }
]

SYSTEM = (
    "Eres un asistente con acceso a una base de conocimiento. "
    "Usa buscar_documentos cuando necesites información específica. "
    "Responde directamente si ya tienes suficiente información."
)

# ── Agent loop ─────────────────────────────────────────────────────────────────

def agente_rag(pregunta: str, index: list, df: dict[str, int], client: anthropic.Anthropic) -> str:
    messages: list[dict] = [{"role": "user", "content": pregunta}]

    for iteracion in range(MAX_ITER):
        response = client.messages.create(
            model=MODEL,
            max_tokens=1024,
            system=SYSTEM,
            tools=TOOLS,
            messages=messages,
        )

        print(f"\n[iter={iteracion + 1}] stop_reason={response.stop_reason}")

        if response.stop_reason == "end_turn":
            # Extraer el texto final
            for block in response.content:
                if block.type == "text":
                    return block.text
            return "[sin texto en la respuesta]"

        if response.stop_reason == "tool_use":
            # Añadir la respuesta del asistente (con los tool_use blocks) al historial
            messages.append({"role": "assistant", "content": response.content})

            # Ejecutar todas las tool calls y acumular resultados
            tool_results = []
            for block in response.content:
                if block.type != "tool_use":
                    continue

                args = block.input
                query = args["query"]
                k = int(args.get("k", 3))

                print(f"  → buscar_documentos(query={query!r}, k={k})")
                resultado = buscar_documentos(query, k, index, df)
                print(f"  ← {resultado[:120].replace(chr(10), ' | ')}")

                tool_results.append(
                    {
                        "type": "tool_result",
                        "tool_use_id": block.id,
                        "content": resultado,
                    }
                )

            # CRÍTICO: todos los tool_results en un único mensaje user
            messages.append({"role": "user", "content": tool_results})
            continue

        # Cualquier otro stop_reason (max_tokens, etc.)
        print(f"  [warn] stop_reason inesperado: {response.stop_reason}")
        break

    return "[límite de iteraciones alcanzado]"


# ── Demo ───────────────────────────────────────────────────────────────────────

def main() -> None:
    client = anthropic.Anthropic()  # lee ANTHROPIC_API_KEY del entorno
    index, df = indexar(CORPUS)

    pregunta = "¿Qué diferencia hay entre RAG-Anything y LightRAG?"
    print(f"Pregunta: {pregunta}")

    respuesta = agente_rag(pregunta, index, df, client)
    print(f"\n=== Respuesta final ===\n{respuesta}")


if __name__ == "__main__":
    main()
