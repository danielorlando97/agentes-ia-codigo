"""Tree of Thoughts (ToT) — Yao et al. 2023 (arXiv:2305.10601).

tot_bfs: beam search en anchura; conserva los BEAM_WIDTH mejores por nivel.
tot_dfs: búsqueda en profundidad con backtracking cuando el evaluador dice 'impossible'.
prompt_propuesta: genera k pensamientos candidatos desde el estado actual (temp=0.7).
prompt_evaluacion: clasifica cada estado como sure/maybe/impossible (temp=0.0).
es_solucion: función configurable por el llamador — ToT no asume qué es una solución.

Requiere: pip install anthropic

Cómo ejecutar:
    make py SCRIPT=python/08-bucle/tree_of_thoughts.py

Qué esperar:
    BFS mantiene los BEAM_WIDTH mejores pensamientos por nivel.
    DFS hace backtracking cuando el evaluador marca 'impossible'.
    Muestra el árbol de exploración y el camino al resultado final.

Variables de entorno:
    MODEL — modelo para propuestas y evaluación (default: claude-sonnet-4-6)
"""
from dataclasses import dataclass
from typing import Callable, Optional
import os
import anthropic

MODEL = os.environ.get("MODEL", "claude-sonnet-4-6")

PROMPT_PROPUESTA = (
    "Estado actual del problema:\n{estado}\n\n"
    "Genera {k} posibles próximos pasos (uno por línea, comenzando con un verbo)."
)

PROMPT_EVALUACION = (
    "Objetivo: {objetivo}\n"
    "Estado: {estado}\n\n"
    "¿Es posible alcanzar el objetivo desde este estado?\n"
    "Responde SOLO una de estas tres palabras: sure | maybe | impossible"
)


@dataclass
class ToTAgent:
    client:           anthropic.Anthropic
    es_solucion:      Callable[[str, str], bool]
    branching_factor: int   = 3
    profundidad_max:  int   = 3
    beam_width:       int   = 3
    model:            str   = MODEL

    def _proponer(self, estado: str, k: int) -> list[str]:
        resp = self.client.messages.create(
            model=self.model,
            max_tokens=300,
            temperature=0.7,
            messages=[{"role": "user", "content": PROMPT_PROPUESTA.format(estado=estado, k=k)}],
        )
        lineas = [l.strip() for l in resp.content[0].text.splitlines() if l.strip()]
        return lineas[:k]

    def _evaluar(self, estado: str, objetivo: str) -> str:
        resp = self.client.messages.create(
            model=self.model,
            max_tokens=5,
            temperature=0.0,
            messages=[{"role": "user", "content": PROMPT_EVALUACION.format(objetivo=objetivo, estado=estado)}],
        )
        t = resp.content[0].text.strip().lower()
        if "sure"       in t: return "sure"
        if "impossible" in t: return "impossible"
        return "maybe"

    def tot_bfs(self, estado_inicial: str, objetivo: str) -> Optional[str]:
        """Beam search en anchura. Conserva los BEAM_WIDTH mejores nodos por nivel."""
        frontera = [estado_inicial]

        for profundidad in range(self.profundidad_max):
            candidatos: list[tuple[str, str]] = []

            for estado in frontera:
                if self.es_solucion(estado, objetivo):
                    return estado
                for prop in self._proponer(estado, self.branching_factor):
                    nuevo = f"{estado}\n{prop}"
                    eval_ = self._evaluar(nuevo, objetivo)
                    if eval_ != "impossible":
                        candidatos.append((nuevo, eval_))

            candidatos.sort(key=lambda x: 0 if x[1] == "sure" else 1)
            frontera = [e for e, _ in candidatos[: self.beam_width]]
            print(f"  [BFS depth={profundidad+1}] {len(frontera)} nodos en frontera")

            if not frontera:
                break

        return frontera[0] if frontera else None

    def tot_dfs(self, estado: str, objetivo: str, _depth: int = 0) -> Optional[str]:
        """DFS con backtracking. Retrocede cuando el evaluador dice 'impossible'."""
        if self.es_solucion(estado, objetivo):
            return estado
        if _depth >= self.profundidad_max:
            return None

        for prop in self._proponer(estado, self.branching_factor):
            nuevo = f"{estado}\n{prop}"
            if self._evaluar(nuevo, objetivo) == "impossible":
                print(f"  [DFS depth={_depth+1}] backtrack: '{prop[:40]}'")
                continue
            resultado = self.tot_dfs(nuevo, objetivo, _depth + 1)
            if resultado:
                return resultado

        return None  # backtrack desde este nodo


if __name__ == "__main__":
    client = anthropic.Anthropic()

    def es_solucion(estado: str, objetivo: str) -> bool:
        """Solución: el estado contiene las palabras 'haiku' y 5-7-5 sílabas."""
        e = estado.lower()
        return "5-7-5" in e or ("haiku" in e and "sílabas" in e and len(estado.splitlines()) > 5)

    agent = ToTAgent(
        client=client,
        es_solucion=es_solucion,
        branching_factor=2,
        profundidad_max=2,
        beam_width=2,
    )

    objetivo = "Escribe un haiku sobre el otoño con 5-7-5 sílabas"
    estado_inicial = f"Objetivo: {objetivo}\nEstado: ningún pensamiento todavía."

    print("=== BFS ===")
    resultado = agent.tot_bfs(estado_inicial, objetivo)
    if resultado:
        print(f"Mejor estado:\n{resultado[-300:]}")
    else:
        print("No se encontró solución con BFS")

    print("\n=== DFS ===")
    resultado_dfs = agent.tot_dfs(estado_inicial, objetivo)
    if resultado_dfs:
        print(f"Estado DFS:\n{resultado_dfs[-300:]}")
    else:
        print("No se encontró solución con DFS")
