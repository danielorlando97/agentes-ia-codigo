"""Mini-proyecto: El checkpoint simulator.

Simula un agente que ejecuta una tarea de múltiples pasos con checkpoints
de aprobación humana. Observa cómo los checkpoints bloquean la ejecución,
qué pasa cuando el humano rechaza una acción, y cómo se recupera el agente.

Modo interactivo: el usuario aprueba, modifica o rechaza cada checkpoint.
Modo automático (--auto): simula decisiones humanas predefinidas.

Uso:
    python mini-checkpoint-sim.py
    python mini-checkpoint-sim.py --auto
    python mini-checkpoint-sim.py --escenario destructivo
    python mini-checkpoint-sim.py --escenario suave --auto

Cómo ejecutar:
    make py SCRIPT=python/13-hitl/mini-checkpoint-sim.py

Qué esperar:
    Agente de múltiples pasos que pausa en checkpoints para aprobación humana.
    Muestra qué pasa cuando el humano rechaza una acción: rollback al checkpoint.

Variables de entorno:
    MODEL — modelo a usar (default: claude-sonnet-4-6)
"""

import argparse
import json
import sys
import time
from dataclasses import dataclass, field
from typing import Literal
from enum import Enum

# ── tipos ──────────────────────────────────────────────────────────────────────

class RiesgoNivel(Enum):
    BAJO = "bajo"
    MEDIO = "medio"
    ALTO = "alto"
    CRITICO = "crítico"


@dataclass
class Accion:
    nombre: str
    descripcion: str
    riesgo: RiesgoNivel
    reversible: bool
    requiere_hitl: bool
    payload: dict = field(default_factory=dict)


@dataclass
class Checkpoint:
    accion: Accion
    contexto: str
    decision: Literal["pendiente", "aprobado", "rechazado", "modificado"] = "pendiente"
    modificacion: dict = field(default_factory=dict)
    latencia_s: float = 0.0


@dataclass
class EstadoAgente:
    tarea: str
    historial: list[str] = field(default_factory=list)
    checkpoints: list[Checkpoint] = field(default_factory=list)
    completado: bool = False
    abortado: bool = False

    def log(self, msg: str) -> None:
        self.historial.append(msg)
        print(f"  [agente] {msg}")


# ── escenarios ─────────────────────────────────────────────────────────────────

ACCIONES_SUAVES = [
    Accion(
        nombre="leer_archivos",
        descripcion="Leer 47 archivos de configuración del proyecto",
        riesgo=RiesgoNivel.BAJO,
        reversible=True,
        requiere_hitl=False,
        payload={"archivos": 47, "directorios": ["config/", "src/"]},
    ),
    Accion(
        nombre="analizar_dependencias",
        descripcion="Ejecutar análisis estático de dependencias",
        riesgo=RiesgoNivel.BAJO,
        reversible=True,
        requiere_hitl=False,
        payload={"herramienta": "pip-audit"},
    ),
    Accion(
        nombre="generar_reporte",
        descripcion="Escribir reporte de hallazgos en reports/audit_2026.md",
        riesgo=RiesgoNivel.MEDIO,
        reversible=True,
        requiere_hitl=True,
        payload={"archivo": "reports/audit_2026.md", "tamaño_kb": 12},
    ),
    Accion(
        nombre="enviar_notificacion",
        descripcion="Enviar email de resumen al equipo de seguridad (3 destinatarios)",
        riesgo=RiesgoNivel.MEDIO,
        reversible=False,
        requiere_hitl=True,
        payload={"destinatarios": ["security@empresa.com"], "asunto": "Audit 2026"},
    ),
    Accion(
        nombre="cerrar_tarea",
        descripcion="Marcar tarea como completada en el tracker",
        riesgo=RiesgoNivel.BAJO,
        reversible=True,
        requiere_hitl=False,
        payload={"ticket": "SEC-1247"},
    ),
]

