"""ContextManager con conteo exacto de tokens y compactación LLM como fallback.

Añade respecto al nivel 3:
- count_tokens_exact: usa client.messages.count_tokens() para precisión real (~50ms);
  fallback a estimación si la llamada falla
- La decisión de compactar usa conteo exacto (una sola llamada por turno)
- El loop de evicción usa estimación rápida (sin overhead de API)
- Compactación LLM: cuando FIFO no basta, un modelo barato resume el historial intermedio
- ContextMetrics: fifo_evictions, llm_compactions, tokens_saved

Qué demuestra:
    Nivel 4: conteo exacto via count_tokens API + compactación LLM como fallback.
    Cuando la estimación no es suficiente (código, JSON) usa la API real para contar.
    La compactación LLM se activa cuando la evicción no es suficiente.

Cómo ejecutar:
    make py SCRIPT=python/06-memoria/02-corto-plazo/nivel-4-completo.py

Qué esperar:
    Demo con conteo exacto de tokens por mensaje. Más lento que el nivel 3
    (1 llamada extra a count_tokens por turno) pero más preciso.

Variables de entorno:
    MODEL         — modelo principal (default: claude-sonnet-4-6)
    COMPACT_MODEL — modelo de compactación (default: claude-haiku-4-5-20251001)
"""
import os
import json
import logging
from dataclasses import dataclass, field
from typing import Optional

import anthropic

logging.basicConfig(level=logging.INFO, format="%(message)s")
logger = logging.getLogger(__name__)

MODEL = os.environ.get("MODEL", "claude-sonnet-4-6")
COMPACT_MODEL = os.environ.get("COMPACT_MODEL", "claude-haiku-4-5-20251001")


@dataclass
class ContextBudget:
    total: int = 128_000
    system: int = 4_000
    retrieved: int = 3_000
    tools: int = 2_000
    response: int = 8_000
    threshold: float = 0.75

    @property
    def history(self) -> int:
        return self.total - self.system - self.retrieved - self.tools - self.response

    @property
    def compact_trigger(self) -> int:
        return int(self.history * self.threshold)


@dataclass
class ContextMetrics:
    fifo_evictions: int = 0
    llm_compactions: int = 0
    tokens_saved: int = 0


class ContextManager:
    def __init__(
        self,
        client: anthropic.Anthropic,
        budget: Optional[ContextBudget] = None,
        system_prompt: str = "",
    ):
        self.client = client
        self.budget = budget or ContextBudget()
        self.system_prompt = system_prompt
        self.metrics = ContextMetrics()

    def count_tokens_exact(self, messages: list[dict]) -> int:
        """Conteo exacto via API (una llamada por turno). Fallback a estimación."""
        try:
            r = self.client.messages.count_tokens(
                model=MODEL,
                system=self.system_prompt,
                messages=messages,
            )
            return r.input_tokens
        except Exception:
            return self._estimate(messages)

    @staticmethod
    def _estimate(messages: list[dict]) -> int:
        return sum(len(json.dumps(m, ensure_ascii=False)) for m in messages) // 4

    def _fifo_reduce(self, messages: list[dict], budget: int) -> tuple[list[dict], int]:
        """Evicción FIFO sin llamadas adicionales a la API."""
        working = list(messages)
        evicted = 0
        while self._estimate(working) > budget:
            for i, m in enumerate(working):
                if not m.get("pinned"):
                    working.pop(i)
                    evicted += 1
                    break
            else:
                break
        return working, evicted

    def _llm_compact(self, messages: list[dict]) -> list[dict]:
        """Resume el historial intermedio con un modelo barato."""
        if len(messages) <= 8:
            return messages

        head = messages[:2]
        tail = messages[-6:]
        middle = messages[2:-6]

        if not middle:
            return messages

        tokens_before = self._estimate(messages)
        logger.info(f"[compactación LLM] resumiendo {len(middle)} mensajes intermedios")

        response = self.client.messages.create(
            model=COMPACT_MODEL,
            max_tokens=1_500,
            messages=[{
                "role": "user",
                "content": (
                    "Resume este historial preservando exactamente: "
                    "cada herramienta llamada y su resultado, "
                    "cada decisión tomada y por qué, "
                    "el estado actual de la tarea.\n\n"
                    f"Historial: {json.dumps(middle, ensure_ascii=False)[:12_000]}"
                ),
            }],
        )

        summary = response.content[0].text
        compressed = {"role": "user", "content": f"[HISTORIAL COMPRIMIDO]\n{summary}"}
        result = head + [compressed] + tail

        tokens_after = self._estimate(result)
        self.metrics.llm_compactions += 1
        self.metrics.tokens_saved += max(0, tokens_before - tokens_after)
        logger.info(f"[compactación LLM] ~{tokens_before}t → ~{tokens_after}t")
        return result

    def prepare(self, messages: list[dict]) -> list[dict]:
        """Aplica reducción si el historial supera el umbral. Una llamada a count_tokens."""
        current = self.count_tokens_exact(messages)
        if current <= self.budget.compact_trigger:
            return messages

        logger.info(f"[contexto] {current}t > threshold={self.budget.compact_trigger}t")

        # Intento 1: FIFO (sin coste adicional de API)
        reduced, evicted = self._fifo_reduce(messages, self.budget.history)
        self.metrics.fifo_evictions += evicted

        if self._estimate(reduced) <= self.budget.history:
            return reduced

        # Intento 2: FIFO no fue suficiente → compactación LLM
        return self._llm_compact(reduced)

    def report(self) -> dict:
        return {
            "fifo_evictions": self.metrics.fifo_evictions,
            "llm_compactions": self.metrics.llm_compactions,
            "tokens_saved_est": self.metrics.tokens_saved,
        }


if __name__ == "__main__":
    client = anthropic.Anthropic()
    budget = ContextBudget(total=10_000, system=500, retrieved=300, tools=200, response=500)
    mgr = ContextManager(client, budget=budget, system_prompt="Eres un asistente de código.")

    history = [
        {"role": "user", "content": "Analiza este repositorio.", "pinned": True},
        *[
            {
                "role": "assistant" if i % 2 else "user",
                "content": f"Turno {i}: " + "resultado de análisis. " * 40,
            }
            for i in range(1, 30)
        ],
    ]

    prepared = mgr.prepare(history)
    print(f"Historial final: {len(prepared)} mensajes")
    print(f"Métricas: {mgr.report()}")
