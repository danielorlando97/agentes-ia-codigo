"""Ventana deslizante por conteo de turns.

Invariante: mantiene los últimos max_turns mensajes,
preservando siempre el primero (ancla de la tarea).

Qué demuestra:
    Nivel 1 de memoria de corto plazo: ventana deslizante por número de turnos.
    El mas simple de todos — elimina los mensajes mas antiguos cuando se supera max_turns.
    Preserva siempre el primer mensaje (ancla de la tarea).

Cómo ejecutar:
    make py SCRIPT=python/06-memoria/02-corto-plazo/nivel-1-minimo.py

Qué esperar:
    Demo de una sesion con 10 turnos y ventana de 4. Muestra qué mensajes
    se retienen y cuáles se eliminan en cada turno.

Variables de entorno:
    MODEL — modelo a usar (default: claude-sonnet-4-6)
"""


def build_context(messages: list, max_turns: int = 20) -> list:
    if len(messages) <= max_turns:
        return messages
    return [messages[0]] + messages[-(max_turns - 1):]


if __name__ == "__main__":
    msgs = [
        {"role": "user" if i % 2 == 0 else "assistant", "content": f"mensaje {i}"}
        for i in range(40)
    ]

    result = build_context(msgs, max_turns=10)
    print(f"Entrada: {len(msgs)} mensajes")
    print(f"Salida:  {len(result)} mensajes")
    print(f"Primero: {result[0]['content']}")   # siempre "mensaje 0"
    print(f"Último:  {result[-1]['content']}")  # siempre "mensaje 39"
