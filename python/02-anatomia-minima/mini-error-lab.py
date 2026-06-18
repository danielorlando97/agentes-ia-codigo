"""
Mini-error-lab — el agente que se equivoca a proposito.

Qué demuestra:
    Los 4 errores mas frecuentes en produccion de agentes, junto con
    el diagnostico y la version corregida de cada uno:
    1. tool_use sin tool_result — la API devuelve HTTP 400
    2. Historial malformado — dos mensajes del mismo rol consecutivos
    3. Context overflow — el historial crece hasta superar la ventana
    4. Alucinacion de herramientas — el modelo inventa tools no definidas

Por qué este formato:
    Ver el error y la correccion en el mismo script facilita el aprendizaje.
    Cada demo muestra primero la version incorrecta (marcada ❌) y luego
    la correccion (marcada ✓), con tokens usados para comparar.

Cómo ejecutar:
    make py SCRIPT=python/02-anatomia-minima/mini-error-lab.py
    make py SCRIPT=python/02-anatomia-minima/mini-error-lab.py --error tool_result
    make py SCRIPT=python/02-anatomia-minima/mini-error-lab.py --error historial
    make py SCRIPT=python/02-anatomia-minima/mini-error-lab.py --error context_overflow
    make py SCRIPT=python/02-anatomia-minima/mini-error-lab.py --error alucinacion

Qué esperar:
    Cada seccion muestra el error provocado y la version corregida.
    Algunos errores lanzan excepciones deliberadas — el lab las captura.

Variables de entorno:
    MODEL — modelo a usar (default: claude-haiku-4-5-20251001, modelo rapido para demos)
"""

import argparse
import os
import sys

try:
    import anthropic
except ImportError:
    print("Error: pip install anthropic")
    sys.exit(1)

MODEL = os.environ.get("MODEL", "claude-haiku-4-5-20251001")

HERRAMIENTAS = [
    {
        "name": "search_docs",
        "description": "Busca documentación técnica.",
        "input_schema": {
            "type": "object",
            "properties": {"query": {"type": "string"}},
            "required": ["query"],
        },
    }
]


def llamar(client, mensajes, system="Eres un asistente de código.", con_tools=True):
    kwargs = dict(model=MODEL, max_tokens=512, system=system, messages=mensajes)
    if con_tools:
        kwargs["tools"] = HERRAMIENTAS
    resp = client.messages.create(**kwargs)
    tok = resp.usage.input_tokens + resp.usage.output_tokens
    return resp, tok


def demo_tool_use_sin_result(client):
    print("\n  ── Error 1: tool_use sin tool_result ──")
    print("  El modelo llamó a search_docs pero no recibió resultado.")
    print("  Si enviamos el siguiente turno sin el tool_result, la API falla.")

    mensajes = [{"role": "user", "content": "¿Cómo se usa asyncio en Python?"}]
    resp, tok = llamar(client, mensajes)

    if resp.stop_reason == "tool_use":
        tool_block = next((b for b in resp.content if b.type == "tool_use"), None)
        if tool_block:
            print(f"\n  Modelo pidió: {tool_block.name}({tool_block.input})")
            print("\n  ❌ VERSIÓN INCORRECTA: saltar el tool_result:")
            print("     mensajes.append({'role': 'assistant', 'content': [tool_block]})")
            print("     mensajes.append({'role': 'user', 'content': 'Continúa.'})  # ← FALTA tool_result!")

            print("\n  ✓ VERSIÓN CORRECTA: siempre responder con tool_result:")
            mensajes_ok = mensajes + [
                {"role": "assistant", "content": resp.content},
                {"role": "user", "content": [
                    {"type": "tool_result", "tool_use_id": tool_block.id,
                     "content": "asyncio.run() ejecuta corrutinas. Ejemplo: asyncio.run(main())"}
                ]},
            ]
            resp2, tok2 = llamar(client, mensajes_ok)
            print(f"     Respuesta final: {resp2.content[0].text[:100]}...")
            print(f"     Tokens: {tok + tok2}")
    else:
        print(f"  (El modelo respondió directamente sin usar tools — tokens: {tok})")


def demo_historial_malformado(client):
    print("\n  ── Error 2: historial con roles incorrectos ──")
    print("  API de Anthropic requiere alternancia estricta user/assistant.")

    print("\n  ❌ VERSIÓN INCORRECTA — dos mensajes 'user' consecutivos:")
    mensajes_malo = [
        {"role": "user", "content": "¿Qué es Python?"},
        {"role": "user", "content": "Dame un ejemplo."},  # ← Dos user seguidos
    ]
    try:
        client.messages.create(model=MODEL, max_tokens=100, messages=mensajes_malo)
        print("  (Llamada exitosa — este proveedor es más permisivo)")
    except Exception as e:
        print(f"  Error capturado: {str(e)[:80]}")

    print("\n  ✓ VERSIÓN CORRECTA — fusionar mensajes consecutivos del mismo rol:")
    mensajes_ok = [
        {"role": "user", "content": "¿Qué es Python? Dame un ejemplo."},
    ]
    resp, tok = llamar(client, mensajes_ok, con_tools=False)
    print(f"  Respuesta: {resp.content[0].text[:80]}... (tokens: {tok})")


