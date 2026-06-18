# Fase 4: añade checkpoint HITL para hallazgos críticos.
# En producción: checkpoint → webhook/Slack; aquí: CLI interactiva.
#
# Cómo ejecutar:
#   make py SCRIPT=python/16-proyecto/fase4_hitl.py
#
# Qué esperar:
#   El agente revisa codigo y pide aprobacion humana para hallazgos criticos.
#   El humano puede aprobar, rechazar o modificar cada accion critica via CLI.
#
# Variables de entorno:
#   MODEL — modelo a usar (default: claude-sonnet-4-6)

import json
import hashlib
import os

from fase3_memoria import agente_revision_con_memoria
from fase2_herramientas import ejecutar_herramienta


def necesita_aprobacion(revision: dict) -> bool:
    hallazgos_criticos = [
        h for h in revision["hallazgos"]
        if h["severidad"] == "critical"
    ]
    return len(hallazgos_criticos) > 0


def solicitar_aprobacion_cli(revision: dict) -> dict:
    """Checkpoint HITL en terminal. En producción: webhook, Slack, UI."""
    criticos = [h for h in revision["hallazgos"] if h["severidad"] == "critical"]

    print("\n=== REVISIÓN REQUIERE APROBACIÓN ===")
    print(f"Se encontraron {len(criticos)} hallazgo(s) crítico(s):\n")

    for i, h in enumerate(criticos, 1):
        print(f"{i}. Línea {h.get('linea', '?')}: {h['descripcion']}")
        print(f"   Sugerencia: {h['sugerencia']}\n")

    print("Opciones:")
    print("  [a] Aprobar y emitir informe completo")
    print("  [m] Modificar un hallazgo antes de emitir")
    print("  [d] Descartar hallazgos críticos con justificación")

    opcion = input("\nElige [a/m/d]: ").strip().lower()

    if opcion == "a":
        return {"aprobado": True, "revision": revision}

    elif opcion == "m":
        idx = int(input(f"Número de hallazgo a modificar (1-{len(criticos)}): ")) - 1
        nueva_desc = input("Nueva descripción: ")
        criticos[idx]["descripcion"] = nueva_desc
        return {"aprobado": True, "revision": revision}

    elif opcion == "d":
        justificacion = input("Justificación para descartar: ")
        revision["hallazgos"] = [h for h in revision["hallazgos"] if h["severidad"] != "critical"]
        revision["hitl_descarte"] = justificacion
        return {"aprobado": True, "revision": revision}

    return {"aprobado": False, "revision": revision}


def pipeline_revision_completo(codigo: str, ruta: str, proyecto_dir: str) -> dict:
    """Pipeline completo: loop → memoria → HITL → informe."""
    revision = agente_revision_con_memoria(codigo, ruta, proyecto_dir)

    if necesita_aprobacion(revision):
        resultado = solicitar_aprobacion_cli(revision)
        if not resultado["aprobado"]:
            return {"estado": "rechazado", "revision": None}
        revision = resultado["revision"]

    nombre_informe = f"revision_{hashlib.md5(ruta.encode()).hexdigest()[:8]}.json"
    ejecutar_herramienta("write_report", {
        "content": json.dumps(revision, indent=2, ensure_ascii=False),
        "filename": nombre_informe
    }, proyecto_dir)

    return revision


if __name__ == "__main__":
    import sys
    codigo = open(sys.argv[1]).read() if len(sys.argv) > 1 else """
import subprocess

def ejecutar_comando(cmd_usuario):
    # CRÍTICO: inyección de comandos — nunca hacer esto
    resultado = subprocess.run(cmd_usuario, shell=True, capture_output=True, text=True)
    return resultado.stdout
"""
    ruta = sys.argv[1] if len(sys.argv) > 1 else "test.py"
    proyecto = sys.argv[2] if len(sys.argv) > 2 else os.getcwd()

    resultado = pipeline_revision_completo(codigo, ruta, proyecto)
    print(json.dumps(resultado, indent=2, ensure_ascii=False))
