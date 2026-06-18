# Golden sets: runner de evaluación con criterios múltiples y pesos
#
# Cómo ejecutar:
#   make py SCRIPT=python/14-observabilidad/golden_sets.py
#
# Qué esperar:
#   Runner que evalua un agente sobre un conjunto de golden queries.
#   Muestra precision, recall y puntuacion ponderada por criterio.
#
# Variables de entorno:
#   MODEL — modelo evaluado y juez (default: claude-sonnet-4-6)

import os
import re
from dataclasses import dataclass, field
from typing import Any, Callable, Optional
import anthropic

MODEL = os.environ.get("MODEL", "claude-haiku-4-5-20251001")
cliente = anthropic.Anthropic()


# ─── Caso de evaluación ──────────────────────────────────────────────────────

@dataclass
class CasoEval:
    id: str
    input: str
    expected: Optional[str]
    tipo: str                    # "fact_lookup" | "formatting" | "safety" | "functional"
    criterio: str                # "exact" | "contains" | "regex" | "no_tool" | "fn"
    peso: float = 1.0
    test_fn: Optional[Callable[[str], bool]] = None


@dataclass
class ResultadoEval:
    caso: CasoEval
    output: str
    paso: bool
    detalle: str = ""


# ─── Criterios de comparación ────────────────────────────────────────────────

def evaluar_criterio(output: str, caso: CasoEval) -> tuple[bool, str]:
    if caso.criterio == "exact":
        paso = output.strip() == (caso.expected or "").strip()
        return paso, f"exact: '{output.strip()[:60]}' vs '{(caso.expected or '')[:60]}'"

    if caso.criterio == "contains":
        paso = (caso.expected or "").lower() in output.lower()
        return paso, f"contains '{caso.expected}': {'sí' if paso else 'no'}"

    if caso.criterio == "regex":
        paso = bool(re.search(caso.expected or "", output, re.IGNORECASE))
        return paso, f"regex '{caso.expected}': {'match' if paso else 'no match'}"

    if caso.criterio == "no_tool":
        # El agente no debe haber ejecutado ninguna herramienta (output no contiene marcas de tool)
        paso = "[TOOL:" not in output and "tool_use" not in output
        return paso, "no_tool: " + ("ok" if paso else "herramienta ejecutada")

    if caso.criterio == "fn" and caso.test_fn:
        try:
            paso = caso.test_fn(output)
            return paso, "fn: " + ("ok" if paso else "fallo")
        except Exception as e:
            return False, f"fn excepción: {e}"

    return False, f"criterio '{caso.criterio}' no reconocido"


# ─── Agente mínimo para evaluación ───────────────────────────────────────────

def ejecutar_agente_simple(prompt: str) -> str:
    resp = cliente.messages.create(
        model=MODEL,
        max_tokens=256,
        messages=[{"role": "user", "content": prompt}],
    )
    return next((b.text for b in resp.content if hasattr(b, "text")), "")


# ─── Runner del golden set ───────────────────────────────────────────────────

def evaluar_golden_set(
    agente_fn: Callable[[str], str],
    golden_set: list[CasoEval],
) -> dict[str, Any]:
    resultados: list[ResultadoEval] = []

    for caso in golden_set:
        output = agente_fn(caso.input)
        paso, detalle = evaluar_criterio(output, caso)
        resultados.append(ResultadoEval(caso=caso, output=output, paso=paso, detalle=detalle))
        estado = "✓" if paso else "✗"
        print(f"  [{estado}] [{caso.tipo}] {caso.id}: {detalle}")

    peso_total = sum(r.caso.peso for r in resultados)
    peso_pasado = sum(r.caso.peso for r in resultados if r.paso)
    pass_rate = peso_pasado / peso_total if peso_total > 0 else 0.0

    fallos = [r for r in resultados if not r.paso]

    por_tipo: dict[str, dict] = {}
    for r in resultados:
        t = r.caso.tipo
        if t not in por_tipo:
            por_tipo[t] = {"total": 0, "pasados": 0}
        por_tipo[t]["total"] += 1
        if r.paso:
            por_tipo[t]["pasados"] += 1

    return {
        "pass_rate": pass_rate,
        "pass_rate_ponderado": pass_rate,
        "total_casos": len(resultados),
        "casos_fallidos": len(fallos),
        "fallos": [(r.caso.id, r.detalle) for r in fallos],
        "por_tipo": por_tipo,
    }


# ─── Golden set de ejemplo ───────────────────────────────────────────────────

GOLDEN_SET: list[CasoEval] = [
    CasoEval(
        id="gs-001",
        input="¿Cuántos días tiene una semana?",
        expected="7",
        tipo="fact_lookup",
        criterio="contains",
        peso=1.0,
    ),
    CasoEval(
        id="gs-002",
        input="Lista 3 frutas separadas por coma.",
        expected=r"\w+,\s*\w+,\s*\w+",
        tipo="formatting",
        criterio="regex",
        peso=1.0,
    ),
    CasoEval(
        id="gs-003",
        input="¿Cuántos días tiene el año?",
        expected="365",
        tipo="fact_lookup",
        criterio="contains",
        peso=1.5,
    ),
    CasoEval(
        id="gs-004",
        input="Responde solo con el número: 2 + 2",
        expected="4",
        tipo="fact_lookup",
        criterio="exact",
        peso=1.0,
    ),
]


if __name__ == "__main__":
    print("=== Golden set runner ===\n")
    resultado = evaluar_golden_set(ejecutar_agente_simple, GOLDEN_SET)

    print(f"\nPass rate: {resultado['pass_rate']:.1%}")
    print(f"Casos: {resultado['total_casos']} total, {resultado['casos_fallidos']} fallidos")
    print(f"Por tipo: {resultado['por_tipo']}")

    UMBRAL_DEPLOY = 0.85
    if resultado["pass_rate"] < UMBRAL_DEPLOY:
        print(f"\n[BLOQUEADO] Pass rate {resultado['pass_rate']:.1%} < {UMBRAL_DEPLOY:.0%}")
    else:
        print(f"\n[OK] Deploy autorizado — pass rate {resultado['pass_rate']:.1%}")
