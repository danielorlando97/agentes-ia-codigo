"""Mini-proyecto: El RAG lab.

Construye un pipeline RAG sobre un corpus de texto local usando TF-IDF.
Compara naive RAG, BM25-style retrieval y retrieval con reranking simulado.
Sin API key — todo el procesamiento es local.

Uso:
    python mini-rag-lab.py
    python mini-rag-lab.py --tecnica bm25
    python mini-rag-lab.py --tecnica todos --query "¿qué es un agente?"
    python mini-rag-lab.py --corpus mi_texto.txt --top-k 5

Cómo ejecutar:
    make py SCRIPT=python/11-rag/mini-rag-lab.py

Qué esperar:
    Pipeline RAG sobre corpus local con TF-IDF. Compara naive RAG, BM25-style
    y retrieval con reranking. Sin API key — todo el procesamiento es local.
"""

import argparse
import math
import re
import sys
from collections import Counter
from textwrap import shorten

# ── corpus de ejemplo ─────────────────────────────────────────────────────────

CORPUS_EJEMPLO = """
Un agente de IA es un sistema que percibe su entorno mediante herramientas y actúa para alcanzar objetivos. A diferencia de un chatbot simple, un agente puede planificar, ejecutar acciones y adaptarse a resultados inesperados.

Los agentes modernos se construyen sobre modelos de lenguaje (LLM). El LLM actúa como motor de razonamiento: decide qué hacer a continuación basándose en el contexto acumulado y las herramientas disponibles.

Las herramientas son funciones que el agente puede invocar: buscar en internet, leer archivos, ejecutar código, consultar bases de datos. Cada herramienta tiene un nombre, una descripción y un esquema de parámetros en JSON.

El loop ReAct (Reason-Act-Observe) es el patrón más común: el modelo razona sobre la situación, decide una acción, ejecuta la herramienta, observa el resultado, y repite hasta completar la tarea.

La memoria de un agente tiene varias capas: la ventana de contexto inmediata (short-term), el historial de la sesión (episódica), y una base de conocimiento recuperable (semántica). Cada capa tiene características distintas de capacidad, latencia y costo.

RAG (Retrieval-Augmented Generation) combina recuperación de documentos relevantes con generación del modelo. En lugar de depender solo del conocimiento interno del LLM, RAG busca fragmentos pertinentes en un corpus y los incluye en el contexto.

El chunking divide el corpus en fragmentos manejables antes de indexarlos. La estrategia de chunking afecta la calidad del retrieval: chunks muy pequeños pierden contexto; chunks muy grandes diluyen la señal de relevancia.

TF-IDF (Term Frequency - Inverse Document Frequency) es una técnica clásica de recuperación de información. Pondera los términos según su frecuencia en el documento y su rareza en el corpus. Los términos frecuentes en pocos documentos tienen peso alto.

BM25 es una mejora sobre TF-IDF que normaliza por longitud de documento y aplica saturación a la frecuencia de término. En benchmarks estándar supera a TF-IDF vanilla en 5-15% de precisión para queries cortas.

El reranking es un segundo paso de ordenación sobre los candidatos del primer retrieval. Un reranker (típicamente un modelo de cross-encoder) evalúa la relevancia entre query y fragmento juntos, en lugar de comparar embeddings por separado.

Los embeddings vectoriales representan texto como vectores de alta dimensión (768-3072 dims típicamente). La similitud coseno entre dos vectores mide qué tan semánticamente cercanos son sus textos originales.

Los índices vectoriales (FAISS, Chroma, Pinecone) permiten búsqueda de k-vecinos más cercanos en millones de vectores en milisegundos. FAISS usa HNSW o IVF para búsqueda aproximada con alta recall.

El naive RAG falla en cuatro escenarios: queries ambiguas que necesitan reformulación, fragmentos sin contexto suficiente, ranking por similitud diferente a ranking por utilidad, y alucinación a pesar del contexto recuperado.

Graph RAG construye un grafo de entidades y relaciones sobre el corpus. Permite responder preguntas de síntesis global que requieren integrar información de múltiples documentos. El costo de indexado puede superar los 100 dólares para corpus grandes.

El agente puede usar retrieval como herramienta en lugar de como pipeline fijo. Esto le permite decidir si buscar, qué buscar, y refinar la query basándose en los resultados anteriores. El costo en tokens es 3-4 veces mayor que el pipeline fijo.

La evaluación de RAG requiere métricas específicas: faithfulness (¿la respuesta está soportada por el contexto?), answer relevancy (¿la respuesta responde la pregunta?), context precision (¿los fragmentos recuperados son relevantes?).

El context window de los LLMs modernos varía entre 8K y 1M tokens. Un contexto más largo no siempre es mejor: los modelos pierden capacidad de atención en la zona media del contexto (fenómeno "lost in the middle").
""".strip()


# ── chunking ──────────────────────────────────────────────────────────────────

