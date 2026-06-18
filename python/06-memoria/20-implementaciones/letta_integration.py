"""Integración con Letta: agente que gestiona su propia memoria via tool calls.

En Letta, el LLM controla explícitamente qué guardar y qué recuperar
mediante herramientas de memoria nativas (core_memory_replace,
archival_memory_insert, archival_memory_search). El agente decide
cuándo acceder a la memoria — no hay extracción automática.

Requiere:
    pip install letta-client
    export LETTA_API_KEY=...   (para cloud)
    # o letta server --port 8283  (para local)

Cómo ejecutar:
    pip install letta-client   # instalar Letta
    make py SCRIPT=python/06-memoria/20-implementaciones/letta_integration.py

Qué esperar:
    El agente gestiona su propia memoria: decide qué guardar y qué recuperar
    usando herramientas de memoria nativas. Sin Letta instalado, muestra
    el patrón simulado.

Variables de entorno:
    MODEL         — modelo a usar (default: claude-sonnet-4-6)
    LETTA_API_KEY — clave de Letta (opcional, para modo cloud)
"""

# Letta ofrece cliente cloud y local.
# El patrón de uso es idéntico en ambos modos.
try:
    from letta_client import Letta
    # Cloud: requiere LETTA_API_KEY
    client = Letta(token="LETTA_API_KEY_AQUI", base_url="https://api.letta.ai")
except ImportError:
    raise SystemExit("pip install letta-client")


PERSONA = (
    "Eres un asistente técnico que recuerda las preferencias del usuario "
    "y las usa para dar respuestas personalizadas."
)
HUMAN_CONTEXT = "El usuario es un desarrollador de software."


def crear_agente() -> str:
    """Crea un agente Letta con memoria inicial y devuelve su ID."""
    from letta_client.schemas.memory import ChatMemory

    agente = client.agents.create(
        name="asistente-tecnico",
        memory=ChatMemory(
            human=HUMAN_CONTEXT,
            persona=PERSONA,
        ),
        model=os.environ.get("MODEL", "claude-sonnet-4-6"),
        embedding="letta-free",   # reemplazar por tu proveedor de embeddings
    )
    return agente.id


def enviar_mensaje(agent_id: str, mensaje: str) -> str:
    """Envía un mensaje al agente. Letta gestiona la memoria internamente."""
    from letta_client.schemas.letta_message import LettaMessageUnion
    from letta_client.models.messages_request import MessagesRequest

    response = client.agents.messages.create(
        agent_id=agent_id,
        messages=[{"role": "user", "content": mensaje}],
    )

    # La respuesta puede incluir pasos de razonamiento y tool calls de memoria.
    # Filtramos solo los mensajes de texto del asistente.
    textos = [
        msg.content
        for msg in response.messages
        if hasattr(msg, "message_type") and msg.message_type == "assistant_message"
    ]
    return " ".join(textos) if textos else "[sin respuesta de texto]"


def ver_memoria_core(agent_id: str) -> dict:
    """Muestra el estado actual de la core memory del agente."""
    memory = client.agents.core_memory.retrieve(agent_id=agent_id)
    return {
        "human": memory.memory.get("human", {}).get("value", ""),
        "persona": memory.memory.get("persona", {}).get("value", ""),
    }


if __name__ == "__main__":
    print("Creando agente Letta...")
    agent_id = crear_agente()
    print(f"Agente creado: {agent_id}\n")

    # Turno 1: el agente aprende la preferencia y la guarda en core_memory
    r1 = enviar_mensaje(agent_id, "Prefiero trabajar con Python 3.12 en producción.")
    print(f"Agente: {r1[:150]}\n")

    # Turno 2: el agente usa la memoria para responder
    r2 = enviar_mensaje(agent_id, "¿Qué lenguaje debería usar para el nuevo microservicio?")
    print(f"Agente: {r2[:150]}\n")

    # Ver qué tiene en core_memory tras los dos turnos
    memoria = ver_memoria_core(agent_id)
    print("--- core memory ---")
    print(f"human:   {memoria['human'][:200]}")
    print(f"persona: {memoria['persona'][:100]}")

    # Limpiar: borrar el agente de demo
    client.agents.delete(agent_id=agent_id)
    print("\nAgente eliminado.")
