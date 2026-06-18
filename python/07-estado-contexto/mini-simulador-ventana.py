"""Mini-proyecto: El simulador de ventana de contexto.

Construye una conversación ficticia token a token y observa cómo se
comportan las distintas técnicas de compactación: truncación head+tail,
clearing de tool results, y sumarización simulada.

Uso:
    python mini-simulador-ventana.py
    python mini-simulador-ventana.py --ventana 4096
    python mini-simulador-ventana.py --turnos 30 --tecnica clearing
    python mini-simulador-ventana.py --tecnica todos

Cómo ejecutar:
    make py SCRIPT=python/07-estado-contexto/mini-simulador-ventana.py

Qué esperar:
    Simulacion visual de como crece y se compacta el historial.
    Muestra truncacion head+tail, clearing de tool results y sumarizacion.
    No hace llamadas a la API — todo es simulado localmente.
"""

import argparse
import math
import random
import sys
from copy import deepcopy
from textwrap import shorten

# Snapshot precios mayo 2026 — verificar en docs del proveedor
PRECIO_SONNET_INPUT = 3.00  # USD / millón tokens
VENTANA_DEFAULT = 8_192


def estimar_tokens(texto: str) -> int:
    return max(1, len(texto) // 4)


def tokens_mensaje(msg: dict) -> int:
    texto = msg.get("content", "")
    if isinstance(texto, list):
        def _bloque_a_str(b):
            if not isinstance(b, dict):
                return str(b)
            val = b.get("text") or b.get("input") or ""
            return str(val) if not isinstance(val, dict) else str(list(val.values()))
        texto = " ".join(_bloque_a_str(b) for b in texto)
    return estimar_tokens(str(texto)) + 4  # overhead de rol/estructura


def tokens_historial(mensajes: list) -> int:
    return sum(tokens_mensaje(m) for m in mensajes)


# ── generador de conversación ficticia ───────────────────────────────────────

TIPOS_TURNO = [
    ("user_simple", 30),
    ("user_largo", 120),
    ("assistant_texto", 80),
    ("tool_call", 40),
    ("tool_result_corto", 60),
    ("tool_result_largo", 400),
]


def _lorem(n_tokens: int) -> str:
    palabras = ["agente", "contexto", "herramienta", "respuesta", "análisis",
                "código", "función", "resultado", "iteración", "plan",
                "decisión", "estado", "memoria", "búsqueda", "resumen"]
    resultado = []
    while len(" ".join(resultado)) // 4 < n_tokens:
        resultado.append(random.choice(palabras))
    return " ".join(resultado)


def generar_turno(tipo: str) -> dict:
    if tipo == "user_simple":
        return {"role": "user", "content": _lorem(30)}
    if tipo == "user_largo":
        return {"role": "user", "content": _lorem(120)}
    if tipo == "assistant_texto":
        return {"role": "assistant", "content": _lorem(80)}
    if tipo == "tool_call":
        return {
            "role": "assistant",
            "content": [{"type": "tool_use", "id": f"t{random.randint(1000,9999)}",
                          "name": "search_docs", "input": {"query": _lorem(10)}}],
        }
    if tipo == "tool_result_corto":
        return {
            "role": "user",
            "content": [{"type": "tool_result", "tool_use_id": f"t{random.randint(1000,9999)}",
                          "content": _lorem(60)}],
        }
    if tipo == "tool_result_largo":
        return {
            "role": "user",
            "content": [{"type": "tool_result", "tool_use_id": f"t{random.randint(1000,9999)}",
                          "content": _lorem(400)}],
        }
    return {"role": "user", "content": _lorem(30)}


def generar_historial(n_turnos: int) -> list:
    random.seed(42)
    tipos, pesos = zip(*TIPOS_TURNO)
    historial = []
    for _ in range(n_turnos):
        tipo = random.choices(tipos, weights=pesos, k=1)[0]
        historial.append(generar_turno(tipo))
    return historial


# ── técnicas de compactación ──────────────────────────────────────────────────

def es_tool_result(msg: dict) -> bool:
    content = msg.get("content", "")
    if isinstance(content, list):
        return any(isinstance(b, dict) and b.get("type") == "tool_result"
                   for b in content)
    return False


def es_tool_call(msg: dict) -> bool:
    content = msg.get("content", "")
    if isinstance(content, list):
        return any(isinstance(b, dict) and b.get("type") == "tool_use"
                   for b in content)
    return False


def clearing(mensajes: list) -> list:
    """Reemplaza tool results por placeholder, preservando el par tool_use."""
    resultado = []
    for msg in mensajes:
        if es_tool_result(msg):
            nuevo = deepcopy(msg)
            for bloque in nuevo["content"]:
                if isinstance(bloque, dict) and bloque.get("type") == "tool_result":
                    bloque["content"] = "[cleared]"
            resultado.append(nuevo)
        else:
            resultado.append(msg)
    return resultado


def head_tail(mensajes: list, max_tokens: int) -> list:
    """Conserva los primeros N y los últimos M mensajes hasta max_tokens."""
    if tokens_historial(mensajes) <= max_tokens:
        return mensajes
    head = []
    tail = []
    presupuesto = max_tokens
    for msg in mensajes:
        tok = tokens_mensaje(msg)
        if presupuesto >= tok:
            head.append(msg)
            presupuesto -= tok
        else:
            break
    for msg in reversed(mensajes[len(head):]):
        tok = tokens_mensaje(msg)
        if presupuesto >= tok:
            tail.insert(0, msg)
            presupuesto -= tok
        else:
            break
    separador = {"role": "user", "content": f"[... {len(mensajes) - len(head) - len(tail)} mensajes omitidos ...]"}
    return head + [separador] + tail


def sumarizacion_simulada(mensajes: list, max_tokens: int) -> list:
    """Simula sumarización: colapsa mensajes viejos en un bloque de resumen."""
    if tokens_historial(mensajes) <= max_tokens:
        return mensajes
    n_conservar = max(2, len(mensajes) // 3)
    a_resumir = mensajes[:-n_conservar]
    recientes = mensajes[-n_conservar:]
    tokens_resumidos = tokens_historial(a_resumir)
    resumen_tokens = max(20, tokens_resumidos // 5)  # ~80% compresión simulada
    resumen = {
        "role": "user",
        "content": f"[RESUMEN de {len(a_resumir)} mensajes / ~{tokens_resumidos} tokens → {resumen_tokens} tokens]: {_lorem(resumen_tokens)}",
    }
    return [resumen] + recientes


# ── simulación y métricas ─────────────────────────────────────────────────────

def simular(historial_original: list, tecnica: str, ventana: int) -> dict:
    """Recorre el historial turno a turno aplicando la técnica cuando se supera el umbral."""
    umbral = int(ventana * 0.85)
    historial = []
    compactaciones = 0
    tokens_enviados_total = 0
    tokens_ahorrados_total = 0
    desbordamientos = 0

    for msg in historial_original:
        historial.append(msg)
        tok_actual = tokens_historial(historial)

        if tok_actual > umbral:
            tok_antes = tok_actual
            if tecnica == "clearing":
                historial = clearing(historial)
            elif tecnica == "head_tail":
                historial = head_tail(historial, umbral)
            elif tecnica == "sumarizacion":
                historial = sumarizacion_simulada(historial, umbral)
            elif tecnica == "ninguna":
                pass
            tok_despues = tokens_historial(historial)
            compactaciones += 1
            tokens_ahorrados_total += max(0, tok_antes - tok_despues)

        tok_final = tokens_historial(historial)
        tokens_enviados_total += tok_final
        if tok_final > ventana:
            desbordamientos += 1

    return {
        "tecnica": tecnica,
        "tokens_enviados": tokens_enviados_total,
        "compactaciones": compactaciones,
        "tokens_ahorrados": tokens_ahorrados_total,
        "desbordamientos": desbordamientos,
        "tokens_final": tokens_historial(historial),
        "costo_usd": tokens_enviados_total * PRECIO_SONNET_INPUT / 1_000_000,
    }


# ── presentación ──────────────────────────────────────────────────────────────

def barra(valor: int, maximo: int, ancho: int = 30) -> str:
    lleno = int(valor / maximo * ancho) if maximo else 0
    return "█" * lleno + "░" * (ancho - lleno)


def imprimir_resultados(resultados: list, n_turnos: int, ventana: int) -> None:
    print(f"\n{'='*66}")
    print(f"  SIMULADOR DE VENTANA DE CONTEXTO")
    print(f"  {n_turnos} turnos  |  ventana {ventana:,} tokens  |  precios sonnet mayo 2026")
    print(f"{'='*66}")

    # máximos para barras relativas
    max_tok = max(r["tokens_enviados"] for r in resultados)
    max_cost = max(r["costo_usd"] for r in resultados)

    print(f"\n{'Técnica':<16} {'Tokens env.':>11} {'Compact.':>9} {'Ahorr. tok':>11} {'Desbord.':>9} {'USD':>8}")
    print("-" * 66)
    for r in resultados:
        print(
            f"{r['tecnica']:<16} {r['tokens_enviados']:>11,} {r['compactaciones']:>9} "
            f"{r['tokens_ahorrados']:>11,} {r['desbordamientos']:>9} ${r['costo_usd']:>7.4f}"
        )

    print(f"\n{'─'*66}")
    print("  Tokens enviados (barra relativa al máximo)")
    print(f"{'─'*66}")
    for r in resultados:
        bar = barra(r["tokens_enviados"], max_tok)
        print(f"  {r['tecnica']:<14} {bar}  {r['tokens_enviados']:,}")

    print(f"\n{'─'*66}")
    print("  Costo USD (barra relativa al máximo)")
    print(f"{'─'*66}")
    base = next((r for r in resultados if r["tecnica"] == "ninguna"), None)
    for r in resultados:
        bar = barra(r["costo_usd"], max_cost)
        ahorro_str = ""
        if base and r["tecnica"] != "ninguna":
            ahorro = (1 - r["costo_usd"] / base["costo_usd"]) * 100
            ahorro_str = f"  ({ahorro:+.1f}% vs sin compactación)"
        print(f"  {r['tecnica']:<14} {bar}  ${r['costo_usd']:.4f}{ahorro_str}")

    print(f"\n[Estimación ±10% — conteo exacto con tiktoken]")
    print(f"[Snapshot precios mayo 2026 — verificar en docs del proveedor]")


# ── main ──────────────────────────────────────────────────────────────────────

def main():
    parser = argparse.ArgumentParser(
        description="Simula técnicas de compactación de ventana de contexto."
    )
    parser.add_argument("--ventana", type=int, default=VENTANA_DEFAULT,
                        help=f"Tokens de ventana (default: {VENTANA_DEFAULT})")
    parser.add_argument("--turnos", type=int, default=40,
                        help="Número de turnos de conversación a simular (default: 40)")
    parser.add_argument(
        "--tecnica",
        choices=["clearing", "head_tail", "sumarizacion", "ninguna", "todos"],
        default="todos",
        help="Técnica a comparar (default: todos)",
    )
    args = parser.parse_args()

    historial = generar_historial(args.turnos)
    tokens_bruto = tokens_historial(historial)

    print(f"\n[Historial generado: {args.turnos} turnos, {tokens_bruto:,} tokens bruto]")
    print(f"[Ventana configurada: {args.ventana:,} tokens  |  umbral de compactación: {int(args.ventana*0.85):,}]")

    tecnicas = (
        ["ninguna", "clearing", "head_tail", "sumarizacion"]
        if args.tecnica == "todos"
        else [args.tecnica]
    )

    resultados = [simular(list(historial), t, args.ventana) for t in tecnicas]
    imprimir_resultados(resultados, args.turnos, args.ventana)


if __name__ == "__main__":
    main()
