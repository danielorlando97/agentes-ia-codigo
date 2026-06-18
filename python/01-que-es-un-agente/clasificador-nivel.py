"""
Clasificador de nivel — ubica un sistema en el espectro de autonomia.

Qué demuestra:
    Herramienta de diagnostico: dado un sistema existente, determina en que
    nivel del espectro de autonomia se encuentra segun sus caracteristicas.
    No usa LLM — el razonamiento es puramente estructural (flags → etiqueta).

Espectro implementado:
    ☆☆☆ Procesador    — sin loop, sin tools, sin decision de ruta
    ★☆☆ Router        — LLM elige entre N rutas predefinidas
    ★★☆ Tool caller   — loop acotado, tools fijas
    ★★☆ Multi-step    — loop no acotado, el LLM decide que tools usar
    ★★★ Multi-agent   — sub-agentes con loops propios coordinados
    ★★★ Code agent    — el LLM genera codigo arbitrario para ejecutar

Cómo ejecutar:
    make py SCRIPT=python/01-que-es-un-agente/clasificador-nivel.py

Qué esperar:
    6 casos de ejemplo, cada uno con su nivel asignado.
    No hace llamadas a API — es completamente offline.
"""
from dataclasses import dataclass


@dataclass
class Features:
    multi_agente: bool = False
    code_agent: bool = False
    loop_no_acotado_y_decide_tools: bool = False
    loop_acotado_con_tools: bool = False
    llm_elige_ruta_sin_loop: bool = False


def classify(f: Features) -> str:
    if f.multi_agente:
        return "★★★ multi-agente"
    if f.code_agent:
        return "★★★ code agent"
    if f.loop_no_acotado_y_decide_tools:
        return "★★☆ multi-step"
    if f.loop_acotado_con_tools:
        return "★★☆ tool caller"
    if f.llm_elige_ruta_sin_loop:
        return "★☆☆ router"
    return "☆☆☆ procesador"


CASES = [
    ("traduccion sin tools", Features()),
    ("router por intent", Features(llm_elige_ruta_sin_loop=True)),
    ("RAG simple (1 retrieve + 1 generate)", Features(loop_acotado_con_tools=True)),
    ("ReAct hasta end_turn", Features(loop_no_acotado_y_decide_tools=True)),
    ("supervisor + workers", Features(multi_agente=True)),
    ("agente que escribe codigo Python", Features(code_agent=True)),
]


if __name__ == "__main__":
    for name, f in CASES:
        print(f"{classify(f):<25}  {name}")
