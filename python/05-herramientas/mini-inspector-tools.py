"""Mini-proyecto: El inspector de tools.

Observa cómo el modelo elige herramientas, las invoca, y cómo el
feedback del resultado modifica el siguiente paso.
Compara llamadas seriales vs paralelas.

Requiere: ANTHROPIC_API_KEY

Uso:
    python mini-inspector-tools.py
    python mini-inspector-tools.py --modo serial
    python mini-inspector-tools.py --modo paralelo
    python mini-inspector-tools.py --modo feedback

Cómo ejecutar:
    make py SCRIPT=python/05-herramientas/mini-inspector-tools.py

Qué esperar:
    Inspector interactivo que muestra qué herramienta elige el modelo para cada
    consulta, el argumento generado, y el resultado. Compara serial vs paralelo.

Variables de entorno:
    MODEL — modelo a usar (default: claude-sonnet-4-6)
"""

import argparse
import json
import os
import sys
import time

try:
    import anthropic
except ImportError:
    print("Error: pip install anthropic")
    sys.exit(1)

MODEL = os.environ.get("MODEL", "claude-haiku-4-5-20251001")

HERRAMIENTAS = [
    {
        "name": "search_web",
        "description": "Busca información actual en la web.",
        "input_schema": {
            "type": "object",
            "properties": {
                "query": {"type": "string", "description": "Términos de búsqueda"},
                "num_results": {"type": "integer", "description": "Número de resultados (1-5)"},
            },
            "required": ["query"],
        },
    },
    {
        "name": "read_file",
        "description": "Lee el contenido de un archivo local.",
        "input_schema": {
            "type": "object",
            "properties": {"path": {"type": "string", "description": "Ruta del archivo"}},
            "required": ["path"],
        },
    },
    {
        "name": "run_code",
        "description": "Ejecuta código Python y devuelve el output.",
        "input_schema": {
            "type": "object",
            "properties": {"code": {"type": "string", "description": "Código a ejecutar"}},
            "required": ["code"],
        },
    },
    {
        "name": "write_report",
        "description": "Escribe un reporte en un archivo.",
        "input_schema": {
            "type": "object",
            "properties": {
                "filename": {"type": "string"},
                "content": {"type": "string"},
            },
            "required": ["filename", "content"],
        },
    },
]


def dispatcher(nombre: str, params: dict, simular_error: bool = False) -> str:
    """Dispatcher simulado — devuelve siempre string."""
    if simular_error and nombre == "search_web":
        return "ERROR: timeout después de 5s. El servicio de búsqueda no está disponible."
    if nombre == "search_web":
        q = params.get("query", "")
        return f"Resultados para '{q}': [1] Python asyncio docs - python.org, [2] asyncio tutorial - realpython.com"
    if nombre == "read_file":
        path = params.get("path", "")
        return f"Contenido de {path}:\n# Config\nversion = 1.0\ndebug = false"
    if nombre == "run_code":
        code = params.get("code", "")
        if "print" in code:
            return "Hello, World!"
        return "[Output vacío]"
    if nombre == "write_report":
        return f"Reporte guardado en {params.get('filename', 'report.txt')}"
    return f"Herramienta '{nombre}' no reconocida"


def modo_serial(client):
    print("\n  ── Modo serial: herramientas una a la vez ──")
    task = "Busca información sobre asyncio en Python, lee el archivo config.py, y escribe un reporte."
    mensajes = [{"role": "user", "content": task}]

    pasos = 0
    tokens_total = 0
    t0 = time.time()

    while pasos < 6:
        resp = client.messages.create(
            model=MODEL, max_tokens=512,
            messages=mensajes,
            tools=HERRAMIENTAS,
        )
        tokens_total += resp.usage.input_tokens + resp.usage.output_tokens
        pasos += 1

        tool_blocks = [b for b in resp.content if b.type == "tool_use"]
        if not tool_blocks:
            print(f"\n  Respuesta final: {resp.content[0].text[:100]}...")
            break

        print(f"\n  Paso {pasos}: {len(tool_blocks)} herramienta(s) invocada(s)")
        mensajes.append({"role": "assistant", "content": resp.content})

        results = []
        for tb in tool_blocks:
            resultado = dispatcher(tb.name, tb.input)
            print(f"    → {tb.name}({json.dumps(tb.input)[:50]}) = {resultado[:60]}...")
            results.append({"type": "tool_result", "tool_use_id": tb.id, "content": resultado})
        mensajes.append({"role": "user", "content": results})

    dur = time.time() - t0
    print(f"\n  Serial: {pasos} LLM calls | {tokens_total} tokens | {dur:.1f}s")


