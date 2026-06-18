"""Integración de Mem0 como capa de memoria para un agente existente.

Mem0 extrae memorias automáticamente de cada turno conversacional
mediante un LLM auxiliar. La integración mínima requiere 3 llamadas:
add() al final de cada turno, search() al inicio, get_all() para contexto completo.

Requiere:
    pip install mem0ai anthropic
    export MEM0_API_KEY=...  (o usar Memory() local sin clave)
    export ANTHROPIC_API_KEY=...

Cómo ejecutar:
    pip install mem0ai   # instalar Mem0
    make py SCRIPT=python/06-memoria/20-implementaciones/mem0_integration.py

Qué esperar:
    El agente extrae memorias automáticamente de cada turno. Muestra el ciclo
    add() → search() → get_all() en acción. Sin Mem0, muestra el patrón simulado.

Variables de entorno:
    MODEL        — modelo a usar (default: claude-sonnet-4-6)
    MEM0_API_KEY — clave de Mem0 (opcional, para modo cloud)
"""
import os
import anthropic

# Mem0 ofrece dos modos:
# - Memory()        → local, sin API key, SQLite + embeddings locales
# - MemoryClient()  → cloud, requiere MEM0_API_KEY
try:
    from mem0 import MemoryClient
    mem = MemoryClient()          # usa MEM0_API_KEY del entorno
except Exception:
    from mem0 import Memory       # type: ignore[import]
    mem = Memory()                # fallback local

MODEL = os.environ.get("MODEL", "claude-sonnet-4-6")
USER_ID = "usuario-demo"


def recuperar_contexto(query: str, top_k: int = 5) -> str:
    """Busca memorias relevantes para el turno actual."""
    resultados = mem.search(query=query, user_id=USER_ID, limit=top_k)
    if not resultados:
        return ""
    lineas = [f"- {r['memory']}" for r in resultados]
    return "## Memoria recuperada\n" + "\n".join(lineas)


def guardar_turno(mensaje_usuario: str, respuesta_asistente: str) -> None:
    """Extrae y guarda memorias del turno. Latencia típica: 400-900ms."""
    mem.add(
        messages=[
            {"role": "user",      "content": mensaje_usuario},
            {"role": "assistant", "content": respuesta_asistente},
        ],
        user_id=USER_ID,
    )


def turno(historial: list[dict], mensaje: str) -> str:
    """Un turno del agente con memoria Mem0 integrada."""
    cliente = anthropic.Anthropic()

    # 1. Recuperar contexto relevante antes de responder
    contexto_memoria = recuperar_contexto(mensaje)
    system = "Eres un asistente técnico."
    if contexto_memoria:
        system += f"\n\n{contexto_memoria}"

    historial.append({"role": "user", "content": mensaje})
    respuesta_api = cliente.messages.create(
        model=MODEL,
        max_tokens=1024,
        system=system,
        messages=historial,
    )
    respuesta = respuesta_api.content[0].text
    historial.append({"role": "assistant", "content": respuesta})

    # 2. Guardar el turno para sesiones futuras (post-turno — fuera del hot path ideal)
    guardar_turno(mensaje, respuesta)
    return respuesta


if __name__ == "__main__":
    historial: list[dict] = []

    # Turno 1: el agente aprende la preferencia
    r1 = turno(historial, "Prefiero trabajar con Python 3.12 en producción.")
    print(f"Agente: {r1[:120]}\n")

    # Turno 2: nueva sesión — historial vacío, pero Mem0 recupera la preferencia
    historial_nuevo: list[dict] = []
    r2 = turno(historial_nuevo, "¿Qué lenguaje debería usar para el nuevo servicio?")
    print(f"Agente (sesión nueva): {r2[:120]}")

    # Ver todas las memorias guardadas
    print("\n--- memorias almacenadas ---")
    for m_item in mem.get_all(user_id=USER_ID):
        print(f"  {m_item['memory']}")
