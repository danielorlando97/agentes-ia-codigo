"""Descomposición de tareas con DAG explícito y ejecución paralela.

El planificador LLM genera un JSON de subtareas con dependencias;
el executor las resuelve en oleadas paralelas respetando el DAG.

Requiere: pip install anthropic

Cómo ejecutar:
    make py SCRIPT=python/09-planificacion/descomposicion.py

Qué esperar:
    El planificador genera un DAG de subtareas con dependencias.
    El executor las resuelve en oleadas paralelas: primero las que no tienen deps,
    luego las que dependen de las completadas, etc.

Variables de entorno:
    MODEL          — modelo planificador (default: claude-sonnet-4-6)
    SMALL_MODEL    — modelo executor (default: claude-haiku-4-5-20251001)
"""
import asyncio
import json
import os
import re
from dataclasses import dataclass, field

import anthropic

MODEL_PLANNER = os.environ.get("PLANNER_MODEL", "claude-sonnet-4-6")
MODEL_EXECUTOR = os.environ.get("EXECUTOR_MODEL", "claude-haiku-4-5-20251001")

PROMPT_PLANIFICADOR = """\
Descompón la siguiente tarea en subtareas atómicas.
Responde ÚNICAMENTE con un array JSON válido, sin texto adicional.
Cada elemento debe tener:
  "id": string único (S1, S2, ...),
  "objetivo": string de una oración con el objetivo de la subtarea,
  "deps": array de IDs que deben completarse primero ([] si ninguna)

Regla: maximiza las subtareas con deps=[] (ejecutables en paralelo desde el inicio).
Tarea: {tarea}"""

PROMPT_EXECUTOR = """\
Contexto de subtareas ya completadas:
{contexto}

Ejecuta esta subtarea y devuelve el resultado como texto conciso (máx 150 palabras):
{objetivo}"""

PROMPT_SINTESIS = """\
Tarea original: {tarea}

Resultados de cada subtarea:
{resultados}

Genera la respuesta final integrando todos los resultados. Sé conciso."""


@dataclass
class Subtarea:
    id: str
    objetivo: str
    deps: list[str] = field(default_factory=list)


def parsear_plan(texto: str) -> list[Subtarea]:
    """Extrae subtareas del JSON generado por el planificador.

    Tolerante: busca el primer array JSON aunque haya texto alrededor.
    """
    # Buscar el array JSON aunque haya texto adicional antes/después
    m = re.search(r"\[.*\]", texto, re.DOTALL)
    if not m:
        raise ValueError(f"No se encontró array JSON en la respuesta:\n{texto[:300]}")

    datos = json.loads(m.group())
    subtareas = []
    for item in datos:
        subtareas.append(Subtarea(
            id=str(item["id"]),
            objetivo=str(item["objetivo"]),
            deps=[str(d) for d in item.get("deps", [])],
        ))
    return subtareas


def validar_plan(plan: list[Subtarea]) -> None:
    """Verifica que todos los IDs en deps existen en el plan."""
    ids = {s.id for s in plan}
    for s in plan:
        for dep in s.deps:
            if dep not in ids:
                raise ValueError(f"Subtarea {s.id} depende de '{dep}' que no existe en el plan")


async def ejecutar_subtarea(
    subtarea: Subtarea,
    resultados: dict[str, str],
    client: anthropic.AsyncAnthropic,
) -> tuple[str, str]:
    contexto = "\n".join(f"[{k}] {v}" for k, v in resultados.items()) or "(ninguno)"
    resp = await client.messages.create(
        model=MODEL_EXECUTOR,
        max_tokens=300,
        messages=[{
            "role": "user",
            "content": PROMPT_EXECUTOR.format(contexto=contexto, objetivo=subtarea.objetivo),
        }],
    )
    resultado = resp.content[0].text.strip() if resp.content else ""
    return subtarea.id, resultado


async def ejecutar_dag(
    plan: list[Subtarea],
    client: anthropic.AsyncAnthropic,
) -> dict[str, str]:
    """Ejecuta el DAG en oleadas: cada oleada contiene subtareas con deps satisfechas."""
    resultados: dict[str, str] = {}
    completadas: set[str] = set()
    pendientes = list(plan)

    while pendientes:
        # Subtareas cuyas dependencias están todas completadas
        ejecutables = [
            s for s in pendientes
            if all(d in completadas for d in s.deps)
        ]
        if not ejecutables:
            bloqueadas = [s.id for s in pendientes]
            raise RuntimeError(f"Plan bloqueado — subtareas sin ejecutables: {bloqueadas}")

        print(f"  [oleada] ejecutando en paralelo: {[s.id for s in ejecutables]}")

        # Ejecutar la oleada en paralelo
        nuevos = await asyncio.gather(
            *(ejecutar_subtarea(s, resultados, client) for s in ejecutables)
        )
        for sid, resultado in nuevos:
            resultados[sid] = resultado
            completadas.add(sid)
            print(f"    {sid} ✓ {resultado[:60]}...")

        pendientes = [s for s in pendientes if s.id not in completadas]

    return resultados


async def descomponer_y_ejecutar(tarea: str, client: anthropic.AsyncAnthropic) -> str:
    # 1. Planificar
    resp = await client.messages.create(
        model=MODEL_PLANNER,
        max_tokens=800,
        messages=[{"role": "user", "content": PROMPT_PLANIFICADOR.format(tarea=tarea)}],
    )
    plan_texto = resp.content[0].text if resp.content else ""
    plan = parsear_plan(plan_texto)
    validar_plan(plan)

    print(f"Plan generado ({len(plan)} subtareas):")
    for s in plan:
        deps_str = f" [deps: {s.deps}]" if s.deps else " [sin deps]"
        print(f"  {s.id}: {s.objetivo[:60]}{deps_str}")

    # 2. Ejecutar DAG
    print("\nEjecutando DAG:")
    resultados = await ejecutar_dag(plan, client)

    # 3. Sintetizar
    resultados_str = "\n".join(f"[{k}] {v}" for k, v in resultados.items())
    resp_final = await client.messages.create(
        model=MODEL_PLANNER,
        max_tokens=600,
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
            "Escribe un breve análisis comparativo de Python vs TypeScript para "
            "desarrollo de agentes IA: (1) ecosistema de librerías, "
            "(2) rendimiento async, (3) tipado y mantenibilidad."
        )
        print(f"Tarea: {tarea}\n")
        resultado = await descomponer_y_ejecutar(tarea, client)
        print(f"\n=== Resultado final ===\n{resultado}")

    asyncio.run(main())
