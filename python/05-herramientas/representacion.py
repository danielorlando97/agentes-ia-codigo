# Representación de herramientas: descripción, input_schema y errores de selección.
#
# Una herramienta es un contrato textual: nombre, descripción en lenguaje natural,
# y JSON Schema de parámetros. La calidad de ese contrato determina si el modelo
# elige la herramienta correcta (selección) y si genera los argumentos válidos
# (parametrización). IAC (Insufficient API Calls) — el modelo no invoca la tool
# cuando debería — es el error más frecuente, causado por descripciones pobres.
#
# Cómo ejecutar:
#   make py SCRIPT=python/05-herramientas/representacion.py
#
# Qué esperar:
#   Tres versiones de la misma herramienta con nombres/descripciones distintos.
#   El modelo elige (o no) la herramienta según como esté descrita.
#
# Variables de entorno:
#   MODEL — modelo a usar (default: claude-sonnet-4-6)

import os
import json
import anthropic

MODEL = os.environ.get("MODEL", "claude-haiku-4-5-20251001")


# --- Definiciones de herramientas ---

# Herramienta con descripción pobre — solo describe el mecanismo.
# Causa IAC: el modelo responde desde memoria en lugar de invocarla.
TOOL_MALA = {
    "name": "get_account_info",
    "description": "Gets account information from the database.",
    "input_schema": {
        "type": "object",
        "properties": {
            "id": {
                "type": "string",
            },
        },
        "required": ["id"],
    },
}

# Herramienta con descripción efectiva — incluye cuándo usarla, qué no hace,
# y qué campos devuelve. Resuelve la ambigüedad de selección.
TOOL_BUENA = {
    "name": "get_account_info",
    "description": (
        "Retrieves complete account information for a customer. "
        "Use this when the user asks about their account status, balance, "
        "subscription plan, or any account-specific detail. "
        "Do NOT use this for order-specific questions — use get_order_info instead. "
        "Returns: account_id, email, subscription_plan, account_balance, created_at."
    ),
    "input_schema": {
        "type": "object",
        "properties": {
            "account_id": {
                "type": "string",
                "description": (
                    "Customer account ID (format: ACC-XXXXXX). "
                    "If not provided, uses the ID from the current conversation context."
                ),
            },
        },
        "required": [],  # Opcional: el modelo puede inferirlo del contexto
        "additionalProperties": False,  # Equivalente a strict en Anthropic
    },
}

# Herramienta con schema bien documentado para formatos no obvios.
# Las descripciones de parámetros con ejemplos reducen errores de parametrización.
TOOL_BUSQUEDA = {
    "name": "search_orders",
    "description": (
        "Searches orders within a date range and optional status filter. "
        "Use when the user asks to find, list, or review orders. "
        "Do NOT use for a single known order ID — use get_order_info instead."
    ),
    "input_schema": {
        "type": "object",
        "properties": {
            "date_range": {
                "type": "string",
                "description": (
                    "Date range in ISO 8601 format: 'YYYY-MM-DD/YYYY-MM-DD'. "
                    "Example: '2024-01-01/2024-03-31'"
                ),
            },
            "status": {
                "type": "string",
                "enum": ["active", "inactive", "pending"],
                "description": (
                    "Account status filter. "
                    "Use 'active' for currently subscribed accounts."
                ),
            },
            "limit": {
                "type": "integer",
                "minimum": 1,
                "maximum": 100,
                "default": 20,
                "description": "Maximum number of results. Default is 20. Use higher values only for exports.",
            },
        },
        "required": ["date_range"],
        "additionalProperties": False,
    },
}


# --- Mock de herramientas ---

def mock_get_account_info(args: dict) -> str:
    account_id = args.get("account_id", args.get("id", "ACC-000000"))
    return json.dumps({
        "account_id": account_id,
        "email": "usuario@ejemplo.com",
        "subscription_plan": "Pro",
        "account_balance": 42.50,
        "created_at": "2023-05-15",
    })


# --- Demo: diferencia entre descripción mala y buena ---

