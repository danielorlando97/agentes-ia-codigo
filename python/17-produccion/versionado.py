# versionado — Versionado de prompts y A/B testing: registro inmutable, rollback, canary.
#
# Cómo ejecutar:
#     make py SCRIPT=python/17-produccion/versionado.py
#
# Qué esperar:
#     Registra versiones de un prompt de clasificación, simula un A/B test entre v1 y v2,
#     muestra la distribución de tráfico y cómo hacer rollback si una versión falla.
#
# Variables de entorno:
#     MODEL — modelo a usar (default: claude-sonnet-4-6)
import hashlib
import random
from dataclasses import dataclass, field
from datetime import datetime
from typing import Optional
import anthropic

cliente = anthropic.Anthropic()


@dataclass
class VersionPrompt:
    id: str
    version: str
    prompt: str
    # Versión fija del modelo — nunca alias como "claude-sonnet-4-6"
    modelo: str
    creado: datetime
    evaluacion: dict   # resultado del golden set al desplegar
    activo: bool = False
    canary_peso: float = 0.0  # 0.0 = no en canary, 0.1 = 10% del tráfico


_registro: dict[str, VersionPrompt] = {}
_historial_activos: list[str] = []  # para rollback


def registrar_prompt(
    prompt: str,
    modelo: str,
    version: str,
    evaluacion: dict,
) -> str:
    pid = hashlib.sha256(f"{prompt}::{modelo}".encode()).hexdigest()[:16]
    _registro[pid] = VersionPrompt(
        id=pid,
        version=version,
        prompt=prompt,
        modelo=modelo,
        creado=datetime.now(),
        evaluacion=evaluacion,
    )
    print(f"[version] Registrado prompt {pid} v{version} ({modelo})")
    return pid


def activar_prompt(prompt_id: str) -> None:
    for p in _registro.values():
        if p.activo:
            p.activo = False
            p.canary_peso = 0.0

    _registro[prompt_id].activo = True
    _historial_activos.append(prompt_id)
    print(f"[version] Activado prompt {prompt_id} v{_registro[prompt_id].version}")


def rollback() -> Optional[str]:
    """Vuelve al prompt activo anterior."""
    if len(_historial_activos) < 2:
        print("[version] No hay versión anterior para rollback")
        return None

    _historial_activos.pop()  # descartar actual
    anterior_id = _historial_activos[-1]
    activar_prompt(anterior_id)
    print(f"[version] Rollback a {anterior_id}")
    return anterior_id


def activar_canary(canary_id: str, peso: float = 0.1) -> None:
    """Envía `peso` fracción del tráfico al prompt canary."""
    if canary_id not in _registro:
        raise ValueError(f"Prompt {canary_id} no registrado")
    _registro[canary_id].canary_peso = peso
    print(f"[canary] Prompt {canary_id} recibe {peso*100:.0f}% del tráfico")


def obtener_prompt_para_request() -> VersionPrompt:
    """Selecciona prompt activo o canary según el peso configurado."""
    candidatos_canary = [p for p in _registro.values() if p.canary_peso > 0]

    for canary in candidatos_canary:
        if random.random() < canary.canary_peso:
            return canary

    for p in _registro.values():
        if p.activo:
            return p

    raise RuntimeError("No hay prompt activo")


def llamar_con_version(pregunta: str) -> dict:
    pv = obtener_prompt_para_request()
    respuesta = cliente.messages.create(
        model=pv.modelo,
        max_tokens=256,
        system=pv.prompt,
        messages=[{"role": "user", "content": pregunta}],
    )
    return {
        "respuesta": respuesta.content[0].text,
        "prompt_version": pv.version,
        "prompt_id": pv.id,
    }


if __name__ == "__main__":
    # Versiones fijas de modelos — nunca aliases
    VERSIONES_MODELO = {
        "haiku":  "claude-haiku-4-5-20251001",
        "sonnet": "claude-sonnet-4-6-20250219",
        "opus":   "claude-opus-4-7-20250219",
    }

    print("=== Registro y activación ===")
    v1 = registrar_prompt(
        prompt="Eres un asistente técnico conciso.",
        modelo=VERSIONES_MODELO["sonnet"],
        version="1.0.0",
        evaluacion={"pass_rate": 0.82, "casos": 50},
    )
    activar_prompt(v1)

    v2 = registrar_prompt(
        prompt="Eres un asistente técnico conciso. Usa ejemplos de código cuando sea útil.",
        modelo=VERSIONES_MODELO["sonnet"],
        version="1.1.0",
        evaluacion={"pass_rate": 0.87, "casos": 50},
    )

    print("\n=== Canary deployment (10% tráfico a v1.1.0) ===")
    activar_canary(v2, peso=0.10)

    print("\n=== Llamadas con selección automática de versión ===")
    for i in range(5):
        r = llamar_con_version("¿Qué es un agente ReAct?")
        print(f"Request {i+1}: version={r['prompt_version']} | "
              f"respuesta={r['respuesta'][:60]}...")

    print("\n=== Rollback ===")
    activar_prompt(v2)  # promover v2 como activo
    rollback()          # volver a v1
