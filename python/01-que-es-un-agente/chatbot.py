"""
Chatbot — conversacion turn-by-turn con memoria de sesion.

Qué demuestra:
    El bloque de construccion mas basico: un LLM con historial de mensajes.
    No hay tools, no hay loop autonomo — el usuario controla el flujo.
    Este es el nivel "Procesador" en el espectro de autonomia (cap. 1).

Patron clave:
    session[] acumula todos los turnos (user + assistant). En cada llamada
    se envia la sesion completa — el LLM no tiene memoria propia, la
    memoria la lleva el codigo.

Cómo ejecutar:
    make py SCRIPT=python/01-que-es-un-agente/chatbot.py

Qué esperar:
    Prompt interactivo "> ". Escribe mensajes, el modelo responde.
    Escribe "salir" para terminar. El modelo recuerda el hilo completo.

Variables de entorno:
    MODEL — modelo a usar (default: claude-sonnet-4-6)
"""
import os
import anthropic

MODEL = os.environ.get("MODEL", "claude-sonnet-4-6")
SYSTEM = "Eres un asistente util. Responde de forma concisa."


def chat() -> None:
    client = anthropic.Anthropic()
    session: list[dict] = []
    print(f"Chatbot iniciado (modelo: {MODEL}). Escribe 'salir' para terminar.")
    while True:
        msg = input("> ")
        if msg.strip().lower() == "salir":
            break
        session.append({"role": "user", "content": msg})
        response = client.messages.create(
            model=MODEL,
            max_tokens=1024,
            system=SYSTEM,
            messages=session,  # historial completo: el LLM no tiene memoria propia
        )
        text = "".join(b.text for b in response.content if b.type == "text")
        session.append({"role": "assistant", "content": text})
        print(text)


if __name__ == "__main__":
    chat()
