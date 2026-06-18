"""Selección dinámica de herramientas por similitud Jaccard.

Demuestra el mecanismo de tool retrieval sin dependencias externas:
Jaccard sobre word sets reemplaza embeddings para mostrar el bucle
selección → agente → selección.

Requiere: pip install anthropic

Cómo ejecutar:
    make py SCRIPT=python/10-decisiones/seleccion_herramientas.py

Qué esperar:
    El agente tiene acceso a 20 herramientas pero solo ve las 5 mas relevantes
    para la query actual. Muestra el score Jaccard de cada herramienta candidata.

Variables de entorno:
    MODEL — modelo a usar (default: claude-sonnet-4-6)
"""
import os
import json
import re
from dataclasses import dataclass

import anthropic

MODEL = os.environ.get("MODEL", "claude-sonnet-4-6")
JACCARD_THRESHOLD = 0.04   # sin stemming, palabras distintas reducen el score
TOP_K = 3

SYSTEM_AGENTE = """\
Eres un agente de asistencia. Tienes acceso a un subconjunto de herramientas relevantes
para la tarea actual. Usa las herramientas disponibles para responder.
Si no tienes suficiente información después de una ronda, indica qué necesitarías."""


@dataclass
class Tool:
    name: str
    description: str
    # Texto completo indexado (nombre + descripción + casos de uso)
    index_text: str


# --- Indexación offline ---

def indexar_tools(tools: list[Tool]) -> dict[str, set[str]]:
    """Construye word sets para cada tool. Se ejecuta una sola vez al arrancar."""
    return {
        tool.name: set(tool.index_text.lower().split())
        for tool in tools
    }


# --- Selección en runtime ---

def seleccionar_tools(
    query: str,
    index: dict[str, set[str]],
    tools_by_name: dict[str, Tool],
    k: int = TOP_K,
    threshold: float = JACCARD_THRESHOLD,
) -> list[Tool]:
    query_words = set(query.lower().split())
    scores: list[tuple[str, float]] = []

    for tool_name, tool_words in index.items():
        union = query_words | tool_words
        inter = query_words & tool_words
        score = len(inter) / len(union) if union else 0.0
        scores.append((tool_name, score))

    scores.sort(key=lambda x: x[1], reverse=True)
    return [
        tools_by_name[name]
        for name, score in scores[:k]
        if score >= threshold
    ]


def construir_query_seleccion(tarea: str, ultimo_resultado: str | None) -> str:
    """Combina tarea original con contexto del último resultado para guiar la selección."""
    if not ultimo_resultado:
        return tarea
    # El último resultado orienta qué herramienta se necesita a continuación
    return f"{tarea} — contexto: {ultimo_resultado[:200]}"


# --- Agente con selección dinámica ---

def agente(tarea: str, tools: list[Tool], client: anthropic.Anthropic) -> None:
    index = indexar_tools(tools)
    tools_by_name = {t.name: t for t in tools}
    ultimo_resultado: str | None = None

    print(f"\nTarea: {tarea}")
    print("=" * 60)

    for turno in range(1, 3):
        query_sel = construir_query_seleccion(tarea, ultimo_resultado)
        tools_seleccionadas = seleccionar_tools(query_sel, index, tools_by_name)

        nombres_sel = [t.name for t in tools_seleccionadas]
        print(f"\n[Turno {turno}] Query de selección: {query_sel[:80]}")
        print(f"[Turno {turno}] Tools seleccionadas: {nombres_sel}")

        # Definiciones en formato que Claude entiende
        tool_defs = [
            {
                "name": t.name,
                "description": t.description,
                "input_schema": {
                    "type": "object",
                    "properties": {"query": {"type": "string", "description": "Parámetro de consulta"}},
                    "required": ["query"],
                },
            }
            for t in tools_seleccionadas
        ]

        response = client.messages.create(
            model=MODEL,
            max_tokens=512,
            system=SYSTEM_AGENTE,
            tools=tool_defs,
            messages=[{"role": "user", "content": tarea}],
        )

        # Recopilar lo que el modelo decidió hacer (sin ejecutar realmente las tools)
        acciones = []
        for block in response.content:
            if block.type == "tool_use":
                acciones.append(f"{block.name}({json.dumps(block.input)})")

        if acciones:
            ultimo_resultado = f"Llamadas planeadas: {'; '.join(acciones)}"
            print(f"[Turno {turno}] Acciones: {ultimo_resultado}")
        else:
            texto = next(
                (b.text for b in response.content if hasattr(b, "text")), ""
            )
            ultimo_resultado = texto[:200]
            print(f"[Turno {turno}] Respuesta directa: {ultimo_resultado}")
            break


# --- Catálogo de herramientas ---

TOOLS = [
    Tool(
        name="buscar_contratos",
        description="Busca contratos por nombre de cliente, fecha o estado",
        index_text="buscar contratos cliente acuerdo documento legal renovación fecha vencimiento",
    ),
    Tool(
        name="calcular_fechas",
        description="Calcula diferencias entre fechas, días hasta vencimiento, rangos",
        index_text="calcular fechas diferencia días semanas meses vencimiento plazo duración",
    ),
    Tool(
        name="consultar_crm",
        description="Obtiene información de clientes del CRM: contactos, historial, estado",
        index_text="crm cliente contacto historial estado cuenta empresa organización",
    ),
    Tool(
        name="generar_factura",
        description="Crea facturas con detalle de servicios, impuestos y datos de pago",
        index_text="factura generar crear cobro pago servicio impuesto importe total",
    ),
    Tool(
        name="enviar_email",
        description="Envía correos electrónicos a clientes o equipos internos",
        index_text="email correo enviar notificación mensaje destinatario asunto adjunto",
    ),
    Tool(
        name="analizar_logs",
        description="Analiza logs de sistema para diagnosticar errores y anomalías",
        index_text="logs errores sistema diagnosticar anomalía stack trace excepción servidor",
    ),
]


def main() -> None:
    client = anthropic.Anthropic()

    agente(
        tarea="Busca el contrato de Acme Corp y calcula cuántos días faltan para su renovación",
        tools=TOOLS,
        client=client,
    )


if __name__ == "__main__":
    main()
