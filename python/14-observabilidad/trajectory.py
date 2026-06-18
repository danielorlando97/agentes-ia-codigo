# Evaluación de trayectoria: precision, recall, step efficiency, LLM-as-judge
#
# Cómo ejecutar:
#   make py SCRIPT=python/14-observabilidad/trajectory.py
#
# Qué esperar:
#   Evaluacion de la trayectoria del agente: precision de pasos, recall,
#   step efficiency, y LLM-as-judge para calidad holistica.
#
# Variables de entorno:
#   MODEL — modelo evaluado y juez (default: claude-sonnet-4-6)

from dataclasses import dataclass, field
from typing import Any, Optional
import os
import anthropic

MODEL = os.environ.get("MODEL", "claude-haiku-4-5-20251001")
cliente = anthropic.Anthropic()


# ─── Tipos ───────────────────────────────────────────────────────────────────

@dataclass
class Paso:
    herramienta: str
    params: dict
    resultado: Any = ""

    def __str__(self) -> str:
        params_str = str(sorted(self.params.items()))
        return f"{self.herramienta}({params_str})"

    def __eq__(self, other: object) -> bool:
        if not isinstance(other, Paso):
            return False
        return str(self) == str(other)

    def __hash__(self) -> int:
        return hash(str(self))


@dataclass
class ResultadoTrayectoria:
    trajectory_precision: float
    trajectory_recall: float
    trajectory_exact_match: bool
    step_efficiency: float
    n_pasos_agente: int
    n_pasos_gt: int
    primer_error_herramienta: Optional[dict]


# ─── Métricas de trayectoria ─────────────────────────────────────────────────

def evaluar_trayectoria(
    trayectoria_agente: list[Paso],
    ground_truth: list[Paso],
) -> ResultadoTrayectoria:
    pasos_gt = set(str(p) for p in ground_truth)
    pasos_agente_str = [str(p) for p in trayectoria_agente]

    tp = sum(1 for p in pasos_agente_str if p in pasos_gt)
    precision = tp / len(trayectoria_agente) if trayectoria_agente else 0.0
    recall = tp / len(ground_truth) if ground_truth else 0.0
    step_efficiency = len(ground_truth) / len(trayectoria_agente) if trayectoria_agente else 0.0
    exact_match = pasos_agente_str == [str(p) for p in ground_truth]

    primer_error = None
    for i, (pa, pgt) in enumerate(zip(trayectoria_agente, ground_truth)):
        if pa.herramienta != pgt.herramienta:
            primer_error = {
                "step": i,
                "agente": pa.herramienta,
                "gt": pgt.herramienta,
            }
            break

    return ResultadoTrayectoria(
        trajectory_precision=round(precision, 3),
        trajectory_recall=round(recall, 3),
        trajectory_exact_match=exact_match,
        step_efficiency=round(step_efficiency, 3),
        n_pasos_agente=len(trayectoria_agente),
        n_pasos_gt=len(ground_truth),
        primer_error_herramienta=primer_error,
    )


# ─── Evaluación sin ground truth (LLM-as-judge) ─────────────────────────────

def evaluar_trayectoria_con_juez(
    trayectoria: list[Paso],
    objetivo_tarea: str,
) -> dict:
    tray_str = "\n".join(
        f"Step {i+1}: {p.herramienta}({p.params}) → {str(p.resultado)[:100]}"
        for i, p in enumerate(trayectoria)
    )

    prompt = f"""Evalúa si la siguiente secuencia de pasos es eficiente y correcta para el objetivo dado.

OBJETIVO: {objetivo_tarea}

PASOS EJECUTADOS:
{tray_str}

Responde en JSON con este formato exacto:
{{"es_correcta": <true/false>, "es_eficiente": <true/false>, "pasos_innecesarios": [<indices base-1>], "pasos_faltantes": [<descripción>], "puntuacion": <1-10>, "razon": "<explicación breve>"}}"""

    resp = cliente.messages.create(
        model=MODEL,
        max_tokens=512,
        messages=[{"role": "user", "content": prompt}],
    )
    texto = next((b.text for b in resp.content if hasattr(b, "text")), "{}")

    import json
    try:
        return json.loads(texto)
    except json.JSONDecodeError:
        return {"error": "parse fallido", "raw": texto[:200]}


# ─── Ground truth de ejemplo ─────────────────────────────────────────────────

GROUND_TRUTH: dict[str, list[Paso]] = {
    "precio_cobre": [
        Paso("search_web", {"query": "precio cobre USD libra hoy"}),
        Paso("parse_number", {"texto": "$resultado_anterior"}),
    ],
    "crear_issue": [
        Paso("get_repo_info", {"repo": "mi-proyecto"}),
        Paso("create_issue", {"title": "Bug encontrado", "body": "Descripción del bug", "labels": ["bug"]}),
    ],
}


if __name__ == "__main__":
    print("=== Evaluación de trayectoria ===\n")

    # Escenario 1: trayectoria correcta (idéntica al GT)
    gt = GROUND_TRUTH["precio_cobre"]
    tray_correcta = [
        Paso("search_web", {"query": "precio cobre USD libra hoy"}, resultado="$4.23/lb"),
        Paso("parse_number", {"texto": "$4.23/lb"}, resultado=4.23),
    ]
    res = evaluar_trayectoria(tray_correcta, gt)
    print("Trayectoria correcta:")
    print(f"  Precision: {res.trajectory_precision} | Recall: {res.trajectory_recall}")
    print(f"  Exact match: {res.trajectory_exact_match} | Efficiency: {res.step_efficiency}")

    # Escenario 2: trayectoria con pasos extras
    tray_ineficiente = [
        Paso("search_web", {"query": "precio cobre"}),
        Paso("search_web", {"query": "precio cobre USD"}),   # duplicado innecesario
        Paso("search_web", {"query": "precio cobre USD libra hoy"}, resultado="$4.23/lb"),
        Paso("parse_number", {"texto": "$4.23/lb"}, resultado=4.23),
    ]
    res2 = evaluar_trayectoria(tray_ineficiente, gt)
    print("\nTrayectoria ineficiente (3 búsquedas en lugar de 1):")
    print(f"  Precision: {res2.trajectory_precision} | Recall: {res2.trajectory_recall}")
    print(f"  Exact match: {res2.trajectory_exact_match} | Efficiency: {res2.step_efficiency}")
    print(f"  → Un agente así cuesta {1/res2.step_efficiency:.1f}× más que el óptimo")

    # Escenario 3: evaluación sin ground truth
    print("\nEvaluación con LLM-as-judge (sin ground truth):")
    veredicto = evaluar_trayectoria_con_juez(
        tray_ineficiente,
        "Obtener el precio actual del cobre en USD/libra",
    )
    print(f"  Es correcta: {veredicto.get('es_correcta')}")
    print(f"  Es eficiente: {veredicto.get('es_eficiente')}")
    print(f"  Puntuación: {veredicto.get('puntuacion')}/10")
    print(f"  Razón: {veredicto.get('razon', '')[:200]}")
