"""Patrón Supervisor/Worker: despacho paralelo con workers especializados.

El supervisor genera un plan de subtareas independientes; cada worker
recibe una tarea con su propio contexto aislado y system prompt especializado.
Todos corren en paralelo — sin DAG, porque no hay dependencias cruzadas.

Requiere: pip install anthropic
"""
import asyncio
import json
import os
import re
from dataclasses import dataclass

import anthropic

MODEL_SUPERVISOR = os.environ.get("MODEL", "claude-sonnet-4-6")
MODEL_WORKER = os.environ.get("SMALL_MODEL", "claude-haiku-4-5-20251001")

# Cada tipo de worker tiene su propio rol
#
# Cómo ejecutar:
#   make py SCRIPT=python/09-planificacion/supervisor_worker.py
#
# Qué esperar:
#   Supervisor descompone la tarea y despacha subtareas a workers especializados.
#   Los workers corren en paralelo (asyncio) y el supervisor sintetiza los resultados.
#
# Variables de entorno:
#   MODEL          — modelo supervisor/sintetizador (default: claude-sonnet-4-6)
#   SMALL_MODEL    — modelo worker (default: claude-haiku-4-5-20251001)

WORKER_SYSTEM_PROMPTS = {
    "analista":     "Eres un analista técnico. Responde con datos concretos y estructura clara.",
    "investigador": "Eres un investigador especializado. Cita benchmarks y referencias cuando existan.",
    "arquitecto":   "Eres un arquitecto de software. Enfócate en decisiones de diseño y tradeoffs reales.",
    "critico":      "Eres un crítico técnico. Señala limitaciones, casos borde y riesgos concretos.",
}

DEFAULT_SYSTEM = "Eres un asistente técnico especializado. Responde de forma concisa y estructurada."

PROMPT_PLAN = """\
Descompón la siguiente tarea en subtareas independientes que puedan ejecutarse en paralelo.
Responde ÚNICAMENTE con un array JSON válido, sin texto adicional.
Cada elemento debe tener:
  "id": string único (W1, W2, ...),
  "descripcion": objetivo concreto para el worker (una oración + criterios de output),
  "tipo_worker": uno de ["analista", "investigador", "arquitecto", "critico"]

Tarea: {tarea}"""

PROMPT_SINTESIS = """\
Tarea original: {tarea}

Resultados de los workers:
{resultados}

Sintetiza una respuesta final integrando todos los resultados. Sé conciso y directo."""


@dataclass
class Subtarea:
    id: str
    descripcion: str
    tipo_worker: str


def parsear_plan(texto: str) -> list[Subtarea]:
    # Strip markdown code fences
    texto_limpio = re.sub(r"```(?:json)?\s*", "", texto).strip()
    m = re.search(r"\[.*\]", texto_limpio, re.DOTALL)
    if not m:
        raise ValueError(f"No se encontró array JSON:\n{texto[:300]}")
    try:
        datos = json.loads(m.group())
    except json.JSONDecodeError:
        # Try to repair: remove trailing commas before ] or }
        fixed = re.sub(r",\s*([}\]])", r"\1", m.group())
        datos = json.loads(fixed)
    return [
        Subtarea(
            id=str(item["id"]),
            descripcion=str(item["descripcion"]),
            tipo_worker=str(item.get("tipo_worker", "analista")),
        )
        for item in datos
    ]


async def ejecutar_worker(
    subtarea: Subtarea, client: anthropic.AsyncAnthropic
) -> tuple[str, str]:
    system = WORKER_SYSTEM_PROMPTS.get(subtarea.tipo_worker, DEFAULT_SYSTEM)
    resp = await client.messages.create(
        model=MODEL_WORKER,
        max_tokens=300,
        system=system,
        messages=[{"role": "user", "content": subtarea.descripcion}],
    )
    resultado = resp.content[0].text.strip() if resp.content else ""
    return subtarea.id, resultado


async def supervisor_worker(tarea: str, client: anthropic.AsyncAnthropic) -> str:
    # 1. Supervisor genera plan
    resp_plan = await client.messages.create(
        model=MODEL_SUPERVISOR,
        max_tokens=600,
        messages=[{"role": "user", "content": PROMPT_PLAN.format(tarea=tarea)}],
    )
    plan_texto = resp_plan.content[0].text if resp_plan.content else ""
    plan = parsear_plan(plan_texto)

    print(f"Plan ({len(plan)} workers):")
    for s in plan:
        print(f"  {s.id} [{s.tipo_worker}]: {s.descripcion[:65]}")

    # 2. Todos los workers en paralelo — contexto aislado por worker
    print("\nDispatcheando workers en paralelo...")
    pares = await asyncio.gather(*(ejecutar_worker(s, client) for s in plan))
    resultados = dict(pares)

    for wid, r in resultados.items():
        print(f"  {wid} ✓ {r[:60]}...")

    # 3. Supervisor consolida
    resultados_str = "\n".join(f"[{wid}] {r}" for wid, r in resultados.items())
    resp_final = await client.messages.create(
        model=MODEL_SUPERVISOR,
        max_tokens=500,
        messages=[{
            "role": "user",
            "content": PROMPT_SINTESIS.format(tarea=tarea, resultados=resultados_str),
        }],
    )
    return resp_final.content[0].text.strip() if resp_final.content else ""


if __name__ == "__main__":
    async def main():
        client = anthropic.AsyncAnthropic()
        tarea = (
            "Evalúa si Python o TypeScript es mejor para construir agentes IA en 2025: "
            "considera ecosistema de librerías, rendimiento async, tipado y facilidad de debugging."
        )
        print(f"Tarea: {tarea}\n")
        resultado = await supervisor_worker(tarea, client)
        print(f"\n=== Síntesis final ===\n{resultado}")

    asyncio.run(main())
