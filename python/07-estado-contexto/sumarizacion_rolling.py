"""Sumarización rolling del historial conversacional.

SummarizationBuffer aplica compactación LLM cuando el historial supera el umbral.
Preserva head+tail configurable; el segmento intermedio se reemplaza por un resumen.
SummarizationConfig centraliza todos los parámetros de la política de compactación.

Requiere: pip install anthropic

Cómo ejecutar:
    make py SCRIPT=python/07-estado-contexto/sumarizacion_rolling.py

Qué esperar:
    Conversacion de 8 turnos que supera el umbral de compactacion.
    Muestra la sumarizacion rolling en accion: el segmento intermedio
    se reemplaza por un resumen generado por el modelo.

Variables de entorno:
    MODEL         — modelo principal (default: claude-sonnet-4-6)
    COMPACT_MODEL — modelo de sumarizacion (default: claude-haiku-4-5-20251001)
"""
import json
from dataclasses import dataclass
from typing import Optional

import anthropic


def _estimate_tokens(messages: list[dict]) -> int:
    return sum(len(json.dumps(m, ensure_ascii=False)) for m in messages) // 4


def validar_paridad(messages: list[dict]) -> list[str]:
    """Devuelve IDs huérfanos de tool_use/tool_result. Lista vacía = OK."""
    uses: set[str] = set()
    results: set[str] = set()
    for msg in messages:
        for block in msg.get("content") or []:
            if isinstance(block, dict):
                if block.get("type") == "tool_use":
                    uses.add(block["id"])
                elif block.get("type") == "tool_result":
                    results.add(block["tool_use_id"])
    return list((uses - results) | (results - uses))


@dataclass
class SummarizationConfig:
    head: int = 2                                  # mensajes iniciales siempre preservados
    tail: int = 6                                  # mensajes recientes siempre preservados
    max_tokens: int = 110_000                      # presupuesto total del historial
    threshold: float = 0.75                        # activar al 75% del presupuesto
    model: str = "claude-haiku-4-5-20251001"       # modelo barato para resumir
    summary_max_tokens: int = 1_500                # longitud máxima del resumen


class SummarizationBuffer:
    def __init__(
        self,
        client: anthropic.Anthropic,
        config: Optional[SummarizationConfig] = None,
    ):
        self.client = client
        self.cfg = config or SummarizationConfig()
        self._messages: list[dict] = []
        self.compaction_count = 0

    def add(self, message: dict) -> None:
        self._messages.append(message)

    def get(self) -> list[dict]:
        return list(self._messages)

    @property
    def tokens(self) -> int:
        return _estimate_tokens(self._messages)

    def _should_summarize(self) -> bool:
        trigger = int(self.cfg.max_tokens * self.cfg.threshold)
        return self.tokens > trigger

    def _build_compaction_prompt(self, messages: list[dict]) -> str:
        """Prompt específico que instruye a preservar valores exactos, no parafrasear."""
        return (
            "Resume este historial de un agente. Preserva exactamente:\n"
            "- Cada herramienta llamada, sus parámetros y su resultado (números, IDs, rutas)\n"
            "- Cada decisión tomada y su justificación\n"
            "- Restricciones y constraints del usuario\n"
            "- El estado actual de la tarea y el progreso\n"
            "No parafrasees valores numéricos ni identificadores — cópialos literalmente.\n\n"
            f"Historial: {json.dumps(messages, ensure_ascii=False)[:14_000]}"
        )

    def compact(self) -> bool:
        """Compacta si supera el umbral. Devuelve True si se compactó."""
        if not self._should_summarize():
            return False

        msgs = self._messages
        if len(msgs) <= self.cfg.head + self.cfg.tail:
            return False

        head = msgs[: self.cfg.head]
        tail = msgs[len(msgs) - self.cfg.tail :]
        middle = msgs[self.cfg.head : len(msgs) - self.cfg.tail]

        if not middle:
            return False

        response = self.client.messages.create(
            model=self.cfg.model,
            max_tokens=self.cfg.summary_max_tokens,
            messages=[{
                "role": "user",
                "content": self._build_compaction_prompt(middle),
            }],
        )
        summary = response.content[0].text
        compressed = {"role": "user", "content": f"[HISTORIAL COMPRIMIDO]\n{summary}"}

        self._messages = head + [compressed] + tail
        self.compaction_count += 1

        # Los tool_use/tool_result del segmento comprimido desaparecen — es esperado.
        # Orphans en head o tail indicarían un problema real con el punto de corte.
        orphans_in_boundary = validar_paridad(head + tail)
        if orphans_in_boundary:
            # El punto de corte partió un par tool_use/tool_result — ajustar head/tail
            print(f"  [aviso] paridad en boundary: {orphans_in_boundary}")

        return True

    def __len__(self) -> int:
        return len(self._messages)


if __name__ == "__main__":
    client = anthropic.Anthropic()

    cfg = SummarizationConfig(
        head=1,
        tail=2,
        max_tokens=3_000,
        threshold=0.6,
        summary_max_tokens=300,
    )
    buf = SummarizationBuffer(client, config=cfg)

    # Simular un historial creciente
    buf.add({"role": "user", "content": "Analiza el repo y encuentra bugs de seguridad."})
    for i in range(10):
        buf.add({"role": "assistant", "content": f"Analicé auth_{i}.py: sin vulnerabilidades evidentes."})
        buf.add({"role": "user", "content": f"Continúa con el módulo {i + 1}."})

    print(f"Antes de compact: {len(buf)} msgs, ~{buf.tokens} tokens")
    compactado = buf.compact()
    print(f"Compactó: {compactado} | Tras compact: {len(buf)} msgs, ~{buf.tokens} tokens")
    print(f"Compacciones totales: {buf.compaction_count}")

    if compactado:
        print(f"\nMensaje comprimido (primeros 200 chars):")
        print(f"  {buf.get()[1]['content'][:200]}")
