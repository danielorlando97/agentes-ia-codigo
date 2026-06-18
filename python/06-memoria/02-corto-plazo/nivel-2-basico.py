"""Evicción FIFO por presupuesto de tokens con mensajes anclados.

Añade respecto al nivel 1:
- Estimación de tokens (len(json) / 4, error ±15% texto, ±30% código)
- Evicción por tokens en lugar de conteo de turns
- Mensajes con pinned=True nunca se evictan

Qué demuestra:
    Nivel 2: evicción FIFO por presupuesto de tokens con mensajes anclados (pinned).
    Mejor que el nivel 1 porque el presupuesto es proporcional al costo real.
    Los mensajes con pinned=True (p.ej. la tarea original) nunca se evictan.

Cómo ejecutar:
    make py SCRIPT=python/06-memoria/02-corto-plazo/nivel-2-basico.py

Qué esperar:
    Demo con presupuesto de 2000 tokens y mensajes anclados. Muestra la evicción
    por tokens en acción con estimación ~4 chars/token.

Variables de entorno:
    MODEL — modelo a usar (default: claude-sonnet-4-6)
"""
import json


HISTORY_BUDGET = 110_000  # tokens disponibles para el historial


def estimate_tokens(messages: list[dict]) -> int:
    return sum(len(json.dumps(m, ensure_ascii=False)) for m in messages) // 4


def build_context(messages: list[dict], budget: int = HISTORY_BUDGET) -> list[dict]:
    working = list(messages)

    while estimate_tokens(working) > budget:
        for i, m in enumerate(working):
            if not m.get("pinned"):
                working.pop(i)
                break
        else:
            break  # todos anclados — no se puede reducir más

    return working


if __name__ == "__main__":
    msgs = [{"role": "user", "content": "Analiza este repositorio.", "pinned": True}]
    for i in range(1, 20):
        msgs.append({
            "role": "assistant" if i % 2 else "user",
            "content": "resultado de herramienta: " + "x" * 800,
        })

    budget = 4_000
    result = build_context(msgs, budget=budget)

    print(f"Entrada: {len(msgs)} msgs, ~{estimate_tokens(msgs)} tokens")
    print(f"Salida:  {len(result)} msgs, ~{estimate_tokens(result)} tokens (budget={budget})")
    print(f"Anclado preservado: {result[0].get('pinned')} → '{result[0]['content'][:40]}'")
