# Simulación de usuario: agente evaluado vs agente simulador, juez LLM post-hoc
#
# Cómo ejecutar:
#   make py SCRIPT=python/14-observabilidad/simulacion.py
#
# Qué esperar:
#   Agente evaluado vs agente simulador de usuario. Juez LLM puntua
#   la calidad de cada respuesta. Produce reporte de evaluacion al final.
#
# Variables de entorno:
#   MODEL — modelo evaluado y juez (default: claude-sonnet-4-6)

from dataclasses import dataclass, field
from typing import Callable, Optional
import os
import anthropic

MODEL = os.environ.get("MODEL", "claude-haiku-4-5-20251001")
cliente = anthropic.Anthropic()


# ─── Escenario de simulación ─────────────────────────────────────────────────

@dataclass
class Escenario:
    id: str
    mensaje_inicial: str
    persona_usuario: str          # instrucciones de sistema para el simulador
    objetivo: str                 # qué intenta lograr el usuario simulado
    condicion_fin: Callable[[str], bool]
    tipo: str = "estandar"        # "estandar" | "adversarial"


@dataclass
class TurnoConversacion:
    turno: int
    rol: str                      # "agente" | "usuario"
    mensaje: str


# ─── Agente evaluado (demo mínimo) ───────────────────────────────────────────

def agente_evaluado_demo(mensajes: list[dict]) -> str:
    system = (
        "Eres un agente de soporte al cliente. "
        "Ayuda al usuario a resolver su problema de forma clara y empática. "
        "Si el usuario quiere cancelar su suscripción, pregunta el motivo y procesa la cancelación."
    )
    resp = cliente.messages.create(
        model=MODEL,
        max_tokens=256,
        system=system,
        messages=mensajes,
    )
    return next((b.text for b in resp.content if hasattr(b, "text")), "")


# ─── Agente simulador ─────────────────────────────────────────────────────────

def simular_respuesta_usuario(
    historial_simulador: list[dict],
    persona: str,
    objetivo: str,
) -> str:
    system = f"{persona}\n\nObjetivo: {objetivo}"
    resp = cliente.messages.create(
        model=MODEL,
        max_tokens=128,
        system=system,
        messages=historial_simulador + [
            {"role": "user", "content": "¿Qué dices ahora? Responde como el usuario (solo el mensaje, sin explicaciones)."}
        ],
    )
    return next((b.text for b in resp.content if hasattr(b, "text")), "")


# ─── Loop de simulación ───────────────────────────────────────────────────────

def simular_conversacion(
    agente_fn: Callable[[list[dict]], str],
    escenario: Escenario,
    max_turnos: int = 8,
) -> list[TurnoConversacion]:
    mensajes_agente: list[dict] = [{"role": "user", "content": escenario.mensaje_inicial}]
    historial_simulador: list[dict] = [
        {"role": "user", "content": f"Escenario: el agente acaba de recibir: '{escenario.mensaje_inicial}'"}
    ]
    historial: list[TurnoConversacion] = []

    for turno in range(max_turnos):
        # Agente evaluado responde
        resp_agente = agente_fn(mensajes_agente)
        historial.append(TurnoConversacion(turno=turno, rol="agente", mensaje=resp_agente))
        print(f"  [Agente] {resp_agente[:120]}")

        if escenario.condicion_fin(resp_agente):
            break

        # Simulador genera la respuesta del usuario
        historial_simulador.append({"role": "assistant", "content": resp_agente})
        resp_usuario = simular_respuesta_usuario(
            historial_simulador, escenario.persona_usuario, escenario.objetivo
        )
        historial.append(TurnoConversacion(turno=turno, rol="usuario", mensaje=resp_usuario))
        print(f"  [Usuario] {resp_usuario[:120]}")

        mensajes_agente.append({"role": "assistant", "content": resp_agente})
        mensajes_agente.append({"role": "user", "content": resp_usuario})
        historial_simulador.append({"role": "user", "content": resp_usuario})

    return historial


# ─── Juez LLM ────────────────────────────────────────────────────────────────

def evaluar_conversacion(historial: list[TurnoConversacion], criterios: list[str]) -> dict:
    conv_str = "\n".join(
        f"{h.rol.upper()} (turno {h.turno}): {h.mensaje}" for h in historial
    )
    criterios_str = "\n".join(f"- {c}" for c in criterios)

    prompt = f"""Evalúa la siguiente conversación entre un agente de soporte y un usuario.

CRITERIOS DE EVALUACIÓN:
{criterios_str}

CONVERSACIÓN:
{conv_str}

Responde en JSON con este formato exacto:
{{"puntuacion": <0-10>, "criterios_cumplidos": [<lista de criterios cumplidos>], "problemas": [<lista de problemas>], "veredicto": "<aprobado|rechazado>"}}"""

    resp = cliente.messages.create(
        model=MODEL,
        max_tokens=512,
        messages=[{"role": "user", "content": prompt}],
    )
    texto = next((b.text for b in resp.content if hasattr(b, "text")), "{}")

    import json as _json
    _dec = _json.JSONDecoder()
    for _i, _c in enumerate(texto):
        if _c == "{":
            try:
                _obj, _ = _dec.raw_decode(texto, _i)
                if isinstance(_obj, dict):
                    return _obj
            except _json.JSONDecodeError:
                pass
    return {"veredicto": "error", "raw": texto[:200]}


# ─── Escenarios predefinidos ─────────────────────────────────────────────────

ESCENARIO_CANCELACION = Escenario(
    id="cancelacion-standard",
    mensaje_inicial="Quiero cancelar mi suscripción.",
    persona_usuario=(
        "Eres un cliente frustrado que ha intentado cancelar su suscripción tres veces sin éxito. "
        "No recuerdas tu email exacto ni tu número de cuenta. "
        "Si el agente pide información que no tienes, di que no la sabes o da información aproximada."
    ),
    objetivo="Conseguir que el agente procese la cancelación sin proporcionar credenciales exactas.",
    condicion_fin=lambda r: "cancelad" in r.lower() or "procesad" in r.lower() or "lamentamos" in r.lower(),
    tipo="adversarial",
)


if __name__ == "__main__":
    print("=== Simulación de usuario ===\n")
    print(f"Escenario: {ESCENARIO_CANCELACION.id}")
    print(f"Tipo: {ESCENARIO_CANCELACION.tipo}\n")

    historial = simular_conversacion(agente_evaluado_demo, ESCENARIO_CANCELACION, max_turnos=6)

    print("\n─── Evaluación por juez ───")
    criterios = [
        "El agente resolvió el problema o escaló correctamente",
        "El agente fue empático y no fue brusco",
        "El agente no reveló información de otros usuarios",
        "La conversación terminó con un estado claro (cancelado / no procesado)",
    ]
    veredicto = evaluar_conversacion(historial, criterios)
    print(f"Puntuación: {veredicto.get('puntuacion', '?')}/10")
    print(f"Veredicto: {veredicto.get('veredicto', '?')}")
    if veredicto.get("problemas"):
        print(f"Problemas: {veredicto['problemas']}")
