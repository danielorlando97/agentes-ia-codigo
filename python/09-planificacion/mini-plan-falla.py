"""Mini-proyecto: El plan que falla.

Ejecuta un agente Plan-and-Execute sobre una tarea real y provoca
fallos en pasos intermedios. Observa cómo el replanning adapta el plan
original cuando un paso no produce el resultado esperado.

Requiere: ANTHROPIC_API_KEY

Uso:
    python mini-plan-falla.py
    python mini-plan-falla.py --fallo paso2
    python mini-plan-falla.py --fallo ninguno

Cómo ejecutar:
    make py SCRIPT=python/09-planificacion/mini-plan-falla.py

Qué esperar:
    Agente Plan-and-Execute con fallos provocados en pasos intermedios.
    Muestra como el replanning adapta el plan original ante resultados inesperados.

Variables de entorno:
    MODEL — modelo planificador/evaluador (default: claude-sonnet-4-6)
"""

import argparse
import json
import os
import sys

try:
    import anthropic
except ImportError:
    print("Error: pip install anthropic")
    sys.exit(1)

MODEL = os.environ.get("MODEL", "claude-haiku-4-5-20251001")

TAREA = (
    "Analiza el repositorio de Python de un proyecto de ML: "
    "revisa la estructura de archivos, ejecuta los tests, "
    "verifica las dependencias y genera un reporte de calidad."
)

HERRAMIENTAS = [
    {"name": "list_files", "description": "Lista archivos en un directorio.",
     "input_schema": {"type": "object", "properties": {"path": {"type": "string"}}, "required": ["path"]}},
    {"name": "run_tests", "description": "Ejecuta los tests del proyecto.",
     "input_schema": {"type": "object", "properties": {"framework": {"type": "string"}}, "required": []}},
    {"name": "check_deps", "description": "Verifica dependencias del proyecto.",
     "input_schema": {"type": "object", "properties": {}, "required": []}},
    {"name": "write_report", "description": "Escribe un reporte.",
     "input_schema": {"type": "object", "properties": {"content": {"type": "string"}}, "required": ["content"]}},
]


def dispatcher_con_fallo(nombre: str, params: dict, paso: int, fallo_en: str) -> tuple[str, bool]:
    """Devuelve (resultado, hubo_fallo)."""
    if fallo_en == "paso2" and nombre == "run_tests" and paso == 1:
        return "ERROR: No se encontró pytest. El proyecto no tiene tests configurados.", True
    if fallo_en == "paso3" and nombre == "check_deps" and paso == 2:
        return "ERROR: requirements.txt no encontrado. No se pueden verificar dependencias.", True

    if nombre == "list_files":
        return "src/model.py, src/data.py, src/train.py, tests/test_model.py, requirements.txt", False
    if nombre == "run_tests":
        return "Passed: 12/15 tests. Failed: test_edge_cases (3 cases). Coverage: 67%", False
    if nombre == "check_deps":
        return "numpy==1.26.0 ✓, pandas==2.0.3 ✓, scikit-learn==1.3.0 ✓, torch==2.1.0 ⚠ (version mismatch)", False
    if nombre == "write_report":
        return "Reporte guardado en quality_report.md", False
    return f"Herramienta '{nombre}' no reconocida", False


def generar_plan(client, tarea: str) -> list[str]:
    system = (
        "Eres un planificador de tareas de análisis de software. "
        "Dado una tarea, genera un plan de 3-5 pasos numerados. "
        "Cada paso debe mencionar qué herramienta usar y qué resultado espera. "
        "Sé conciso. Termina con PLAN_LISTO."
    )
    resp = client.messages.create(
        model=MODEL, max_tokens=512,
        system=system,
        messages=[{"role": "user", "content": f"Tarea: {tarea}"}],
    )
    texto = resp.content[0].text
    pasos = [l.strip() for l in texto.split("\n") if l.strip() and l.strip()[0].isdigit()]
    return pasos[:5]