def demo_descripcion(descripcion: str, tool: dict, pregunta: str) -> None:
    """Llama al modelo con la herramienta dada y muestra si la invoca o no."""
    client = anthropic.Anthropic()

    print(f"\n{'='*60}")
    print(f"[{descripcion}]")
    print(f"Pregunta: {pregunta}")
    print(f"Descripción de la herramienta: \"{tool['description'][:80]}...\"")

    response = client.messages.create(
        model=MODEL,
        max_tokens=512,
        tools=[tool],
        messages=[{"role": "user", "content": pregunta}],
    )

    if response.stop_reason == "tool_use":
        tool_block = next(b for b in response.content if b.type == "tool_use")
        print(f"\n  → El modelo invocó '{tool_block.name}'")
        print(f"    input: {json.dumps(tool_block.input)}")

        # Devolver el resultado y obtener respuesta final
        tool_result = mock_get_account_info(tool_block.input)
        final = client.messages.create(
            model=MODEL,
            max_tokens=256,
            tools=[tool],
            messages=[
                {"role": "user", "content": pregunta},
                {"role": "assistant", "content": response.content},
                {
                    "role": "user",
                    "content": [
                        {
                            "type": "tool_result",
                            "tool_use_id": tool_block.id,
                            "content": tool_result,
                            "is_error": False,
                        }
                    ],
                },
            ],
        )
        respuesta = "".join(b.text for b in final.content if b.type == "text")
        print(f"  ← Respuesta final: {respuesta[:120]}")
    else:
        # IAC: el modelo respondió sin llamar la herramienta
        texto = "".join(b.text for b in response.content if b.type == "text")
        print(f"\n  [IAC] El modelo respondió desde memoria sin invocar la herramienta.")
        print(f"  Respuesta: {texto[:120]}")


def demo_schema_detallado() -> None:
    """Muestra cómo un schema con descripciones de parámetros guía la parametrización."""
    client = anthropic.Anthropic()

    pregunta = "Muéstrame los pedidos pendientes de los últimos 3 meses, máximo 50."
    print(f"\n{'='*60}")
    print("[Schema con descripciones de parámetros]")
    print(f"Pregunta: {pregunta}")

    response = client.messages.create(
        model=MODEL,
        max_tokens=512,
        tools=[TOOL_BUSQUEDA],
        messages=[{"role": "user", "content": pregunta}],
    )

    if response.stop_reason == "tool_use":
        tool_block = next(b for b in response.content if b.type == "tool_use")
        print(f"\n  → El modelo invocó '{tool_block.name}'")
        print(f"    input: {json.dumps(tool_block.input, indent=4)}")
        print(f"\n  Notar: date_range en ISO 8601, status y limit correctamente inferidos.")
    else:
        texto = "".join(b.text for b in response.content if b.type == "text")
        print(f"\n  Respuesta directa: {texto[:120]}")


def main() -> None:
    print("=== Representación de herramientas: descripción y schema ===")
    print()
    print("Principios clave:")
    print("  - Descripción efectiva: CUÁNDO usar la tool + QUÉ hace + QUÉ NO hace")
    print("  - Schema: descripción de parámetros con ejemplos para formatos no obvios")
    print("  - additionalProperties: false equivale a strict (Anthropic aplica")
    print("    constrained decoding sobre input_schema por defecto, sin flag opt-in)")
    print()
    print("Tipos de error:")
    print("  - IAC (Insufficient API Calls): el modelo no llama la tool cuando debería")
    print("    Causa: descripción que solo describe el mecanismo ('Gets X from DB')")
    print("  - Llamada incorrecta: el modelo invoca la tool equivocada")
    print("    Causa: falta de diferenciación entre tools similares")

    # Caso 1: descripción pobre → potencial IAC
    demo_descripcion(
        descripcion="Descripción POBRE — solo describe el mecanismo",
        tool=TOOL_MALA,
        pregunta="¿Puedes verificar mi cuenta? Mi ID es ACC-123456.",
    )

    # Caso 2: descripción efectiva → selección correcta
    demo_descripcion(
        descripcion="Descripción EFECTIVA — incluye cuándo usar, qué no usar, qué devuelve",
        tool=TOOL_BUENA,
        pregunta="¿Puedes verificar mi cuenta? Mi ID es ACC-123456.",
    )

    # Caso 3: schema con parámetros documentados
    demo_schema_detallado()

    print(f"\n{'='*60}")
    print("Nota sobre strict / constrained decoding:")
    print("  OpenAI: campo 'strict: true' en la function — incompatible con parallel_tool_calls")
    print("  Anthropic: constrained decoding siempre activo sobre input_schema")
    print("             sin flag, compatible con parallel tool calls")
    print("  En ambos casos: reduce fallo de formato de 2-5% a <0.1%")


if __name__ == "__main__":
    main()