ACCIONES_DESTRUCTIVAS = [
    Accion(
        nombre="listar_usuarios",
        descripcion="Obtener lista de usuarios inactivos hace >90 días",
        riesgo=RiesgoNivel.BAJO,
        reversible=True,
        requiere_hitl=False,
        payload={"filtro": "last_login < 90 días"},
    ),
    Accion(
        nombre="revocar_tokens",
        descripcion="Revocar tokens de acceso de 1,247 usuarios inactivos",
        riesgo=RiesgoNivel.ALTO,
        reversible=False,
        requiere_hitl=True,
        payload={"usuarios_afectados": 1247, "tokens": "API + OAuth"},
    ),
    Accion(
        nombre="archivar_datos",
        descripcion="Archivar datos de usuarios revocados en cold storage",
        riesgo=RiesgoNivel.ALTO,
        reversible=False,
        requiere_hitl=True,
        payload={"gb": 23.4, "destino": "s3://cold-archive/users/2026/"},
    ),
    Accion(
        nombre="eliminar_cuentas",
        descripcion="Eliminar definitivamente 1,247 cuentas de la base de datos",
        riesgo=RiesgoNivel.CRITICO,
        reversible=False,
        requiere_hitl=True,
        payload={"usuarios": 1247, "operacion": "DELETE FROM users WHERE ..."},
    ),
    Accion(
        nombre="purgar_logs",
        descripcion="Purgar logs de acceso de usuarios eliminados",
        riesgo=RiesgoNivel.ALTO,
        reversible=False,
        requiere_hitl=True,
        payload={"registros": 89_432, "tabla": "access_logs"},
    ),
]

ESCENARIOS = {
    "suave": ("Auditoría de seguridad y notificación al equipo", ACCIONES_SUAVES),
    "destructivo": ("Limpieza de usuarios inactivos en producción", ACCIONES_DESTRUCTIVAS),
}

# Decisiones automáticas por acción (para modo --auto)
DECISIONES_AUTO = {
    "suave": {
        "generar_reporte": "aprobado",
        "enviar_notificacion": "modificado",  # cambia destinatarios
    },
    "destructivo": {
        "revocar_tokens": "aprobado",
        "archivar_datos": "aprobado",
        "eliminar_cuentas": "rechazado",  # el humano rechaza el paso más destructivo
        "purgar_logs": "rechazado",       # al rechazar eliminar_cuentas, este no llega
    },
}


# ── motor de simulación ────────────────────────────────────────────────────────

ICONOS_RIESGO = {
    RiesgoNivel.BAJO: "🟢",
    RiesgoNivel.MEDIO: "🟡",
    RiesgoNivel.ALTO: "🔴",
    RiesgoNivel.CRITICO: "🚨",
}

def mostrar_checkpoint(cp: Checkpoint) -> None:
    accion = cp.accion
    print(f"\n{'─'*60}")
    print(f"  CHECKPOINT — Aprobación requerida")
    print(f"{'─'*60}")
    print(f"  Acción:      {accion.nombre}")
    print(f"  Descripción: {accion.descripcion}")
    print(f"  Riesgo:      {ICONOS_RIESGO[accion.riesgo]} {accion.riesgo.value.upper()}")
    print(f"  Reversible:  {'Sí' if accion.reversible else 'NO — irreversible'}")
    print(f"  Contexto:    {cp.contexto}")
    print(f"  Payload:     {json.dumps(accion.payload, ensure_ascii=False)}")
    print(f"{'─'*60}")


def solicitar_decision_interactiva(cp: Checkpoint) -> str:
    mostrar_checkpoint(cp)
    print("\n  Opciones:")
    print("  [A] Aprobar   [R] Rechazar   [M] Modificar   [S] Escalar")
    while True:
        resp = input("\n  Tu decisión > ").strip().upper()
        if resp == "A":
            return "aprobado"
        elif resp == "R":
            return "rechazado"
        elif resp == "M":
            print("  (En este simulador, 'modificar' ajusta el payload con 'dry_run: true')")
            return "modificado"
        elif resp == "S":
            print("  [Escalado a supervisor — simulado como aprobado con flag 'escalado'.]")
            return "aprobado"
        else:
            print("  Opción no válida.")


def solicitar_decision_auto(cp: Checkpoint, decisions: dict) -> str:
    mostrar_checkpoint(cp)
    decision = decisions.get(cp.accion.nombre, "aprobado")
    print(f"\n  [auto] Decisión automática: {decision.upper()}")
    time.sleep(0.3)
    return decision


def ejecutar_accion(accion: Accion, modificado: bool = False) -> str:
    sufijo = " (modo dry-run por modificación)" if modificado else ""
    return f"✓ {accion.nombre} ejecutado{sufijo} — payload: {json.dumps(accion.payload, ensure_ascii=False)}"


