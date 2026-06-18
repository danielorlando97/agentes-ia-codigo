"""Validación de output de agentes — tres capas independientes.

Demuestra las tres capas de validación:
1. Esquema: el output tiene el formato correcto (Pydantic / JSON)
2. Contenido: el output no contiene datos sensibles (regex)
3. Acción: las tool calls son seguras para ejecutar

Sin API key — las llamadas al LLM son simuladas.

Uso:
    python validacion.py
    python validacion.py --capa esquema
    python validacion.py --capa contenido
    python validacion.py --capa accion

Cómo ejecutar:
    make py SCRIPT=python/15-seguridad/validacion.py

Qué esperar:
    3 capas de validación aplicadas al output del agente:
    1. Esquema (Pydantic/JSON schema)  2. Contenido (regex, PII)  3. Negocio (reglas).
    Muestra qué outputs pasan cada capa y qué errores se detectan.

Variables de entorno:
    MODEL — modelo a usar (default: claude-sonnet-4-6)
"""

import argparse
import json
import re
import sys
from dataclasses import dataclass, field
from typing import Any

# ─────────────────────────────────────────────
# Tipos
# ─────────────────────────────────────────────

@dataclass
class ToolCall:
    nombre: str
    params: dict[str, Any] = field(default_factory=dict)


@dataclass
class OutputAgente:
    respuesta: str
    acciones: list[ToolCall] = field(default_factory=list)
    confianza: float = 1.0
    referencias: list[str] = field(default_factory=list)


@dataclass
class ResultadoValidacion:
    valido: bool
    capa_fallida: str | None = None
    motivo: str | None = None
    output: OutputAgente | None = None


# ─────────────────────────────────────────────
# Capa 1: validación de esquema
# ─────────────────────────────────────────────

def validar_esquema(output_raw: str) -> tuple[OutputAgente | None, str | None]:
    """Retorna (OutputAgente, None) si válido, (None, error) si no."""
    try:
        data = json.loads(output_raw)
    except json.JSONDecodeError as e:
        return None, f"JSON inválido: {e}"

    if "respuesta" not in data:
        return None, "Campo 'respuesta' ausente"
    if not isinstance(data.get("confianza", 1.0), (int, float)):
        return None, "Campo 'confianza' debe ser número"
    if not (0.0 <= float(data.get("confianza", 1.0)) <= 1.0):
        return None, "Campo 'confianza' debe estar entre 0.0 y 1.0"

    acciones = []
    for a in data.get("acciones", []):
        if "nombre" not in a:
            return None, "Tool call sin campo 'nombre'"
        acciones.append(ToolCall(nombre=a["nombre"], params=a.get("params", {})))

    return OutputAgente(
        respuesta=data["respuesta"],
        acciones=acciones,
        confianza=float(data.get("confianza", 1.0)),
        referencias=data.get("referencias", []),
    ), None


# ─────────────────────────────────────────────
# Capa 2: validación de contenido
# ─────────────────────────────────────────────

PATRONES_SENSIBLES = [
    (r"\b\d{3}-\d{2}-\d{4}\b", "SSN"),
    (r"\b\d{4}[\s-]\d{4}[\s-]\d{4}[\s-]\d{4}\b", "tarjeta de crédito"),
    (r"password:\s*\S+", "contraseña"),
    (r"api[_-]?key:\s*\S+", "API key"),
    (r"token:\s*[A-Za-z0-9._-]{20,}", "token de sesión"),
]


def validar_contenido(output: OutputAgente) -> tuple[bool, str | None]:
    """Retorna (True, None) si el contenido es seguro."""
    for patron, tipo in PATRONES_SENSIBLES:
        if re.search(patron, output.respuesta, re.IGNORECASE):
            return False, f"Dato sensible en respuesta: {tipo}"
    return True, None


# ─────────────────────────────────────────────
# Capa 3: validación de acción
# ─────────────────────────────────────────────

ACCIONES_PROHIBIDAS = {"delete_database", "drop_table", "rm_rf", "send_bulk_email"}
DIRECTORIO_TRABAJO = "/workspace"


def validar_accion(tool_call: ToolCall) -> tuple[bool, str | None]:
    """Retorna (True, None) si la acción es segura."""
    if tool_call.nombre in ACCIONES_PROHIBIDAS:
        return False, f"Acción prohibida: '{tool_call.nombre}'"

    if tool_call.nombre == "write_file":
        ruta = tool_call.params.get("path", "")
        if ".." in ruta:
            return False, f"Path traversal detectado: '{ruta}'"
        if not ruta.startswith(DIRECTORIO_TRABAJO):
            return False, f"Escritura fuera del directorio de trabajo: '{ruta}'"

    if tool_call.nombre == "send_email":
        destinatarios = tool_call.params.get("to", [])
        externos = [d for d in destinatarios if not d.endswith("@empresa.com")]
        if externos:
            return False, f"Email a destino no autorizado: {externos}"

    return True, None


