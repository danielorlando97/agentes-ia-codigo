# Tres capas de caching: prompt caching (Anthropic), response caching, embedding caching
#
# Cómo ejecutar:
#   make py SCRIPT=python/17-produccion/caching.py
#
# Qué esperar:
#   Demo de 3 capas de caching: prompt caching (Anthropic), response caching,
#   embedding caching. Muestra hit rates y ahorro de tokens/latencia.
#
# Variables de entorno:
#   MODEL — modelo a usar (default: claude-sonnet-4-6)

import hashlib
import json
import time
from functools import wraps
import os
import anthropic

cliente = anthropic.Anthropic()

GUIA_ESTILO = "Regla 1: usa nombres descriptivos.\nRegla 2: máximo 80 chars por línea.\n" * 50

# ─── Capa 1: Prompt caching ──────────────────────────────────────────────────
# cache_control marca el system prompt para reutilización en el servidor de Anthropic.
# Primera llamada paga cache_creation_input_tokens (25% más caro).
# Llamadas siguientes pagan cache_read_input_tokens (10× más barato).
SYSTEM_CON_CACHE = [
    {
        "type": "text",
        "text": "Eres un revisor de código experto.\n\n" + GUIA_ESTILO,
        "cache_control": {"type": "ephemeral"},  # TTL: 5 minutos
    }
]


def revisar_con_prompt_cache(codigo: str) -> anthropic.types.Message:
    respuesta = cliente.messages.create(
        model=os.environ.get("MODEL", "claude-sonnet-4-6"),
        max_tokens=512,
        system=SYSTEM_CON_CACHE,
        messages=[{"role": "user", "content": f"Revisa este código:\n{codigo}"}],
    )
    uso = respuesta.usage
    cache_hit = (getattr(uso, "cache_read_input_tokens", None) or 0) > 0
    print(f"[cache_prompt] hit={cache_hit} | "
          f"creation={getattr(uso, 'cache_creation_input_tokens', None) or 0} | "
          f"read={getattr(uso, 'cache_read_input_tokens', None) or 0}")
    return respuesta


# ─── Capa 2: Response caching ────────────────────────────────────────────────
_response_cache: dict[str, tuple[dict, float]] = {}


def cachear_respuesta(ttl_segundos: int = 300):
    """Decorador: cachea la respuesta si la query es idéntica dentro del TTL."""
    def decorator(func):
        @wraps(func)
        def wrapper(*args, **kwargs):
            clave = hashlib.sha256(
                json.dumps({"args": args, "kwargs": kwargs}, sort_keys=True).encode()
            ).hexdigest()

            if clave in _response_cache:
                valor, ts = _response_cache[clave]
                if time.time() - ts < ttl_segundos:
                    print("[cache_response] hit")
                    return valor

            resultado = func(*args, **kwargs)
            _response_cache[clave] = (resultado, time.time())
            print("[cache_response] miss — respuesta guardada")
            return resultado

        return wrapper
    return decorator


@cachear_respuesta(ttl_segundos=300)
def responder_faq(pregunta: str) -> str:
    respuesta = cliente.messages.create(
        model=os.environ.get("MODEL", "claude-sonnet-4-6"),
        max_tokens=256,
        messages=[{"role": "user", "content": pregunta}],
    )
    return respuesta.content[0].text


# ─── Capa 3: Semantic caching (por similitud de embedding) ───────────────────
# En producción: reemplazar _embedding_stub por llamada real a API de embeddings.
def _embedding_stub(texto: str) -> list[float]:
    """Placeholder — devuelve un vector constante para fines de demo."""
    return [hash(texto) % 100 / 100.0] * 10


def _similitud_coseno(a: list[float], b: list[float]) -> float:
    dot = sum(x * y for x, y in zip(a, b))
    norma_a = sum(x ** 2 for x in a) ** 0.5
    norma_b = sum(x ** 2 for x in b) ** 0.5
    if norma_a == 0 or norma_b == 0:
        return 0.0
    return dot / (norma_a * norma_b)


_semantic_cache: list[tuple[list[float], str, str]] = []  # (embedding, query, respuesta)
UMBRAL_SIMILITUD = 0.95


def responder_semantico(pregunta: str) -> str:
    emb = _embedding_stub(pregunta)

    for emb_guardado, query_guardada, respuesta_guardada in _semantic_cache:
        sim = _similitud_coseno(emb, emb_guardado)
        if sim >= UMBRAL_SIMILITUD:
            print(f"[cache_semantic] hit (similitud={sim:.3f}, query original='{query_guardada}')")
            return respuesta_guardada

    respuesta = cliente.messages.create(
        model=os.environ.get("MODEL", "claude-sonnet-4-6"),
        max_tokens=256,
        messages=[{"role": "user", "content": pregunta}],
    ).content[0].text

    _semantic_cache.append((emb, pregunta, respuesta))
    print("[cache_semantic] miss — respuesta guardada")
    return respuesta


if __name__ == "__main__":
    print("=== Prompt caching ===")
    codigo = "def f(x):\n    return x*2"
    revisar_con_prompt_cache(codigo)  # primera: cache_creation
    revisar_con_prompt_cache(codigo)  # segunda: cache_read (dentro de 5 min)

    print("\n=== Response caching ===")
    responder_faq("¿Cuál es la política de devoluciones?")
    responder_faq("¿Cuál es la política de devoluciones?")  # hit

    print("\n=== Semantic caching ===")
    responder_semantico("¿Qué hace filter_context?")
    responder_semantico("¿Qué hace filter_context?")  # mismo texto → hit