def simular_agente(
    tarea: str,
    acciones: list[Accion],
    auto: bool,
    decisiones_auto: dict,
) -> EstadoAgente:
    estado = EstadoAgente(tarea=tarea)
    estado.log(f"Iniciando tarea: {tarea}")

    for accion in acciones:
        if estado.abortado:
            break

        estado.log(f"Preparando: {accion.nombre}")

        if accion.requiere_hitl:
            contexto = f"El agente ha completado {len(estado.historial)} pasos hasta ahora."
            cp = Checkpoint(accion=accion, contexto=contexto)
            estado.checkpoints.append(cp)

            t_inicio = time.time()
            if auto:
                decision = solicitar_decision_auto(cp, decisiones_auto)
            else:
                decision = solicitar_decision_interactiva(cp)
            cp.latencia_s = time.time() - t_inicio
            cp.decision = decision

            if decision == "rechazado":
                estado.log(f"✗ {accion.nombre} RECHAZADO por humano — abortando ejecución")
                estado.abortado = True
                break
            elif decision == "modificado":
                cp.modificacion = {"dry_run": True}
                resultado = ejecutar_accion(accion, modificado=True)
            else:
                resultado = ejecutar_accion(accion)
        else:
            resultado = ejecutar_accion(accion)

        estado.log(resultado)

    if not estado.abortado:
        estado.completado = True
        estado.log("Tarea completada.")

    return estado


# ── reporte final ─────────────────────────────────────────────────────────────

def imprimir_reporte(estado: EstadoAgente) -> None:
    print(f"\n{'='*60}")
    print(f"  REPORTE FINAL — CHECKPOINT SIMULATOR")
    print(f"{'='*60}")
    print(f"\n  Tarea: {estado.tarea}")
    print(f"  Estado: {'COMPLETADA' if estado.completado else 'ABORTADA'}")
    print(f"  Pasos ejecutados: {len(estado.historial)}")
    print(f"\n  Checkpoints ({len(estado.checkpoints)} total):")
    print(f"  {'─'*56}")

    total_latencia = 0.0
    aprobados = rechazados = modificados = 0
    for cp in estado.checkpoints:
        icon = {"aprobado": "✓", "rechazado": "✗", "modificado": "~", "pendiente": "?"}.get(cp.decision, "?")
        print(f"  {icon} {cp.accion.nombre:<30} [{cp.decision:<10}]  {ICONOS_RIESGO[cp.accion.riesgo]} {cp.accion.riesgo.value}")
        if cp.latencia_s > 0:
            print(f"    Latencia de decisión: {cp.latencia_s:.1f}s")
            total_latencia += cp.latencia_s
        if cp.decision == "aprobado":
            aprobados += 1
        elif cp.decision == "rechazado":
            rechazados += 1
        elif cp.decision == "modificado":
            modificados += 1

    total = len(estado.checkpoints)
    if total > 0:
        tasa_aprobacion = (aprobados + modificados) / total * 100
        print(f"\n  Tasa de aprobación:   {tasa_aprobacion:.0f}%  ({aprobados} aprobados, {modificados} modificados, {rechazados} rechazados)")
        if tasa_aprobacion > 95:
            print("  ⚠️  Approval fatigue: tasa > 95% — revisar umbrales de riesgo")
        if total_latencia > 0:
            print(f"  Latencia total HITL:  {total_latencia:.1f}s")
            print(f"  Latencia media/cp:    {total_latencia/total:.1f}s")

    print(f"\n{'='*60}")
    print("  Lecciones clave:")
    print("  • Los checkpoints bloquean la ejecución — cuantos más, mayor latencia total")
    print("  • Un rechazo puede abortar todo el pipeline — diseñar flujos de fallback")
    print("  • 'Modificar' permite ajustar sin rechazar — reduce la tasa de aborto")
    print("  • Tasa > 95% de aprobación = umbrales de riesgo mal calibrados")
    print(f"{'='*60}")


# ── main ──────────────────────────────────────────────────────────────────────

def main():
    parser = argparse.ArgumentParser(description="Simula checkpoints HITL sin API.")
    parser.add_argument("--auto", action="store_true",
                        default=not sys.stdin.isatty(),
                        help="Modo automático — decisiones humanas simuladas")
    parser.add_argument("--escenario", choices=["suave", "destructivo"], default="suave",
                        help="Escenario de tareas (default: suave)")
    args = parser.parse_args()

    tarea, acciones = ESCENARIOS[args.escenario]
    decisiones_auto = DECISIONES_AUTO[args.escenario]

    print(f"\n{'='*60}")
    print(f"  CHECKPOINT SIMULATOR")
    print(f"  Escenario: {args.escenario}  |  Modo: {'automático' if args.auto else 'interactivo'}")
    print(f"{'='*60}")
    print(f"\n  Tarea: {tarea}")
    print(f"  Acciones: {len(acciones)} pasos")
    checkpoints_n = sum(1 for a in acciones if a.requiere_hitl)
    print(f"  Checkpoints HITL: {checkpoints_n}")

    estado = simular_agente(tarea, acciones, args.auto, decisiones_auto)
    imprimir_reporte(estado)


if __name__ == "__main__":
    main()
