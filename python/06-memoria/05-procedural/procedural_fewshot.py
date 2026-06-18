"""Memoria procedural como few-shot dinámico.

En lugar de reglas explícitas (condición → acción), el conocimiento
procedural se captura como pares (contexto_entrada, salida_deseada).
En inferencia, los ejemplos más similares al turno actual se inyectan
como few-shots en el prompt.

Ventaja sobre reglas: el modelo aprende del ejemplo completo, no de
una descripción abstracta de la regla.
Limitación: la similitud requiere embeddings reales para funcionar bien;
aquí se usa Jaccard sobre palabras como aproximación sin dependencias.

Cómo ejecutar:
    make py SCRIPT=python/06-memoria/05-procedural/procedural_fewshot.py

Qué esperar:
    Demo de few-shot dinámico: los ejemplos más similares al turno actual
    se inyectan como few-shots. Comparación con prompt sin ejemplos.

Variables de entorno:
    MODEL — modelo a usar (default: claude-sonnet-4-6)
"""
import math
from dataclasses import dataclass, field


@dataclass
class Ejemplo:
    contexto: str       # input que disparó el ejemplo
    salida: str         # output que se consideró correcto
    score: float = 1.0  # refuerzo acumulado; sube con aprobaciones, baja con correcciones


def _jaccard(a: str, b: str) -> float:
    """Similitud Jaccard sobre conjunto de palabras. Placeholder para embeddings reales."""
    sa = set(a.lower().split())
    sb = set(b.lower().split())
    if not sa or not sb:
        return 0.0
    return len(sa & sb) / len(sa | sb)


class BufferFewShot:
    def __init__(self, max_ejemplos: int = 50):
        self._ejemplos: list[Ejemplo] = []
        self.max_ejemplos = max_ejemplos

    def add(self, contexto: str, salida: str) -> None:
        """Agrega un ejemplo. Si el buffer está lleno, elimina el de menor score."""
        if len(self._ejemplos) >= self.max_ejemplos:
            self._ejemplos.sort(key=lambda e: e.score)
            self._ejemplos.pop(0)
        self._ejemplos.append(Ejemplo(contexto=contexto, salida=salida))

    def reinforce(self, contexto: str, delta: float = 0.1) -> None:
        """Refuerza los ejemplos más similares al contexto dado."""
        for e in self._recuperar_similares(contexto, top_k=3):
            e.score = min(2.0, e.score + delta)

    def penalize(self, contexto: str, delta: float = 0.15) -> None:
        """Penaliza los ejemplos más similares (el agente hizo algo incorrecto)."""
        for e in self._recuperar_similares(contexto, top_k=3):
            e.score = max(0.0, e.score - delta)
            if e.score < 0.1:
                self._ejemplos.remove(e)

    def _recuperar_similares(self, query: str, top_k: int) -> list[Ejemplo]:
        scored = [(e, _jaccard(query, e.contexto) * e.score) for e in self._ejemplos]
        scored.sort(key=lambda x: x[1], reverse=True)
        return [e for e, _ in scored[:top_k]]

    def build_few_shot_block(self, query: str, top_k: int = 3) -> str:
        """Bloque de texto listo para inyectar como few-shots en el system prompt."""
        ejemplos = self._recuperar_similares(query, top_k=top_k)
        if not ejemplos:
            return ""
        partes = ["## Ejemplos de comportamiento esperado\n"]
        for i, e in enumerate(ejemplos, 1):
            partes.append(f"Ejemplo {i}:\nEntrada: {e.contexto}\nSalida: {e.salida}\n")
        return "\n".join(partes)

    def __len__(self) -> int:
        return len(self._ejemplos)


if __name__ == "__main__":
    buf = BufferFewShot(max_ejemplos=20)

    buf.add(
        contexto="Explica qué es un decorador en Python",
        salida="Un decorador es una función que envuelve a otra función para modificar su comportamiento sin cambiar su código.",
    )
    buf.add(
        contexto="Escribe un ejemplo de función recursiva",
        salida="def factorial(n):\n    return 1 if n <= 1 else n * factorial(n - 1)",
    )
    buf.add(
        contexto="Corrige el manejo de errores en este código",
        salida="Añade try/except específico (no bare except), loggea el error con traceback, y relanza si no puedes manejar.",
    )
    buf.add(
        contexto="Resume este texto en 3 puntos",
        salida="• Punto 1: ...\n• Punto 2: ...\n• Punto 3: ...",
    )

    query = "Dame un ejemplo de recursión en Python"
    print(f"Buffer: {len(buf)} ejemplos\n")
    print(buf.build_few_shot_block(query, top_k=2))

    # Refuerzo: el usuario aprobó la respuesta del ejemplo de recursión
    buf.reinforce("función recursiva")
    print(f"Score del ejemplo de recursión tras refuerzo: "
          f"{buf._ejemplos[1].score:.2f}")