def modo_paralelo(client):
    print("\n  ── Modo paralelo: múltiples herramientas a la vez ──")
    task = (
        "En paralelo: busca 'asyncio Python' en la web Y lee el archivo 'config.py'. "
        "Luego escribe un reporte con los hallazgos."
    )
    mensajes = [{"role": "user", "content": task}]

    pasos = 0
    tokens_total = 0
    t0 = time.time()

    while pasos < 4:
        resp = client.messages.create(
            model=MODEL, max_tokens=512,
            messages=mensajes,
            tools=HERRAMIENTAS,
        )
        tokens_total += resp.usage.input_tokens + resp.usage.output_tokens
        pasos += 1

        tool_blocks = [b for b in resp.content if b.type == "tool_use"]
        if not tool_blocks:
            print(f"\n  Respuesta final: {resp.content[0].text[:100]}...")
            break

        print(f"\n  Paso {pasos}: {len(tool_blocks)} herramienta(s) en paralelo")
        mensajes.append({"role": "assistant", "content": resp.content})

        results = []
        for tb in tool_blocks:
            resultado = dispatcher(tb.name, tb.input)
            print(f"    → {tb.name}({json.dumps(tb.input)[:40]}...)")
            results.append({"type": "tool_result", "tool_use_id": tb.id, "content": resultado})

        print(f"    ← {len(results)} resultados devueltos simultáneamente")
        mensajes.append({"role": "user", "content": results})

    dur = time.time() - t0
    print(f"\n  Paralelo: {pasos} LLM calls | {tokens_total} tokens | {dur:.1f}s")
    print(f"  (La paralelización reduce LLM calls, no tokens — la latencia mejora, el costo no)")


def modo_feedback(client):
    print("\n  ── Modo feedback: cómo el error modifica el siguiente paso ──")
    task = "Busca información sobre asyncio y luego escribe un reporte."
    mensajes = [{"role": "user", "content": task}]
    tokens_total = 0

    for paso in range(4):
        resp = client.messages.create(
            model=MODEL, max_tokens=512,
            messages=mensajes,
            tools=HERRAMIENTAS,
        )
        tokens_total += resp.usage.input_tokens + resp.usage.output_tokens

        tool_blocks = [b for b in resp.content if b.type == "tool_use"]
        if not tool_blocks:
            print(f"\n  Respuesta final: {resp.content[0].text[:150]}...")
            break

        mensajes.append({"role": "assistant", "content": resp.content})
        results = []
        for tb in tool_blocks:
            # Simula error en primer intento de search_web
            simular_error = (paso == 0 and tb.name == "search_web")
            resultado = dispatcher(tb.name, tb.input, simular_error=simular_error)
            if simular_error:
                print(f"\n  Paso {paso+1}: {tb.name} → ERROR simulado")
                print(f"  Error devuelto: {resultado}")
                print(f"  Observa cómo el modelo adapta su siguiente acción...")
            else:
                print(f"\n  Paso {paso+1}: {tb.name} → OK")
            results.append({"type": "tool_result", "tool_use_id": tb.id, "content": resultado})
        mensajes.append({"role": "user", "content": results})

    print(f"\n  Tokens totales: {tokens_total}")
    print(f"  El modelo recibió el error como tool_result y cambió de estrategia.")
    print(f"  Clave: el dispatcher SIEMPRE devuelve string — nunca propaga excepciones.")


def main():
    parser = argparse.ArgumentParser(description="Inspector de tools — observa selección e invocación.")
    parser.add_argument("--modo", choices=["serial", "paralelo", "feedback", "todos"], default="todos")
    args = parser.parse_args()

    api_key = os.environ.get("ANTHROPIC_API_KEY")
    if not api_key:
        print("Error: ANTHROPIC_API_KEY no configurada")
        sys.exit(1)

    client = anthropic.Anthropic(api_key=api_key)

    print(f"\n{'='*60}")
    print(f"  INSPECTOR DE TOOLS")
    print(f"  Modelo: {MODEL}  |  Herramientas: {len(HERRAMIENTAS)}")
    print(f"{'='*60}")

    modos = {"serial": modo_serial, "paralelo": modo_paralelo, "feedback": modo_feedback}
    a_ejecutar = list(modos.keys()) if args.modo == "todos" else [args.modo]
    for nombre in a_ejecutar:
        modos[nombre](client)

    print(f"\n{'='*60}")


if __name__ == "__main__":
    main()
