"""Replanificación dinámica: detecta divergencia tras cada paso y replantea los restantes.

El evaluador LLM juzga si cada resultado permite continuar al paso siguiente.
Si no, el replanificador regenera los pasos pendientes — sin repetir los ya completados.
max_replans=3 previene el loop infinito documentado en AutoGPT.

Requiere: pip install anthropic

Cómo ejecutar:
    make py SCRIPT=python/09-planificacion/replanificacion.py

Qué esperar:
    Agente con plan de pasos donde uno falla. El evaluador detecta la divergencia
    y el replanificador genera pasos alternativos para los restantes.
    max_replans=3 previene el loop infinito.

Variables de entorno:
    MODEL          — modelo planificador/evaluador (default: claude-sonnet-4-6)
    SMALL_MODEL    — modelo executor (default: claude-haiku-4-5-20251001)
"""
import os
import re
from dataclasses import dataclass, field

import anthropic

MODEL = os.environ.get("MODEL", "claude-sonnet-4-6")
MAX_REPLANS = 3

PROMPT_PLAN = """\
Genera una lista numerada de pasos atómicos para completar esta tarea.
Cada paso debe comenzar con un verbo y ser ejecutable de forma independiente.
Tarea: {tarea}
Responde solo con la lista numerada."""

PROMPT_EXECUTOR = """\
Contexto de pasos ya completados:
{contexto}

Ejecuta este paso y devuelve el resultado como texto conciso (máx 100 palabras):
{paso}"""

PROMPT_EVALUADOR = """\
Paso ejecutado: {paso}
Resultado obtenido: {resultado}
Próximo paso del plan: {proximo}

¿El resultado permite ejecutar el próximo paso?
Responde SOLO con una de estas palabras: SATISFACE | NO_SATISFACE
Si NO_SATISFACE, añade en la misma línea: | <razón breve>"""

PROMPT_REPLAN = """\
Tarea original: {tarea}

Pasos ya completados exitosamente:
{completados}

Paso que falló: {paso_fallido}
Resultado fallido: {resultado_fallido}
Razón del fallo: {razon}

Genera una lista numerada con los pasos RESTANTES para completar la tarea.
No repitas los pasos ya completados. Responde solo con la lista numerada."""

PROMPT_SINTESIS = """\
Tarea original: {tarea}

Historial de ejecución:
{historial}

Genera la respuesta final integrando todos los resultados."""


@dataclass
class EntradaHistorial:
    paso: str
    resultado: str
    estado: str = "OK"


def parsear_lista(texto: str) -> list[str]:
    pasos = re.findall(r"^\d+[.)]\s+(.+)$", texto, re.MULTILINE)
    return [p.strip() for p in pasos if p.strip()]


def llm(client: anthropic.Anthropic, prompt: str, max_tokens: int = 400) -> str:
    resp = client.messages.create(
        model=MODEL,
        max_tokens=max_tokens,
        messages=[{"role": "user", "content": prompt}],
    )
    return resp.content[0].text.strip() if resp.content else ""


def ejecutar_paso(
    client: anthropic.Anthropic, paso: str, historial: list[EntradaHistorial]
) -> str:
    contexto = "\n".join(f"[{e.paso[:40]}] → {e.resultado[:80]}" for e in historial) or "(sin pasos previos)"
    return llm(client, PROMPT_EXECUTOR.format(contexto=contexto, paso=paso), max_tokens=200)


def evaluar_divergencia(
    client: anthropic.Anthropic, paso: str, resultado: str, proximo: str
) -> tuple[bool, str]:
    """Retorna (satisface, razon). Si no hay próximo paso, siempre satisface."""
    if not proximo:
        return True, ""
    resp = llm(client, PROMPT_EVALUADOR.format(paso=paso, resultado=resultado, proximo=proximo), max_tokens=60)
    satisface = resp.upper().startswith("SATISFACE")
    razon = resp.split("|")[1].strip() if "|" in resp else ""
    return satisface, razon


def replanificar(
    client: anthropic.Anthropic,
    tarea: str,
    historial: list[EntradaHistorial],
    paso_fallido: str,
    resultado_fallido: str,
    razon: str,
) -> list[str]:
    completados = "\n".join(f"- {e.paso}" for e in historial) or "(ninguno)"
    resp = llm(
        client,
        PROMPT_REPLAN.format(
            tarea=tarea,
            completados=completados,
            paso_fallido=paso_fallido,
            resultado_fallido=resultado_fallido,
            razon=razon,
        ),
        max_tokens=400,
    )
    return parsear_lista(resp)


def plan_execute_dynamic(tarea: str, client: anthropic.Anthropic) -> str:
    # 1. Generar plan inicial
    plan = parsear_lista(llm(client, PROMPT_PLAN.format(tarea=tarea)))
    print(f"Plan inicial ({len(plan)} pasos):")
    for i, p in enumerate(plan, 1):
        print(f"  {i}. {p[:70]}")

    historial: list[EntradaHistorial] = []
    replans = 0
    i = 0

    while i < len(plan):
        paso = plan[i]
        proximo = plan[i + 1] if i + 1 < len(plan) else ""

        resultado = ejecutar_paso(client, paso, historial)
        satisface, razon = evaluar_divergencia(client, paso, resultado, proximo)

        print(f"\n[paso {i+1}/{len(plan)}] {'✓' if satisface else '✗'} {paso[:60]}")

        if satisface:
            historial.append(EntradaHistorial(paso, resultado))
            i += 1

        elif replans < MAX_REPLANS:
            print(f"  → Divergencia: {razon or '(sin razón explícita)'}")
            nuevos_pasos = replanificar(client, tarea, historial, paso, resultado, razon)
            plan = [e.paso for e in historial] + nuevos_pasos
            replans += 1
            print(f"  → Replan #{replans}: {len(nuevos_pasos)} pasos nuevos desde paso {i+1}")
            # No incrementar i — re-ejecutar desde el mismo punto con plan nuevo

        else:
            # Sin replans disponibles: registrar como parcial y continuar
            historial.append(EntradaHistorial(paso, resultado, estado="PARCIAL"))
            i += 1

    # 2. Síntesis final
    historial_str = "\n".join(f"[{e.estado}] {e.paso}: {e.resultado[:100]}" for e in historial)
    return llm(client, PROMPT_SINTESIS.format(tarea=tarea, historial=historial_str), max_tokens=500)


if __name__ == "__main__":
    client = anthropic.Anthropic()
    tarea = (
        "Calcula cuántos días hay entre el 1 de enero y el 1 de julio de 2025, "
        "luego calcula cuántas semanas y cuántos meses aproximados representa."
    )
    print(f"Tarea: {tarea}\n")
    resultado = plan_execute_dynamic(tarea, client)
    print(f"\n=== Resultado final ===\n{resultado}")