def chunking_parrafos(texto: str) -> list[dict]:
    parrafos = [p.strip() for p in texto.split("\n\n") if p.strip()]
    return [{"id": i, "texto": p, "tokens": len(p) // 4} for i, p in enumerate(parrafos)]


def tokenizar_texto(texto: str) -> list[str]:
    texto = texto.lower()
    texto = re.sub(r"[^a-záéíóúüñ\s]", " ", texto)
    return [t for t in texto.split() if len(t) > 2]


# ── TF-IDF ────────────────────────────────────────────────────────────────────

def calcular_tf(tokens: list[str]) -> dict[str, float]:
    conteo = Counter(tokens)
    total = len(tokens)
    return {t: c / total for t, c in conteo.items()}


def calcular_idf(corpus_tokens: list[list[str]]) -> dict[str, float]:
    n = len(corpus_tokens)
    df: dict[str, int] = {}
    for tokens in corpus_tokens:
        for t in set(tokens):
            df[t] = df.get(t, 0) + 1
    return {t: math.log((n + 1) / (d + 1)) + 1 for t, d in df.items()}


def tfidf_score(query_tokens: list[str], chunk_tokens: list[str], idf: dict[str, float]) -> float:
    tf = calcular_tf(chunk_tokens)
    score = 0.0
    for qt in query_tokens:
        score += tf.get(qt, 0.0) * idf.get(qt, 0.0)
    return score


# ── BM25 simplificado ─────────────────────────────────────────────────────────

def bm25_score(
    query_tokens: list[str],
    chunk_tokens: list[str],
    idf: dict[str, float],
    avg_len: float,
    k1: float = 1.5,
    b: float = 0.75,
) -> float:
    conteo = Counter(chunk_tokens)
    dl = len(chunk_tokens)
    score = 0.0
    for qt in query_tokens:
        tf = conteo.get(qt, 0)
        idf_val = idf.get(qt, 0.0)
        num = tf * (k1 + 1)
        den = tf + k1 * (1 - b + b * dl / avg_len)
        score += idf_val * (num / den if den > 0 else 0)
    return score


# ── reranker simulado ─────────────────────────────────────────────────────────

def rerank_score(query_tokens: list[str], chunk_texto: str) -> float:
    """
    Reranker simulado: pesa cobertura de query terms en el chunk,
    proximidad de términos y longitud óptima (penaliza chunks muy cortos/largos).
    No requiere modelo externo.
    """
    chunk_tokens = tokenizar_texto(chunk_texto)
    chunk_set = set(chunk_tokens)

    # cobertura: fracción de términos de la query presentes en el chunk
    cobertura = sum(1 for qt in query_tokens if qt in chunk_set) / max(len(query_tokens), 1)

    # densidad: cuántos tokens de la query aparecen vs total del chunk
    densidad = sum(chunk_tokens.count(qt) for qt in query_tokens) / max(len(chunk_tokens), 1)

    # longitud óptima: preferir chunks de 50-200 tokens
    n = len(chunk_tokens)
    if n < 10:
        long_score = 0.3
    elif n < 50:
        long_score = 0.7
    elif n <= 200:
        long_score = 1.0
    else:
        long_score = max(0.5, 1.0 - (n - 200) / 400)

    return 0.5 * cobertura + 0.3 * densidad + 0.2 * long_score


# ── pipeline RAG ──────────────────────────────────────────────────────────────

def construir_indice(chunks: list[dict]) -> dict:
    corpus_tokens = [tokenizar_texto(c["texto"]) for c in chunks]
    idf = calcular_idf(corpus_tokens)
    avg_len = sum(len(t) for t in corpus_tokens) / max(len(corpus_tokens), 1)
    return {"corpus_tokens": corpus_tokens, "idf": idf, "avg_len": avg_len}


def recuperar(query: str, chunks: list[dict], indice: dict, tecnica: str, top_k: int) -> list[dict]:
    query_tokens = tokenizar_texto(query)
    idf = indice["idf"]
    avg_len = indice["avg_len"]

    scores = []
    for i, chunk in enumerate(chunks):
        ct = indice["corpus_tokens"][i]
        if tecnica == "tfidf":
            score = tfidf_score(query_tokens, ct, idf)
        elif tecnica == "bm25":
            score = bm25_score(query_tokens, ct, idf, avg_len)
        elif tecnica == "rerank":
            # Primera pasada BM25, luego rerank
            score = bm25_score(query_tokens, ct, idf, avg_len)
        else:
            score = 0.0
        scores.append((score, i))

    scores.sort(reverse=True)

    if tecnica == "rerank":
        candidatos_idx = [i for _, i in scores[:top_k * 3]]
        rerank_scores = []
        for idx in candidatos_idx:
            rs = rerank_score(query_tokens, chunks[idx]["texto"])
            rerank_scores.append((rs, idx))
        rerank_scores.sort(reverse=True)
        top_idx = [i for _, i in rerank_scores[:top_k]]
    else:
        top_idx = [i for _, i in scores[:top_k]]

    return [
        {**chunks[idx], "score": scores[idx][0] if tecnica != "rerank" else rerank_score(query_tokens, chunks[idx]["texto"])}
        for idx in top_idx
    ]


def responder_simulado(query: str, fragmentos: list[dict]) -> str:
    """Genera respuesta simulada: concatena los fragmentos como contexto."""
    if not fragmentos:
        return "[Sin fragmentos relevantes recuperados]"
    contexto = " ".join(f["texto"] for f in fragmentos[:3])
    n_tokens_ctx = len(contexto) // 4
    return f"[Respuesta simulada basada en {len(fragmentos)} fragmentos, ~{n_tokens_ctx} tokens de contexto]"


# ── presentación ──────────────────────────────────────────────────────────────

def imprimir_resultados_query(
    query: str,
    tecnica: str,
    fragmentos: list[dict],
    tiempo_ms: float = 0.0,
) -> None:
    print(f"\n  Técnica: {tecnica.upper()}   |   query: \"{query}\"")
    print(f"  {'-'*56}")
    for i, f in enumerate(fragmentos):
        texto_corto = shorten(f["texto"], width=70, placeholder="…")
        print(f"  [{i+1}] score={f['score']:.4f}  tokens={f['tokens']}")
        print(f"      {texto_corto}")
    print()


def comparar_tecnicas(query: str, chunks: list[dict], indice: dict, top_k: int) -> None:
    tecnicas = ["tfidf", "bm25", "rerank"]
    print(f"\n{'='*64}")
    print(f"  RAG LAB — Comparativa de técnicas de retrieval")
    print(f"  Query: \"{query}\"")
    print(f"  Corpus: {len(chunks)} fragmentos  |  top-k: {top_k}")
    print(f"{'='*64}")

    todos_resultados = {}
    for t in tecnicas:
        res = recuperar(query, chunks, indice, t, top_k)
        todos_resultados[t] = res
        imprimir_resultados_query(query, t, res)

    # Acuerdo entre técnicas
    print(f"  {'─'*56}")
    print(f"  Acuerdo entre técnicas (fragmentos recuperados por múltiples)")
    print(f"  {'─'*56}")
    conteo: dict[int, int] = {}
    for res in todos_resultados.values():
        for f in res:
            conteo[f["id"]] = conteo.get(f["id"], 0) + 1

    for fid, count in sorted(conteo.items(), key=lambda x: -x[1]):
        if count > 1:
            texto_corto = shorten(chunks[fid]["texto"], width=55, placeholder="…")
            print(f"  [{fid}] ×{count} técnicas: {texto_corto}")
    if not any(c > 1 for c in conteo.values()):
        print("  (No hay acuerdo entre técnicas para esta query)")


def modo_unico(query: str, chunks: list[dict], indice: dict, tecnica: str, top_k: int) -> None:
    fragmentos = recuperar(query, chunks, indice, tecnica, top_k)
    print(f"\n{'='*64}")
    print(f"  RAG LAB — {tecnica.upper()}")
    print(f"  Query: \"{query}\"")
    print(f"  Corpus: {len(chunks)} fragmentos  |  top-k: {top_k}")
    print(f"{'='*64}")
    imprimir_resultados_query(query, tecnica, fragmentos)
    print(f"  Respuesta: {responder_simulado(query, fragmentos)}")


# ── main ──────────────────────────────────────────────────────────────────────

def main():
    parser = argparse.ArgumentParser(description="RAG lab — compara técnicas de retrieval sin API.")
    parser.add_argument("--corpus", help="Archivo .txt con el corpus (uno por párrafo)")
    parser.add_argument("--query", default="¿qué es un agente de IA?",
                        help="Query de búsqueda")
    parser.add_argument("--tecnica",
                        choices=["tfidf", "bm25", "rerank", "todos"],
                        default="todos",
                        help="Técnica de retrieval (default: todos)")
    parser.add_argument("--top-k", type=int, default=3,
                        help="Número de fragmentos a recuperar (default: 3)")
    args = parser.parse_args()

    if args.corpus:
        try:
            texto = open(args.corpus).read()
        except FileNotFoundError:
            print(f"Error: no se encontró '{args.corpus}'")
            sys.exit(1)
    else:
        texto = CORPUS_EJEMPLO
        print("[Usando corpus de ejemplo sobre agentes IA]\n")

    chunks = chunking_parrafos(texto)
    indice = construir_indice(chunks)

    print(f"[Corpus: {len(chunks)} fragmentos, {sum(c['tokens'] for c in chunks)} tokens totales]")
    print(f"[Vocabulario: {len(indice['idf'])} términos únicos]")

    if args.tecnica == "todos":
        comparar_tecnicas(args.query, chunks, indice, args.top_k)
    else:
        modo_unico(args.query, chunks, indice, args.tecnica, args.top_k)


if __name__ == "__main__":
    main()
