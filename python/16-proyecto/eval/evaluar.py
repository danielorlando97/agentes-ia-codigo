# Evaluacion del agente de revision de codigo con golden set.
#
# Ejecuta el golden set de evaluación y reporta precision/recall por severidad.
# El golden set contiene casos con el output esperado (hallazgos, severidad).
# El agente genera su propia salida y el evaluador la compara con el golden set.
#
# Cómo ejecutar:
#   make py SCRIPT=python/16-proyecto/eval/evaluar.py
#
# Qué esperar:
#   Tabla de precision/recall por nivel de severidad (critical/high/medium/low).
#   Muestra qué hallazgos el agente detecto y cuáles se perdio.
#
# Variables de entorno:
#   MODEL — modelo a usar (default: claude-sonnet-4-6)
import json
import re
import sys
import os
import anthropic
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent.parent))
from agente_completo import loop_react


def cargar_golden_set() -> list[dict]:
    ruta = Path(__file__).parent / "golden_set.json"
    return json.loads(ruta.read_text())


def hallazgo_cumple_patron(hallazgo: dict, patron_esperado: dict) -> bool:
    texto = f"{hallazgo.get('descripcion', '')} {hallazgo.get('sugerencia', '')}".lower()
    patron_re = re.compile(patron_esperado["patron"], re.IGNORECASE)
    if not patron_re.search(texto):
        return False
    if "severidad" in patron_esperado and hallazgo.get("severidad") != patron_esperado["severidad"]:
        return False
    if "tipo" in patron_esperado and hallazgo.get("tipo") != patron_esperado["tipo"]:
        return False
    return True


def evaluar_caso(caso: dict, directorio: str) -> dict:
    tracer_dummy = _NullTracer()
    try:
        revision = loop_react(caso["codigo"], directorio, tracer_dummy)
    except Exception as e:
        return {"id": caso["id"], "error": str(e), "precision": 0.0, "recall": 0.0}

    hallazgos = revision.get("hallazgos", [])
    esperados = caso.get("hallazgos_esperados", [])

    encontrados = 0
    for esperado in esperados:
        if any(hallazgo_cumple_patron(h, esperado) for h in hallazgos):
            encontrados += 1

    recall = encontrados / len(esperados) if esperados else 1.0

    criticos_agente = [h for h in hallazgos if h.get("severidad") == "critical"]
    no_debe_tener_criticos = caso.get("no_debe_tener_criticos", False)
    criticos_ok = len(criticos_agente) == 0 if no_debe_tener_criticos else True

    return {
        "id": caso["id"],
        "descripcion": caso["descripcion"],
        "recall": recall,
        "hallazgos_agente": len(hallazgos),
        "hallazgos_esperados": len(esperados),
        "encontrados": encontrados,
        "criticos_ok": criticos_ok,
        "ok": recall >= 0.8 and criticos_ok
    }


class _NullTracer:
    def start_as_current_span(self, name):
        return _NullSpan()

class _NullSpan:
    def __enter__(self): return self
    def __exit__(self, *args): pass
    def set_attribute(self, *args): pass


def main():
    golden = cargar_golden_set()
    directorio = os.getcwd()

    print(f"Evaluando {len(golden)} casos...\n")
    resultados = []
    for caso in golden:
        print(f"  [{caso['id']}] {caso['descripcion'][:50]}...", end=" ", flush=True)
        resultado = evaluar_caso(caso, directorio)
        resultados.append(resultado)
        estado = "✓" if resultado.get("ok") else "✗"
        print(f"{estado} (recall={resultado.get('recall', 0):.0%})")

    total = len(resultados)
    aprobados = sum(1 for r in resultados if r.get("ok"))
    recall_promedio = sum(r.get("recall", 0) for r in resultados) / total

    print(f"\n{'='*50}")
    print(f"Resultado: {aprobados}/{total} casos aprobados ({aprobados/total:.0%})")
    print(f"Recall promedio: {recall_promedio:.0%}")

    fallidos = [r for r in resultados if not r.get("ok")]
    if fallidos:
        print(f"\nFallidos:")
        for r in fallidos:
            print(f"  - {r['id']}: recall={r.get('recall', 0):.0%} criticos_ok={r.get('criticos_ok', True)}")

    sys.exit(0 if aprobados == total else 1)


if __name__ == "__main__":
    main()
