"""LLM Compiler (Kim et al. 2023, arXiv:2312.04511) — ejecución paralela de DAG.

Planner LLM genera un plan con $idx como dependencias implícitas.
Task Fetching Unit schedula con asyncio.Event — cada tarea espera
solo sus deps antes de ejecutar. Joiner decide Finish o Replan.

Requiere: pip install anthropic

Cómo ejecutar:
    make py SCRIPT=python/09-planificacion/llm_compiler.py

Qué esperar:
    Planner genera plan con referencias $idx. Task Fetching Unit ejecuta en paralelo
    con asyncio.Event esperando solo sus dependencias. Joiner sintetiza o replantea.

Variables de entorno:
    MODEL          — modelo planner/joiner (default: claude-sonnet-4-6)
    SMALL_MODEL    — modelo worker (default: claude-haiku-4-5-20251001)
"""
import os
import asyncio
import re
from dataclasses import dataclass, field

import anthropic

MODEL = os.environ.get("MODEL", "claude-sonnet-4-6")
MAX_REPLANS = 3

PLANNER_SYSTEM = """\
Eres un planificador. Descompón el problema en tool calls que maximicen el paralelismo.

Formato estricto (una tarea por línea):
  <idx>. <tool>(<args>)

Reglas:
- Índices desde 1, estrictamente crecientes
- Para usar el output de la tarea N como argumento: $N
- Tareas sin $N en sus args se ejecutan en paralelo de inmediato
- Última línea siempre: join()

Herramientas disponibles:
{tool_docs}"""

JOINER_PROMPT = """\
Historial de ejecución del plan:
{tao}

Decide:
- Si la información es suficiente: Finish(<respuesta completa>)
- Si falta información: Replan(<qué faltó>)

Responde SOLO con una de las dos opciones anteriores."""


@dataclass
class Tarea:
    idx: int
    tool: str
    args: str
    deps: set[int] = field(default_factory=set)


def parsear_plan(texto: str) -> list[Tarea]:
    patron = re.compile(r"^(\d+)\.\s+(\w+)\(([^)]*)\)", re.MULTILINE)
    tareas = []
    for m in patron.finditer(texto):
        idx, tool, args_str = int(m.group(1)), m.group(2), m.group(3)
        if tool == "join":
            continue
        deps = {int(d) for d in re.findall(r"\$(\d+)", args_str)}
        tareas.append(Tarea(idx=idx, tool=tool, args=args_str, deps=deps))
    return tareas


def validar_plan(tareas: list[Tarea]) -> None:
    """Elimina deps inválidas en lugar de lanzar error — los modelos locales
    a veces generan auto-referencias o deps fuera de rango."""
    ids = {t.idx for t in tareas}
    for t in tareas:
        validas = [d for d in t.deps if d < t.idx and d in ids]
        if len(validas) != len(t.deps):
            t.deps = validas


def sustituir_placeholders(args: str, resultados: dict[int, str]) -> str:
    def repl(m):
        idx = int(m.group(1))
        return resultados.get(idx, m.group(0))
    return re.sub(r"\$(\d+)", repl, args)


async def ejecutar_dag(
    tareas: list[Tarea],
    tools: dict[str, callable],
) -> dict[int, str]:
    # asyncio.Event por tarea — broadcast: múltiples deps pueden esperar el mismo evento
    eventos: dict[int, asyncio.Event] = {t.idx: asyncio.Event() for t in tareas}
    resultados: dict[int, str] = {}

    async def ejecutar(tarea: Tarea) -> None:
        if tarea.deps:
            await asyncio.gather(*[eventos[d].wait() for d in tarea.deps])

        args = sustituir_placeholders(tarea.args, resultados)
        fn = tools.get(tarea.tool)
        resultado = fn(args) if fn else f"[tool '{tarea.tool}' no registrada]"
        if asyncio.iscoroutine(resultado):
            resultado = await resultado

        resultados[tarea.idx] = str(resultado)
        eventos[tarea.idx].set()
        print(f"  T{tarea.idx} {tarea.tool}({args[:40]}) → {str(resultado)[:50]}")

    await asyncio.gather(*[ejecutar(t) for t in tareas])
    return resultados


def parsear_joiner(texto: str) -> tuple[str, str]:
    """Extrae (accion, contenido) del joiner. accion = 'Finish' | 'Replan'."""
    m = re.search(r"(Finish|Replan)\((.+?)\)$", texto, re.DOTALL | re.IGNORECASE)
    if m:
        return m.group(1).capitalize(), m.group(2).strip()
    return "Finish", texto.strip()


async def llm_compiler(
    tarea: str,
    tools: dict[str, callable],
    tool_docs: str,
    client: anthropic.AsyncAnthropic,
) -> str:
    context: list[dict] = []

    for ronda in range(MAX_REPLANS):
        # 1. PLANNER
        msgs = context + [{"role": "user", "content": tarea}]
        resp_planner = await client.messages.create(
            model=MODEL,
            max_tokens=600,
            system=PLANNER_SYSTEM.format(tool_docs=tool_docs),
            messages=msgs,
        )
        plan_texto = resp_planner.content[0].text if resp_planner.content else ""

        print(f"\n[ronda {ronda + 1}] Plan generado:")
        print(plan_texto.strip()[:300])

        tareas = parsear_plan(plan_texto)
        validar_plan(tareas)

        # 2. TASK FETCHING UNIT — ejecución paralela con Events
        print("\nEjecutando DAG:")
        resultados = await ejecutar_dag(tareas, tools)

        # 3. JOINER
        tao = plan_texto + "\n\nResultados:\n" + "\n".join(
            f"T{idx}: {res}" for idx, res in sorted(resultados.items())
        )
        resp_joiner = await client.messages.create(
            model=MODEL,
            max_tokens=300,
            messages=[{"role": "user", "content": JOINER_PROMPT.format(tao=tao)}],
        )
        joiner_texto = resp_joiner.content[0].text.strip() if resp_joiner.content else ""
        accion, contenido = parsear_joiner(joiner_texto)

        print(f"\nJoiner: {accion} → {contenido[:80]}")

        if accion == "Finish":
            return contenido

        # Replan: acumular contexto T-A-O
        context += [
            {"role": "assistant", "content": plan_texto},
            {"role": "user", "content": tao},
        ]

    return contenido  # best effort tras max_replans


if __name__ == "__main__":
    async def main():
        client = anthropic.AsyncAnthropic()

        def calcular(expresion: str) -> str:
            try:
                # Limpiar $N residuales y evaluar
                expr = re.sub(r"\$\d+", "0", expresion).strip()
                # eslint-disable-next-line no-new-func
                return str(eval(expr, {"__builtins__": {}}, {}))  # noqa: S307
            except Exception as e:
                return f"Error: {e}"

        tools = {"calcular": calcular}
        tool_docs = "calcular(expresion): evalúa una expresión matemática y devuelve el resultado numérico."

        tarea = (
            "Calcula el área de un rectángulo de 15×8 metros y el área de un "
            "círculo de radio 5 metros (π≈3.14159). ¿Cuál es mayor y por cuánto?"
        )
        print(f"Tarea: {tarea}")
        resultado = await llm_compiler(tarea, tools, tool_docs, client)
        print(f"\n=== Respuesta final ===\n{resultado}")

    asyncio.run(main())