def replanificar(client, tarea: str, plan_original: list, paso_fallido: int, error: str) -> list[str]:
    system = (
        "Eres un planificador de tareas. Un paso del plan falló. "
        "Dado el plan original, el paso que falló y el error, genera un plan alternativo "
        "que continúe desde donde se puede sin el paso fallido. "
        "El plan alternativo debe ser realista dado el error reportado."
    )
    contexto = (
        f"Tarea original: {tarea}\n"
        f"Plan original:\n" + "\n".join(f"{i+1}. {p}" for i, p in enumerate(plan_original)) +
        f"\n\nPaso {paso_fallido+1} falló con error: {error}\n"
        f"Genera un plan alternativo que sortee este problema."
    )
    resp = client.messages.create(
        model=MODEL, max_tokens=512,
        system=system,
        messages=[{"role": "user", "content": contexto}],
    )
    texto = resp.content[0].text
    pasos = [l.strip() for l in texto.split("\n") if l.strip() and (l.strip()[0].isdigit() or l.strip().startswith("-"))]
    return pasos[:5]


def ejecutar_con_replanning(client, tarea: str, plan: list, fallo_en: str) -> None:
    print(f"\n  Plan inicial ({len(plan)} pasos):")
    for i, p in enumerate(plan):
        print(f"  {i+1}. {p[:70]}{'...' if len(p) > 70 else ''}")

    mensajes = [{"role": "user", "content": tarea}]
    paso = 0
    replanificado = False

    while paso < len(plan):
        print(f"\n  → Ejecutando paso {paso+1}...")

        resp = client.messages.create(
            model=MODEL, max_tokens=256,
            messages=mensajes + [{"role": "user", "content": f"Ejecuta: {plan[paso]}"}],
            tools=HERRAMIENTAS,
        )

        tool_blocks = [b for b in resp.content if b.type == "tool_use"]
        if not tool_blocks:
            print(f"     Respuesta: {resp.content[0].text[:80]}...")
            paso += 1
            continue

        resultados = []
        for tb in tool_blocks:
            resultado, hubo_fallo = dispatcher_con_fallo(tb.name, tb.input, paso, fallo_en)
            print(f"     {tb.name}() = {resultado[:80]}...")

            if hubo_fallo:
                print(f"\n  ✗ FALLO en paso {paso+1}: {resultado}")
                print(f"  → Iniciando replanning...")
                nuevo_plan = replanificar(client, tarea, plan, paso, resultado)
                if nuevo_plan:
                    print(f"\n  Plan alternativo ({len(nuevo_plan)} pasos):")
                    for i, p in enumerate(nuevo_plan):
                        print(f"  {i+1}. {p[:70]}{'...' if len(p) > 70 else ''}")
                    plan = nuevo_plan
                    replanificado = True
                    paso = 0  # Reiniciar con el nuevo plan
                    mensajes = [{"role": "user", "content": tarea}]
                    break

            resultados.append({"type": "tool_result", "tool_use_id": tb.id, "content": resultado})

        if not replanificado or paso > 0:
            mensajes.append({"role": "assistant", "content": resp.content})
            if resultados:
                mensajes.append({"role": "user", "content": resultados})
            paso += 1
            replanificado = False

    print(f"\n  ✓ Plan completado{'  (con replanning)' if replanificado else ''}")


def main():
    parser = argparse.ArgumentParser(description="Ejecuta un plan y observa el replanning ante fallos.")
    parser.add_argument("--fallo", choices=["paso2", "paso3", "ninguno"], default="paso2")
    args = parser.parse_args()

    api_key = os.environ.get("ANTHROPIC_API_KEY")
    if not api_key:
        print("Error: ANTHROPIC_API_KEY no configurada")
        sys.exit(1)

    client = anthropic.Anthropic(api_key=api_key)

    print(f"\n{'='*60}")
    print(f"  EL PLAN QUE FALLA")
    print(f"  Modelo: {MODEL}  |  Fallo en: {args.fallo}")
    print(f"{'='*60}")
    print(f"\n  Tarea: {TAREA}")

    print(f"\n  Generando plan...")
    plan = generar_plan(client, TAREA)
    ejecutar_con_replanning(client, TAREA, plan, args.fallo)

    print(f"\n{'='*60}")
    print("  El replanning ocurre cuando:")
    print("  • Un paso falla con error inesperado")
    print("  • El resultado no permite continuar con el siguiente paso")
    print("  • El plan estático asumía un estado del mundo que no existe")
    print(f"{'='*60}")


if __name__ == "__main__":
    main()