def demo_context_overflow(client):
    print("\n  ── Error 3: context overflow ──")
    print("  Un historial que crece sin compactación hasta superar la ventana.")

    historial = []
    tokens_acumulados = 0
    limite_simulado = 500  # tokens simulados para demo rápida

    for turno in range(5):
        historial.append({"role": "user", "content": f"Turno {turno}: cuéntame un hecho sobre Python."})
        resp, tok = llamar(client, historial, con_tools=False)
        historial.append({"role": "assistant", "content": resp.content[0].text})
        tokens_acumulados += tok
        print(f"  Turno {turno+1}: ~{tokens_acumulados} tokens acumulados")

        if tokens_acumulados > limite_simulado:
            print(f"\n  ⚠️  Umbral de {limite_simulado} tokens superado — aplicando compactación")
            print("  Estrategia: conservar últimos 2 turnos + placeholder")
            historial = historial[-4:]  # últimos 2 pares
            print(f"  Historial reducido a {len(historial)} mensajes")
            break


def demo_alucinacion_herramientas(client):
    print("\n  ── Error 4: el modelo inventa nombres de herramientas ──")
    print("  Si el sistema prompt menciona herramientas que no están definidas,")
    print("  el modelo puede intentar llamarlas y el agente falla silenciosamente.")

    system_con_hallucination = (
        "Eres un agente. Tienes acceso a: search_docs, send_email, delete_file, run_code."
    )
    herramientas_reales = HERRAMIENTAS  # Solo search_docs está definida

    mensajes = [{"role": "user", "content": "Envía un email de resumen a admin@empresa.com"}]
    resp2 = client.messages.create(
        model=MODEL, max_tokens=256,
        system=system_con_hallucination,
        messages=mensajes,
        tools=herramientas_reales,
    )
    tok = resp2.usage.input_tokens + resp2.usage.output_tokens

    print(f"\n  Herramientas disponibles: {[h['name'] for h in herramientas_reales]}")
    print(f"  El modelo intentó: {resp2.stop_reason}")
    if resp2.stop_reason == "tool_use":
        tool = next((b for b in resp2.content if b.type == "tool_use"), None)
        if tool:
            print(f"  Tool invocada: {tool.name}")
            if tool.name not in [h["name"] for h in herramientas_reales]:
                print(f"  ❌ '{tool.name}' no está definida — el dispatcher fallará silenciosamente")
            else:
                print(f"  (El modelo eligió una herramienta real disponible)")

    print(f"\n  ✓ FIX: el system prompt solo debe mencionar herramientas que están en 'tools'")
    print(f"  Tokens: {tok}")


def main():
    parser = argparse.ArgumentParser(description="Demuestra errores comunes en agentes.")
    parser.add_argument("--error",
                        choices=["tool_result", "historial", "context_overflow", "alucinacion", "todos"],
                        default="todos")
    args = parser.parse_args()

    api_key = os.environ.get("ANTHROPIC_API_KEY")
    if not api_key:
        print("Error: ANTHROPIC_API_KEY no configurada")
        sys.exit(1)

    client = anthropic.Anthropic(api_key=api_key)

    print(f"\n{'='*60}")
    print(f"  EL AGENTE QUE SE EQUIVOCA A PROPÓSITO")
    print(f"  Modelo: {MODEL}")
    print(f"{'='*60}")

    errores = {
        "tool_result": demo_tool_use_sin_result,
        "historial": demo_historial_malformado,
        "context_overflow": demo_context_overflow,
        "alucinacion": demo_alucinacion_herramientas,
    }

    a_ejecutar = list(errores.keys()) if args.error == "todos" else [args.error]
    for nombre in a_ejecutar:
        errores[nombre](client)

    print(f"\n{'='*60}")
    print("  Patrones de error cubiertos:")
    print("  1. tool_use sin tool_result → API error 400")
    print("  2. Roles consecutivos iguales → API error o comportamiento errático")
    print("  3. Context overflow sin compactación → fallo en turno N")
    print("  4. Herramientas en prompt sin definirlas → dispatcher fallida silenciosa")
    print(f"{'='*60}")


if __name__ == "__main__":
    main()
