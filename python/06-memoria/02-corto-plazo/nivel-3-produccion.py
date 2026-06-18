"""Ensamblador de contexto con presupuesto explícito por región.

Añade respecto al nivel 2:
- ContextBudget: modelo de presupuesto con 5 regiones (system, retrieved, tools, history, response)
- Cada región tiene presupuesto propio; solo el historial es variable
- Umbral configurable (threshold) que activa la evicción antes de llegar al límite
- clip_text para recortar el system prompt y la memoria recuperada si exceden su región
- Log cuando se activa la reducción

Qué demuestra:
    Nivel 3: ContextBudget con 5 regiones (system, retrieved, tools, history, response).
    Cada región tiene su presupuesto propio. Solo el historial es variable.
    Umbral configurable que activa la evicción antes de llegar al límite duro.

Cómo ejecutar:
    make py SCRIPT=python/06-memoria/02-corto-plazo/nivel-3-produccion.py

Qué esperar:
    Demo con presupuesto por región y umbral de evicción al 80%. Muestra
    cómo cada región consume su presupuesto independientemente.

Variables de entorno:
    MODEL — modelo a usar (default: claude-sonnet-4-6)
"""
import json
import logging
from dataclasses import dataclass

logging.basicConfig(level=logging.INFO, format="%(message)s")
logger = logging.getLogger(__name__)


@dataclass
class ContextBudget:
    total: int = 128_000
    system: int = 4_000
    retrieved: int = 3_000
    tools: int = 2_000
    response: int = 8_000
    threshold: float = 0.75  # activa reducción al 75% del budget de history

    @property
    def history(self) -> int:
        return self.total - self.system - self.retrieved - self.tools - self.response

    @property
    def compact_trigger(self) -> int:
        return int(self.history * self.threshold)


def estimate_tokens(obj) -> int:
    return len(json.dumps(obj, ensure_ascii=False)) // 4


def clip_text(text: str, max_tokens: int) -> str:
    """Recorta texto al presupuesto de tokens (aproximación por chars)."""
    max_chars = max_tokens * 4
    return text[:max_chars] if len(text) > max_chars else text


def reduce_history(messages: list[dict], budget: int) -> list[dict]:
    working = list(messages)
    while estimate_tokens(working) > budget:
        for i, m in enumerate(working):
            if not m.get("pinned"):
                working.pop(i)
                break
        else:
            break
    return working


def build_context(
    history: list[dict],
    system_prompt: str,
    retrieved: str = "",
    tools: list | None = None,
    budget: ContextBudget | None = None,
) -> dict:
    budget = budget or ContextBudget()
    tools = tools or []

    history_tokens = estimate_tokens(history)
    if history_tokens > budget.compact_trigger:
        logger.info(
            f"[contexto] historial={history_tokens}t > threshold={budget.compact_trigger}t → reduciendo"
        )
        history = reduce_history(history, budget.history)
        logger.info(f"[contexto] historial reducido a ~{estimate_tokens(history)}t")

    return {
        "system": clip_text(system_prompt, budget.system),
        "retrieved": clip_text(retrieved, budget.retrieved),
        "tools": tools,
        "messages": history,
    }


if __name__ == "__main__":
    budget = ContextBudget(total=10_000, system=500, retrieved=300, tools=200, response=500)

    history = [
        {"role": "user", "content": "Analiza este repositorio.", "pinned": True},
        *[
            {"role": "assistant" if i % 2 else "user", "content": "contenido: " + "x" * 300}
            for i in range(1, 25)
        ],
    ]

    ctx = build_context(
        history=history,
        system_prompt="Eres un asistente de análisis de código experto en Python.",
        retrieved="Sesión anterior: el usuario analizó auth.py y encontró un bug en validate_token().",
        budget=budget,
    )

    print(f"Historial final: {len(ctx['messages'])} mensajes")
    print(f"Anclado preservado: '{ctx['messages'][0]['content']}'")
    print(f"System clipeado a: {len(ctx['system'])} chars")