# ─────────────────────────────────────────────
# Pipeline completo
# ─────────────────────────────────────────────

def validar_pipeline(output_raw: str) -> ResultadoValidacion:
    # Capa 1: esquema
    output, error_esquema = validar_esquema(output_raw)
    if output is None:
        return ResultadoValidacion(valido=False, capa_fallida="esquema", motivo=error_esquema)

    # Capa 2: contenido
    contenido_ok, motivo_contenido = validar_contenido(output)
    if not contenido_ok:
        return ResultadoValidacion(valido=False, capa_fallida="contenido", motivo=motivo_contenido)

    # Capa 3: acciones
    for accion in output.acciones:
        accion_ok, motivo_accion = validar_accion(accion)
        if not accion_ok:
            return ResultadoValidacion(valido=False, capa_fallida="accion",
                                       motivo=f"[{accion.nombre}] {motivo_accion}")

    return ResultadoValidacion(valido=True, output=output)


# ─────────────────────────────────────────────
# Casos de prueba
# ─────────────────────────────────────────────

CASOS = {
    "valido_sin_acciones": json.dumps({
        "respuesta": "Tu pedido llega el jueves.",
        "confianza": 0.95,
        "referencias": ["pedido_12345"]
    }),
    "valido_con_accion_segura": json.dumps({
        "respuesta": "He creado el ticket de soporte.",
        "acciones": [{"nombre": "write_file",
                      "params": {"path": "/workspace/tickets/t001.txt", "content": "..."}}],
        "confianza": 0.88
    }),
    "falla_esquema_json": '{"respuesta": "incompleto"',
    "falla_esquema_campo": json.dumps({"texto": "sin campo respuesta"}),
    "falla_contenido_ssn": json.dumps({
        "respuesta": "El SSN del usuario es 123-45-6789.",
        "confianza": 0.5
    }),
    "falla_contenido_apikey": json.dumps({
        "respuesta": "La API key es: api_key: sk-abcdef123456",
        "confianza": 0.7
    }),
    "falla_accion_prohibida": json.dumps({
        "respuesta": "Limpiando base de datos.",
        "acciones": [{"nombre": "delete_database", "params": {"confirm": True}}],
        "confianza": 0.9
    }),
    "falla_accion_path_traversal": json.dumps({
        "respuesta": "Archivo escrito.",
        "acciones": [{"nombre": "write_file",
                      "params": {"path": "../../etc/passwd", "content": "..."}}],
        "confianza": 0.85
    }),
    "falla_email_externo": json.dumps({
        "respuesta": "Email enviado.",
        "acciones": [{"nombre": "send_email",
                      "params": {"to": ["atacante@external.com"], "body": "datos..."}}],
        "confianza": 0.7
    }),
}



def demo_capa(capa: str | None):
    casos_filtrados = {
        "esquema": ["falla_esquema_json", "falla_esquema_campo", "valido_sin_acciones"],
        "contenido": ["falla_contenido_ssn", "falla_contenido_apikey", "valido_sin_acciones"],
        "accion": ["falla_accion_prohibida", "falla_accion_path_traversal",
                   "falla_email_externo", "valido_con_accion_segura"],
    }

    nombres = casos_filtrados.get(capa, list(CASOS.keys())) if capa else list(CASOS.keys())

    print(f"\n{'='*64}")
    print(f"  VALIDACIÓN DE OUTPUT — {'capa ' + capa.upper() if capa else 'pipeline completo'}")
    print(f"{'='*64}")
    print(f"  {'Caso':<38} {'Válido':<8} {'Detalle'}")
    print(f"  {'-'*38} {'-'*8} {'-'*28}")

    for nombre in nombres:
        if nombre not in CASOS:
            continue
        r = validar_pipeline(CASOS[nombre])
        valido_str = "✓" if r.valido else f"✗ [{r.capa_fallida}]"
        detalle = r.motivo[:35] if r.motivo else ("OK" if r.valido else "")
        print(f"  {nombre:<38} {valido_str:<8} {detalle}")

    print(f"{'='*64}\n")


def main():
    parser = argparse.ArgumentParser(description="Validación de output de agentes.")
    parser.add_argument("--capa", choices=["esquema", "contenido", "accion"],
                        help="Mostrar solo casos de una capa")
    args = parser.parse_args()
    demo_capa(args.capa)


if __name__ == "__main__":
    main()
