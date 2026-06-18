"""Estrategias de truncación del historial conversacional.

AlmacenHistorial centraliza las operaciones de reducción:
- truncar_fifo:        elimina desde el principio, respetando paridad tool_use/tool_result
- truncar_head_tail:   preserva cabeza + cola, descarta el medio
- limpiar_tool_results: vacía el content de tool_results antiguos (observation masking)
- validar_paridad:     detecta tool_use sin tool_result correspondiente (o viceversa)

Cómo ejecutar:
    make py SCRIPT=python/07-estado-contexto/truncacion.py

Qué esperar:
    Demo de 4 estrategias de truncacion aplicadas al mismo historial.
    Incluye validacion de paridad tool_use/tool_result para detectar
    historiales malformados antes de enviar a la API.

Variables de entorno:
    MODEL — modelo a usar (default: claude-sonnet-4-6)
"""
import json
from dataclasses import dataclass, field


def _estimate_tokens(messages: list[dict]) -> int:
    return sum(len(json.dumps(m, ensure_ascii=False)) for m in messages) // 4


def validar_paridad(messages: list[dict]) -> list[str]:
    """Devuelve IDs de tool_use o tool_result huérfanos. Lista vacía = paridad correcta."""
    uses: set[str] = set()
    results: set[str] = set()

    for msg in messages:
        for block in msg.get("content") or []:
            if not isinstance(block, dict):
                continue
            if block.get("type") == "tool_use":
                uses.add(block["id"])
            elif block.get("type") == "tool_result":
                results.add(block["tool_use_id"])

    return list((uses - results) | (results - uses))


def _has_tool_use(msg: dict) -> bool:
    return any(
        isinstance(b, dict) and b.get("type") == "tool_use"
        for b in (msg.get("content") or [])
    )


def truncar_fifo(messages: list[dict], max_tokens: int) -> list[dict]:
    """Elimina mensajes más antiguos hasta cumplir el presupuesto.

    Si elimina un assistant con tool_use, elimina también el user inmediatamente
    siguiente (que contiene los tool_results) para preservar la paridad.
    """
    working = list(messages)
    while _estimate_tokens(working) > max_tokens and len(working) > 1:
        removed = working.pop(0)
        if (
            working
            and removed.get("role") == "assistant"
            and _has_tool_use(removed)
            and working[0].get("role") == "user"
        ):
            working.pop(0)  # eliminar también los tool_results del par
    return working


def truncar_head_tail(
    messages: list[dict],
    max_tokens: int,
    head: int = 2,
    tail: int = 6,
) -> list[dict]:
    """Preserva los primeros `head` y últimos `tail` mensajes; descarta el medio.

    Si head+tail aún excede el presupuesto, aplica FIFO adicional sobre la cola.
    """
    if len(messages) <= head + tail:
        return messages

    result = messages[:head] + messages[len(messages) - tail:]
    if _estimate_tokens(result) > max_tokens:
        result = truncar_fifo(result, max_tokens)
    return result


def limpiar_tool_results(messages: list[dict], min_age: int = 4) -> list[dict]:
    """Reemplaza el content de tool_results con más de min_age ciclos de antigüedad.

    La estructura del mensaje (role, tool_use_id) se preserva — solo se vacía el content.
    Los últimos min_age tool_results se mantienen intactos.
    """
    # Recorrer en reversa para contar desde el más reciente
    tool_result_count = 0
    result_msgs = []

    for msg in reversed(messages):
        content = msg.get("content")
        if not isinstance(content, list):
            result_msgs.insert(0, msg)
            continue

        new_blocks = []
        for block in content:
            if isinstance(block, dict) and block.get("type") == "tool_result":
                tool_result_count += 1
                if tool_result_count > min_age:
                    block = {**block, "content": [{"type": "text", "text": "[cleared]"}]}
            new_blocks.append(block)
        result_msgs.insert(0, {**msg, "content": new_blocks})

    return result_msgs


class AlmacenHistorial:
    """Wrapper sobre la lista de mensajes con operaciones de truncación integradas."""

    def __init__(self, max_tokens: int = 110_000):
        self.max_tokens = max_tokens
        self._messages: list[dict] = []

    def add(self, message: dict) -> None:
        self._messages.append(message)

    def get(self) -> list[dict]:
        return list(self._messages)

    @property
    def tokens(self) -> int:
        return _estimate_tokens(self._messages)

    def apply_fifo(self) -> None:
        self._messages = truncar_fifo(self._messages, self.max_tokens)

    def apply_head_tail(self, head: int = 2, tail: int = 6) -> None:
        self._messages = truncar_head_tail(self._messages, self.max_tokens, head, tail)

    def clear_tool_results(self, min_age: int = 4) -> None:
        self._messages = limpiar_tool_results(self._messages, min_age)

    def check_parity(self) -> list[str]:
        return validar_paridad(self._messages)

    def __len__(self) -> int:
        return len(self._messages)


if __name__ == "__main__":
    # Historial de demo con tool calls intercalados
    historial = AlmacenHistorial(max_tokens=2_000)

    tool_id = "tu_01"
    historial.add({"role": "user", "content": "Analiza el repo."})
    for i in range(8):
        historial.add({
            "role": "assistant",
            "content": [{"type": "tool_use", "id": f"tu_{i:02d}", "name": "read_file",
                         "input": {"path": f"file_{i}.py"}}],
        })
        historial.add({
            "role": "user",
            "content": [{"type": "tool_result", "tool_use_id": f"tu_{i:02d}",
                         "content": [{"type": "text", "text": "x" * 200}]}],
        })

    print(f"Antes: {len(historial)} msgs, ~{historial.tokens} tokens")
    print(f"Paridad: {historial.check_parity() or 'OK'}")

    # Limpiar tool results antiguos (preservar últimos 4)
    historial.clear_tool_results(min_age=4)
    print(f"\nTras clear_tool_results(min_age=4): ~{historial.tokens} tokens")
    print(f"Paridad tras limpiar: {historial.check_parity() or 'OK'}")

    # Truncar preservando head+tail
    historial.apply_head_tail(head=1, tail=4)
    print(f"\nTras head_tail(1,4): {len(historial)} msgs, ~{historial.tokens} tokens")
    print(f"Paridad final: {historial.check_parity() or 'OK'}")
