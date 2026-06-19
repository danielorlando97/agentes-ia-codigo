# UX de agentes interactivos — format_approval_request.
#
# Transforma una acción técnica del agente (SQL, parámetros de herramienta)
# en una solicitud de aprobación legible para humanos no técnicos.
#
# La clave: título en lenguaje de negocio, impacto cuantificado, opciones
# que capturan matices más allá de aprobar/rechazar.
#
# Cómo ejecutar:
#   make py FILE=python/13-hitl/ux.py

from dataclasses import dataclass
from typing import Any


@dataclass
class Opcion:
    etiqueta: str
    accion: str
    descripcion: str


@dataclass
class SolicitudAprobacion:
    titulo: str
    descripcion: str
    impacto: str
    reversible: bool
    vista_previa: list[str]
    contexto: str
    opciones: list[Opcion]

    def mostrar(self) -> None:
        reversible_str = "Sí" if self.reversible else "NO (irreversible)"
        print(f"  Título:      {self.titulo}")
        print(f"  Descripción: {self.descripcion}")
        print(f"  Impacto:     {self.impacto}")
        print(f"  Reversible:  {reversible_str}")
        if self.vista_previa:
            print(f"  Muestra:     {', '.join(self.vista_previa)}")
        print(f"  Contexto:    {self.contexto}")
        opciones_str = " | ".join(f"[{o.etiqueta}]" for o in self.opciones)
        print(f"  Opciones:    {opciones_str}")


def format_approval_request(
    herramienta: str,
    params: dict[str, Any],
    contexto_agente: dict[str, Any],
) -> SolicitudAprobacion:
    if herramienta == "send_email":
        n_afectados = len(params.get("to", []))
    else:
        n_afectados = params.get("n_registros", len(params.get("ids", [])))
    entidad = params.get("tabla", "registros")
    condicion = params.get("condicion", "")

    if herramienta == "db_delete":
        titulo = f"Eliminar {n_afectados} {entidad} {condicion}".strip()
    elif herramienta == "send_email":
        titulo = f"Enviar email a {len(params.get('to', []))} destinatarios"
    elif herramienta == "db_update":
        titulo = f"Actualizar {n_afectados} {entidad} {condicion}".strip()
    else:
        titulo = f"Ejecutar {herramienta}"

    razon = contexto_agente.get("razon", "completar la tarea actual")
    ultimos_pasos = contexto_agente.get("ultimos_pasos", [])

    return SolicitudAprobacion(
        titulo=titulo,
        descripcion=f"El agente propone esto porque: {razon}",
        impacto=f"Afectará {n_afectados} {entidad}",
        reversible=params.get("reversible", True),
        vista_previa=params.get("ejemplos", [])[:5],
        contexto=" → ".join(ultimos_pasos[-3:]) if ultimos_pasos else "inicio del flujo",
        opciones=[
            Opcion("Aprobar", "aprobar", "Ejecutar la acción con los parámetros actuales"),
            Opcion("Rechazar", "rechazar", "Cancelar la acción y notificar al agente"),
            Opcion("Modificar parámetros", "modificar", "Ajustar el alcance antes de ejecutar"),
            Opcion("Escalar a supervisor", "escalar", "Enviar la decisión a un nivel superior"),
            Opcion("Posponer 24h", "posponer", "Ejecutar mañana a la misma hora"),
        ],
    )


def main() -> None:
    print("=== UX de agentes: format_approval_request ===\n")

    # Caso 1: eliminación destructiva
    print("--- Caso 1: Eliminación irreversible ---\n")

    accion = {
        "herramienta": "db_delete",
        "params": {
            "tabla": "usuarios",
            "condicion": "inactivos desde ene 2024",
            "n_registros": 1247,
            "reversible": False,
            "ejemplos": ["user@ejemplo.com", "otro@ejemplo.com", "tercero@ejemplo.com"],
        },
    }
    contexto = {
        "razon": "completar la limpieza de cuentas inactivas solicitada",
        "ultimos_pasos": ["analizar tabla usuarios", "filtrar por actividad", "calcular impacto"],
    }

    print("SIN FORMAT (lo que el agente produce internamente):")
    print("  DELETE FROM usuarios WHERE inactive AND last_login < '2024-01-01'\n")

    solicitud = format_approval_request(
        accion["herramienta"],
        accion["params"],
        contexto,
    )

    print("CON FORMAT (lo que el humano ve):")
    solicitud.mostrar()

    # Caso 2: envío de emails
    print("\n--- Caso 2: Envío masivo de emails ---\n")

    accion2 = {
        "herramienta": "send_email",
        "params": {
            "to": ["a@b.com"] * 843,
            "asunto": "Actualización de términos de servicio",
            "reversible": False,
        },
    }
    contexto2 = {
        "razon": "notificar a todos los usuarios del cambio de ToS antes del 30/06",
        "ultimos_pasos": ["redactar email", "seleccionar destinatarios"],
    }

    solicitud2 = format_approval_request(
        accion2["herramienta"],
        accion2["params"],
        contexto2,
    )

    print("CON FORMAT:")
    solicitud2.mostrar()

    # Indicador de progreso
    print("\n--- Indicador de progreso (ejecución larga) ---\n")
    steps = [
        ("✓", "Analizar estructura del repositorio", "2.3s"),
        ("✓", "Identificar tests existentes", "1.1s"),
        ("→", "Generando nuevos tests para módulo auth...", "30s estimado"),
        (" ", "Ejecutar suite de tests", "pendiente"),
        (" ", "Generar reporte de cobertura", "pendiente"),
    ]
    for estado, descripcion, tiempo in steps:
        print(f"  [{estado}] {descripcion} ({tiempo})")


if __name__ == "__main__":
    main()
