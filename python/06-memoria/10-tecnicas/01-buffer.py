"""Buffer conversacional con evicción por tokens y seguridad concurrente.

Implementa tres invariantes:
  1. Tokens acotados: nunca supera max_tokens (descontando reserva de respuesta).
  2. Paridad tool_use/tool_result: nunca evicta un tool_use cuyo result sigue presente.
  3. Thread-safe: mutex previene corrupción de estado bajo escrituras concurrentes.

Cómo ejecutar:
    make py SCRIPT=python/06-memoria/10-tecnicas/01-buffer.py

Qué esperar:
    Demo con historial de 15 mensajes (incluyendo tool calls) y presupuesto de 300 tokens.
    Muestra la evicción respetando la paridad tool_use/tool_result.
"""

import json
import threading
from dataclasses import dataclass, field
from typing import Optional


def estimar_tokens(mensaje: dict) -> int:
    return len(json.dumps(mensaje, ensure_ascii=False)) // 4


@dataclass
class BufferConversacional:
    max_tokens: int
    reserva_respuesta: int = 2000
    _mensajes: list = field(default_factory=list, init=False, repr=False)
    _lock: threading.Lock = field(default_factory=threading.Lock, init=False, repr=False)

    def __post_init__(self) -> None:
        self._budget = self.max_tokens - self.reserva_respuesta

    def agregar(self, mensaje: dict, pinned: bool = False) -> None:
        with self._lock:
            msg = {**mensaje, "_pinned": pinned}
            self._mensajes.append(msg)
            self._evictar()

    def snapshot(self) -> list[dict]:
        with self._lock:
            return [
                {k: v for k, v in m.items() if not k.startswith("_")}
                for m in self._mensajes
            ]

    def tokens_actuales(self) -> int:
        with self._lock:
            return sum(estimar_tokens(m) for m in self._mensajes)

    def _evictar(self) -> None:
        while self._tokens_actuales_sin_lock() > self._budget:
            idx = self._primer_eviccionable()
            if idx is None:
                break
            self._mensajes.pop(idx)

    def _tokens_actuales_sin_lock(self) -> int:
        return sum(estimar_tokens(m) for m in self._mensajes)

    def _primer_eviccionable(self) -> Optional[int]:
        tool_use_ids = {
            m.get("id") for m in self._mensajes if m.get("type") == "tool_use"
        }
        tool_result_ids = {
            m.get("tool_use_id")
            for m in self._mensajes
            if m.get("type") == "tool_result"
        }
        pares_activos = tool_use_ids & tool_result_ids

        for i, m in enumerate(self._mensajes):
            if m.get("_pinned"):
                continue
            if m.get("type") == "tool_use" and m.get("id") in pares_activos:
                continue
            if m.get("type") == "tool_result" and m.get("tool_use_id") in pares_activos:
                continue
            return i
        return None

    def __len__(self) -> int:
        with self._lock:
            return len(self._mensajes)


if __name__ == "__main__":
    buf = BufferConversacional(max_tokens=600, reserva_respuesta=200)

    buf.agregar({"role": "user", "content": "Analiza el módulo de pagos"}, pinned=True)
    buf.agregar({"role": "assistant", "content": "Voy a revisar los archivos."})

    for i in range(4):
        use_id = f"tu_{i}"
        buf.agregar({
            "role": "assistant",
            "type": "tool_use",
            "id": use_id,
            "name": "read_file",
            "input": {"path": f"src/pagos/modulo_{i}.py"},
        })
        buf.agregar({
            "role": "user",
            "type": "tool_result",
            "tool_use_id": use_id,
            "content": f"Contenido del módulo {i}: " + "x" * 80,
        })

    buf.agregar({"role": "assistant", "content": "Análisis completo. El módulo 2 tiene el problema."})
    buf.agregar({"role": "user", "content": "¿Qué tipo de problema?"})

    snap = buf.snapshot()
    print(f"Mensajes en buffer: {len(snap)}")
    print(f"Tokens estimados: {buf.tokens_actuales()}")
    print(f"Budget: {buf._budget} tokens")
    print()

    for m in snap:
        tipo = m.get("type", m.get("role", "?"))
        contenido = m.get("content", m.get("name", ""))
        preview = str(contenido)[:60]
        pinned = "[PINNED] " if m.get("_pinned") else ""
        print(f"  {pinned}{tipo}: {preview}")
