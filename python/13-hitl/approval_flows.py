# Approval flows: clasificación de acciones por riesgo y gates de aprobación humana
#
# Cómo ejecutar:
#   make py SCRIPT=python/13-hitl/approval_flows.py
#
# Qué esperar:
#   El agente clasifica cada accion por nivel de riesgo (low/medium/high/critical).
#   Las acciones criticas esperan aprobacion humana via CLI.
#
# Variables de entorno:
#   MODEL — modelo a usar (default: claude-sonnet-4-6)

import json
import time
import uuid
from dataclasses import dataclass, field
from typing import Callable, Optional
import os
import anthropic

cliente = anthropic.Anthropic()

# ─── Clasificación de riesgo ─────────────────────────────────────────────────

ACCIONES_ALTO_RIESGO = {
    "borrar_datos", "enviar_email_masivo", "transferencia_dinero",
    "modificar_cuenta_usuario", "desplegar_produccion", "revocar_accesos",
}

ACCIONES_MEDIO_RIESGO = {
    "escribir_produccion", "operacion_bulk", "cambiar_configuracion",
}

UMBRAL_REGISTROS = 100


def clasificar_accion(nombre: str, params: dict) -> str:
    if nombre in ACCIONES_ALTO_RIESGO:
        return "alto"
    if nombre in ACCIONES_MEDIO_RIESGO:
        return "medio"
    if params.get("registros_afectados", 0) > UMBRAL_REGISTROS:
        return "alto"
    if not params.get("reversible", True):
        return "alto"
    return "bajo"


def describir_impacto(nombre: str, params: dict) -> str:
    registros = params.get("registros_afectados", 0)
    tabla = params.get("tabla", "desconocida")
    if nombre == "borrar_datos":
        return f"Se borrarán {registros} registros de la tabla '{tabla}' en producción. Esta operación es irreversible."
    if nombre == "enviar_email_masivo":
        dest = params.get("destinatarios", 0)
        return f"Se enviará un email a {dest} usuarios. No puede deshacerse una vez enviado."
    return f"Acción '{nombre}' con parámetros: {json.dumps(params, ensure_ascii=False)}"


# ─── Cola de aprobaciones pendientes ─────────────────────────────────────────

@dataclass
class SolicitudAprobacion:
    id: str
    nombre_accion: str
    params: dict
    impacto: str
    timestamp: float
    expira_en: float  # unix timestamp
    decision: Optional[str] = None  # "aprobar" | "rechazar" | "modificar"
    params_modificados: Optional[dict] = None


_cola: dict[str, SolicitudAprobacion] = {}


def solicitar_aprobacion_sincrona(nombre: str, params: dict) -> dict:
    """Simula la interacción humana en CLI; en producción esto sería una UI/API."""
    impacto = describir_impacto(nombre, params)
    print(f"\n[APROBACIÓN REQUERIDA]")
    print(f"Acción: {nombre}")
    print(f"Impacto: {impacto}")
    print("Opciones: [a]probar / [r]echazar / [m]odificar")

    decision = input("Tu decisión: ").strip().lower()
    if decision.startswith("a"):
        return {"tipo": "aprobar", "params": params}
    if decision.startswith("m"):
        nuevos = input("Nuevos parámetros (JSON): ").strip()
        try:
            return {"tipo": "modificar", "params_modificados": json.loads(nuevos)}
        except json.JSONDecodeError:
            return {"tipo": "rechazar", "motivo": "parámetros modificados inválidos"}
    return {"tipo": "rechazar", "motivo": "rechazado por el usuario"}


def encolar_aprobacion(nombre: str, params: dict, ttl_horas: int = 4) -> str:
    """Encola para aprobación asíncrona (HOTL). Devuelve id de solicitud."""
    sol_id = str(uuid.uuid4())[:8]
    ahora = time.time()
    _cola[sol_id] = SolicitudAprobacion(
        id=sol_id,
        nombre_accion=nombre,
        params=params,
        impacto=describir_impacto(nombre, params),
        timestamp=ahora,
        expira_en=ahora + ttl_horas * 3600,
    )
    print(f"[COLA] Acción '{nombre}' encolada (id={sol_id}, expira en {ttl_horas}h)")
    return sol_id


# ─── Ejecutor con approval gate ───────────────────────────────────────────────

