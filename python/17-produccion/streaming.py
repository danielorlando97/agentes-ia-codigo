# Streaming SSE: muestra tokens del agente en tiempo real con eventos de tool calls
#
# Cómo ejecutar:
#   make py SCRIPT=python/17-produccion/streaming.py
#
# Qué esperar:
#   Tokens del agente aparecen en tiempo real via SSE.
#   Eventos de tool calls visibles durante el streaming.
#
# Variables de entorno:
#   MODEL — modelo a usar (default: claude-sonnet-4-6)

import asyncio
import json
import os
import anthropic

cliente = anthropic.AsyncAnthropic()

HERRAMIENTAS = [
    {
        "name": "buscar_docs",
        "description": "Busca en la documentación. Úsala cuando el usuario pregunte por APIs o funciones.",
        "input_schema": {
            "type": "object",
            "properties": {"query": {"type": "string"}},
            "required": ["query"],
        },
    }
]


def ejecutar_herramienta(nombre: str, params: dict) -> str:
    if nombre == "buscar_docs":
        return f"Documentación para '{params['query']}': función disponible desde v2.0, acepta str y devuelve dict."
    return f"Error: herramienta '{nombre}' no encontrada."


async def stream_agente_simple(pregunta: str) -> None:
    """Stream básico: imprime tokens a medida que llegan del modelo."""
    async with cliente.messages.stream(
        model=os.environ.get("MODEL", "claude-sonnet-4-6"),
        max_tokens=1024,
        tools=HERRAMIENTAS,
        messages=[{"role": "user", "content": pregunta}],
    ) as stream:
        async for evento in stream:
            if hasattr(evento, "type") and evento.type == "content_block_delta":
                if hasattr(evento.delta, "text"):
                    print(evento.delta.text, end="", flush=True)

            # Tool call completa cuando termina el bloque
            if hasattr(evento, "type") and evento.type == "content_block_stop":
                snap = await stream.get_final_message()
                for bloque in snap.content:
                    if hasattr(bloque, "type") and bloque.type == "tool_use":
                        print(f"\n[tool: {bloque.name}({bloque.input})]")
                        resultado = ejecutar_herramienta(bloque.name, bloque.input)
                        print(f"[resultado: {resultado[:100]}]")
    print()


async def stream_loop_react(pregunta: str, queue: asyncio.Queue) -> None:
    """Loop ReAct con streaming: produce eventos SSE en la queue para el cliente."""
    mensajes = [{"role": "user", "content": pregunta}]
    MAX_PASOS = 10

    for _ in range(MAX_PASOS):
        async with cliente.messages.stream(
            model=os.environ.get("MODEL", "claude-sonnet-4-6"),
            max_tokens=1024,
            tools=HERRAMIENTAS,
            messages=mensajes,
        ) as stream:
            async for evento in stream:
                if hasattr(evento, "type") and evento.type == "content_block_delta":
                    if hasattr(evento.delta, "text"):
                        await queue.put({"type": "text", "content": evento.delta.text})

            respuesta = await stream.get_final_message()

        mensajes.append({"role": "assistant", "content": respuesta.content})

        if respuesta.stop_reason == "end_turn":
            await queue.put({"type": "done"})
            return

        # Ejecutar tool calls y notificar al cliente
        tool_results = []
        for bloque in respuesta.content:
            if bloque.type == "tool_use":
                await queue.put({"type": "tool_start", "tool": bloque.name})
                resultado = ejecutar_herramienta(bloque.name, bloque.input)
                await queue.put({"type": "tool_done", "tool": bloque.name})
                tool_results.append({
                    "type": "tool_result",
                    "tool_use_id": bloque.id,
                    "content": resultado,
                })

        if tool_results:
            mensajes.append({"role": "user", "content": tool_results})

    await queue.put({"type": "error", "content": "Límite de pasos alcanzado"})


async def consumir_stream(queue: asyncio.Queue) -> None:
    """Consume la queue de eventos SSE e imprime como lo haría un cliente."""
    buffer = ""
    while True:
        evento = await queue.get()
        if evento["type"] == "text":
            buffer += evento["content"]
            print(evento["content"], end="", flush=True)
        elif evento["type"] == "tool_start":
            print(f"\n[iniciando {evento['tool']}...]")
        elif evento["type"] == "tool_done":
            print(f"[{evento['tool']} completado]")
        elif evento["type"] in ("done", "error"):
            if evento["type"] == "error":
                print(f"\n[error: {evento['content']}]")
            break
    print()


async def main() -> None:
    print("=== Stream simple ===")
    await stream_agente_simple("¿Qué hace la función filter_context?")

    print("\n=== Loop ReAct con streaming ===")
    queue: asyncio.Queue = asyncio.Queue()

    productor = asyncio.create_task(
        stream_loop_react("Busca cómo funciona filter_context y explícamelo.", queue)
    )
    consumidor = asyncio.create_task(consumir_stream(queue))

    await asyncio.gather(productor, consumidor)


if __name__ == "__main__":
    asyncio.run(main())
