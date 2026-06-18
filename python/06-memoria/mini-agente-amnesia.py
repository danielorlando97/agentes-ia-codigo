"""Mini-proyecto: El agente amnésico vs el agente con memoria.

Compara el comportamiento de dos agentes ante la misma conversación:
uno sin ningún tipo de memoria y otro con memoria episódica simple.
Demuestra cuándo la falta de memoria produce inconsistencias.

Requiere: ANTHROPIC_API_KEY

Uso:
    python mini-agente-amnesia.py
    python mini-agente-amnesia.py --turnos 6
    python mini-agente-amnesia.py --modo amnésico

Cómo ejecutar:
    make py SCRIPT=python/06-memoria/mini-agente-amnesia.py

Qué esperar:
    Dos conversaciones paralelas con la misma secuencia de mensajes.
    El agente amnésico olvida el nombre del usuario entre turnos.
    El agente con memoria lo recuerda y mantiene coherencia.

Variables de entorno:
    MODEL — modelo a usar (default: claude-sonnet-4-6)
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

SYSTEM = "Eres un asistente de soporte técnico. Ayuda al usuario con sus preguntas."

# Conversación donde la falta de memoria produce inconsistencias obvias
TURNOS = [
    "Hola, me llamo Carlos y tengo un problema con mi laptop HP modelo Pavilion.",
    "¿Cuál es el número de serie para registrar la garantía? El número está en la etiqueta debajo.",
    "OK, el número de serie es SN-2024-XY789. ¿Qué hago ahora?",
    "Por cierto, ¿recuerdas cómo me llamo y qué modelo de laptop tengo?",
    "Bien. Y el número de serie que te di, ¿lo recuerdas?",
    "Resumame el problema y los datos que te di hasta ahora.",
]


def agente_amnesico(client, turnos: list) -> None:
    print("\n  ── Agente AMNÉSICO (sin historial) ──")
    print("  Cada turno se envía sin contexto previo.\n")
    tokens_total = 0

    for i, turno in enumerate(turnos):
        # El agente amnésico solo ve el mensaje actual — sin historial
        resp = client.messages.create(
            model=MODEL, max_tokens=256,
            system=SYSTEM,
            messages=[{"role": "user", "content": turno}],
        )
        tokens_total += resp.usage.input_tokens + resp.usage.output_tokens
        respuesta = resp.content[0].text.strip()

        print(f"  [{i+1}] Usuario: {turno}")
        print(f"       Agente:  {respuesta[:120]}{'...' if len(respuesta) > 120 else ''}")
        print()

    print(f"  Tokens totales: {tokens_total}")
    print(f"  Problema: el agente no recuerda el nombre, modelo ni número de serie.")
    print(f"  En el turno 4 y 5 inventa o no responde correctamente.")


def agente_con_memoria(client, turnos: list) -> None:
    print("\n  ── Agente CON MEMORIA (historial completo) ──")
    print("  El historial completo se envía en cada turno.\n")
    mensajes = []
    tokens_total = 0

    for i, turno in enumerate(turnos):
        mensajes.append({"role": "user", "content": turno})
        resp = client.messages.create(
            model=MODEL, max_tokens=256,
            system=SYSTEM,
            messages=mensajes,
        )
        tokens_total += resp.usage.input_tokens + resp.usage.output_tokens
        respuesta = resp.content[0].text.strip()
        mensajes.append({"role": "assistant", "content": respuesta})

        print(f"  [{i+1}] Usuario: {turno}")
        print(f"       Agente:  {respuesta[:120]}{'...' if len(respuesta) > 120 else ''}")
        print(f"       [contexto: ~{sum(len(m['content']) for m in mensajes) // 4} tokens]")
        print()

    print(f"  Tokens totales: {tokens_total} (crece con cada turno)")
    print(f"  Ventaja: el agente recuerda todo. Desventaja: el costo crece linealmente.")


def agente_memoria_episodica(client, turnos: list) -> None:
    print("\n  ── Agente con MEMORIA EPISÓDICA (resumen + datos clave) ──")
    print("  Extrae entidades clave del historial para no cargar todo.\n")
    mensajes = []
    memoria: dict = {}
    tokens_total = 0

    for i, turno in enumerate(turnos):
        # Inyectar memoria como contexto adicional
        contexto_memoria = ""
        if memoria:
            items = [f"{k}: {v}" for k, v in memoria.items()]
            contexto_memoria = f"\n[MEMORIA DEL USUARIO: {', '.join(items)}]\n"

        prompt_completo = contexto_memoria + turno
        mensajes_cortos = mensajes[-4:] + [{"role": "user", "content": prompt_completo}]

        resp = client.messages.create(
            model=MODEL, max_tokens=256,
            system=SYSTEM,
            messages=mensajes_cortos,
        )
        tokens_total += resp.usage.input_tokens + resp.usage.output_tokens
        respuesta = resp.content[0].text.strip()
        mensajes.append({"role": "user", "content": turno})
        mensajes.append({"role": "assistant", "content": respuesta})

        # Extracción simple de entidades (en producción usarías un LLM o regex)
        if "Carlos" in turno or "me llamo" in turno.lower():
            memoria["nombre"] = "Carlos"
        if "HP" in turno or "Pavilion" in turno:
            memoria["laptop"] = "HP Pavilion"
        if "SN-" in turno:
            import re
            match = re.search(r"SN-[\w-]+", turno)
            if match:
                memoria["serie"] = match.group()

        print(f"  [{i+1}] Usuario: {turno}")
        print(f"       Agente:  {respuesta[:120]}{'...' if len(respuesta) > 120 else ''}")
        if memoria:
            print(f"       [memoria: {memoria}]")
        print()

    print(f"  Tokens totales: {tokens_total}")
    print(f"  La memoria episódica mantiene datos clave sin cargar todo el historial.")


def main():
    parser = argparse.ArgumentParser(description="Agente amnésico vs agente con memoria.")
    parser.add_argument("--modo", choices=["amnesico", "memoria", "episodica", "todos"], default="todos")
    parser.add_argument("--turnos", type=int, default=len(TURNOS))
    args = parser.parse_args()

    api_key = os.environ.get("ANTHROPIC_API_KEY")
    if not api_key:
        print("Error: ANTHROPIC_API_KEY no configurada")
        sys.exit(1)

    client = anthropic.Anthropic(api_key=api_key)
    turnos = TURNOS[:args.turnos]

    print(f"\n{'='*64}")
    print(f"  AGENTE AMNÉSICO vs AGENTE CON MEMORIA")
    print(f"  Modelo: {MODEL}  |  Turnos: {len(turnos)}")
    print(f"{'='*64}")

    modos = {
        "amnesico": agente_amnesico,
        "memoria": agente_con_memoria,
        "episodica": agente_memoria_episodica,
    }
    a_ejecutar = list(modos.keys()) if args.modo == "todos" else [args.modo]
    for nombre in a_ejecutar:
        modos[nombre](client, turnos)

    print(f"\n{'='*64}")
    print("  Comparativa:")
    print("  Amnésico:  0 tokens de contexto extra — inconsistente en turno 4+")
    print("  Historial: tokens crecen O(n) — consistente pero costoso en sesiones largas")
    print("  Episódica: tokens casi constantes — consistente en datos clave")
    print(f"{'='*64}")


if __name__ == "__main__":
    main()