AprobadorFn = Callable[[str, dict], dict]


def ejecutar_herramienta_con_approval(
    nombre: str,
    params: dict,
    fn_herramienta: Callable,
    modo: str = "sincrono",  # "sincrono" (HITL) | "cola" (HOTL) | "auto"
) -> dict:
    nivel = clasificar_accion(nombre, params)

    if nivel == "bajo" or modo == "auto":
        resultado = fn_herramienta(nombre, params)
        print(f"[AUTO] {nombre}: {resultado}")
        return {"estado": "ejecutado", "resultado": resultado}

    if nivel == "medio" or modo == "cola":
        sol_id = encolar_aprobacion(nombre, params)
        return {"estado": "pendiente_revision", "id": sol_id}

    # nivel == "alto" → HITL bloqueante
    respuesta = solicitar_aprobacion_sincrona(nombre, params)

    if respuesta["tipo"] == "aprobar":
        resultado = fn_herramienta(nombre, respuesta["params"])
        return {"estado": "ejecutado", "resultado": resultado}
    if respuesta["tipo"] == "modificar":
        resultado = fn_herramienta(nombre, respuesta["params_modificados"])
        return {"estado": "ejecutado_modificado", "resultado": resultado}
    return {"estado": "rechazado", "motivo": respuesta.get("motivo", "")}


# ─── Agente con approval gate integrado ──────────────────────────────────────

HERRAMIENTAS = [
    {
        "name": "buscar_info",
        "description": "Busca información. Acción reversible y segura.",
        "input_schema": {
            "type": "object",
            "properties": {"query": {"type": "string"}},
            "required": ["query"],
        },
    },
    {
        "name": "borrar_datos",
        "description": "Borra registros de la base de datos. IRREVERSIBLE.",
        "input_schema": {
            "type": "object",
            "properties": {
                "tabla": {"type": "string"},
                "registros_afectados": {"type": "integer"},
            },
            "required": ["tabla", "registros_afectados"],
        },
    },
]


def _ejecutar_tool_real(nombre: str, params: dict) -> str:
    if nombre == "buscar_info":
        return f"Información encontrada para '{params['query']}': resultado simulado."
    if nombre == "borrar_datos":
        return f"[SIMULADO] Se habrían borrado {params['registros_afectados']} registros de '{params['tabla']}'."
    return f"Herramienta '{nombre}' no reconocida."


def agente_con_approval(tarea: str, modo_aprobacion: str = "sincrono") -> str:
    mensajes = [{"role": "user", "content": tarea}]

    for _ in range(10):
        respuesta = cliente.messages.create(
            model=os.environ.get("MODEL", "claude-sonnet-4-6"),
            max_tokens=1024,
            tools=HERRAMIENTAS,
            messages=mensajes,
        )

        mensajes.append({"role": "assistant", "content": respuesta.content})

        if respuesta.stop_reason == "end_turn":
            return next((b.text for b in respuesta.content if hasattr(b, "text")), "")

        if respuesta.stop_reason == "tool_use":
            tool_results = []
            for bloque in respuesta.content:
                if bloque.type != "tool_use":
                    continue
                resultado = ejecutar_herramienta_con_approval(
                    nombre=bloque.name,
                    params=bloque.input,
                    fn_herramienta=_ejecutar_tool_real,
                    modo=modo_aprobacion,
                )
                contenido = json.dumps(resultado, ensure_ascii=False)
                tool_results.append({
                    "type": "tool_result",
                    "tool_use_id": bloque.id,
                    "content": contenido,
                })
            mensajes.append({"role": "user", "content": tool_results})

    return "[max iteraciones]"


if __name__ == "__main__":
    print("=== Clasificación de riesgo ===")
    tests = [
        ("buscar_info", {"query": "usuarios activos"}),
        ("escribir_produccion", {"tabla": "users", "registros_afectados": 50}),
        ("borrar_datos", {"tabla": "users", "registros_afectados": 847}),
    ]
    for nombre, params in tests:
        nivel = clasificar_accion(nombre, params)
        print(f"  {nombre}: {nivel}")

    print("\n=== Agente con approval (modo auto — sin interacción) ===")
    resultado = agente_con_approval(
        "Busca información sobre usuarios activos en el último mes.",
        modo_aprobacion="auto",
    )
    print(f"Resultado: {resultado[:200]}")
