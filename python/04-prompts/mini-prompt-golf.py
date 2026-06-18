"""
Mini-proyecto: Prompt golf — optimizar tokens sin sacrificar calidad.

Qué demuestra:
    Reducir tokens del system prompt ahorra dinero en produccion.
    Pero un prompt demasiado compacto puede degradar la calidad.
    Este script mide la relacion tokens/calidad en cada iteracion de optimizacion.
    Compara el prompt original (verbose) vs versiones progressivamente compactas.

Metodo:
    1. Envia el prompt verbose y mide calidad en un conjunto de casos de prueba
    2. El modelo propone una version mas compacta
    3. Mide la calidad de la version compacta
    4. Repite hasta MAX_RONDAS o hasta que la calidad cae por debajo del umbral

Cómo ejecutar:
    make py SCRIPT=python/04-prompts/mini-prompt-golf.py
    make py SCRIPT=python/04-prompts/mini-prompt-golf.py -- --rondas 5
    make py SCRIPT=python/04-prompts/mini-prompt-golf.py -- --tarea clasificacion

Qué esperar:
    Tabla de iteraciones con tokens, calidad y ahorro acumulado.
    El proceso se detiene cuando la calidad cae o se alcanza el maximo de rondas.

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

# ── prompts para comparar ──────────────────────────────────────────────────────

PROMPTS = {
    "verbose": """\
Eres un asistente de clasificación de emails especializado en soporte técnico.
Tu tarea principal es analizar el email que se te proporcione y clasificarlo
en una de las siguientes categorías disponibles:

1. billing - Para todo lo relacionado con facturación, pagos, suscripciones,
   reembolsos, problemas de cargos, facturas, métodos de pago, etc.
2. technical - Para problemas técnicos, errores, bugs, problemas de instalación,
   problemas de rendimiento, compatibilidad, etc.
3. general - Para preguntas generales, consultas de información, preguntas sobre
   características del producto, etc.

INSTRUCCIONES IMPORTANTES:
- Debes leer cuidadosamente el email completo antes de clasificar
- Elige siempre UNA sola categoría que mejor describa el problema principal
- Responde ÚNICAMENTE con la categoría en minúsculas (billing, technical o general)
- No incluyas explicaciones, puntuación adicional ni ningún otro texto
- Si hay duda entre categorías, elige la más específica
""",
    "compacto": """\
Clasifica el email en: billing, technical o general.
Responde solo con la categoría en minúsculas.
""",
    "medio": """\
Clasifica emails de soporte. Categorías: billing (facturación/pagos), technical (errores/bugs), general (info/preguntas).
Responde solo con la categoría en minúsculas.
""",
}

EMAILS_TEST = [
    ("Mi tarjeta fue cobrada dos veces el mes pasado.", "billing"),
    ("La aplicación se cierra sola al abrir el menú.", "technical"),
    ("¿Cuántos usuarios puedo tener en el plan básico?", "general"),
    ("No puedo iniciar sesión y mi contraseña es correcta.", "technical"),
    ("¿Puedo pagar con transferencia bancaria?", "billing"),
]


def estimar_tokens(texto: str) -> int:
    return max(1, len(texto) // 4)


def evaluar_prompt(client, nombre_prompt: str, system: str, emails: list) -> dict:
    correctas = 0
    tokens_total = 0

    for email, esperado in emails:
        resp = client.messages.create(
            model=MODEL, max_tokens=20,
            system=system,
            messages=[{"role": "user", "content": f"Email: {email}"}],
        )
        respuesta = resp.content[0].text.strip().lower()
        tok = resp.usage.input_tokens + resp.usage.output_tokens
        tokens_total += tok
        if respuesta == esperado:
            correctas += 1

    tokens_system = estimar_tokens(system)
    return {
        "nombre": nombre_prompt,
        "tokens_system": tokens_system,
        "tokens_total": tokens_total,
        "precision": correctas / len(emails) * 100,
        "correctas": correctas,
        "total": len(emails),
        "costo_usd": tokens_total * 0.80 / 1_000_000,
    }


def main():
    parser = argparse.ArgumentParser(description="Prompt golf — optimiza tokens sin perder calidad.")
    parser.add_argument("--rondas", type=int, default=1, help="Repeticiones de la evaluación")
    args = parser.parse_args()

    api_key = os.environ.get("ANTHROPIC_API_KEY")
    if not api_key:
        print("Error: ANTHROPIC_API_KEY no configurada")
        sys.exit(1)

    client = anthropic.Anthropic(api_key=api_key)

    print(f"\n{'='*60}")
    print(f"  PROMPT GOLF — Optimización de tokens")
    print(f"  Modelo: {MODEL}  |  Casos de test: {len(EMAILS_TEST)}")
    print(f"{'='*60}")

    resultados = []
    for nombre, system in PROMPTS.items():
        print(f"\n  Evaluando: {nombre} (~{estimar_tokens(system)} tokens de system)...")
        res = evaluar_prompt(client, nombre, system, EMAILS_TEST)
        resultados.append(res)

    print(f"\n{'─'*60}")
    print(f"  {'Variante':<12} {'Tokens sys':>10} {'Tokens total':>12} {'Precisión':>10} {'Costo':>10}")
    print(f"  {'─'*56}")
    for r in resultados:
        print(f"  {r['nombre']:<12} {r['tokens_system']:>10} {r['tokens_total']:>12} "
              f"{r['precision']:>9.0f}%  ${r['costo_usd']:>9.6f}")

    base = resultados[0]
    print(f"\n  Ahorro al pasar de verbose a compacto:")
    for r in resultados[1:]:
        ahorro_sys = (1 - r['tokens_system'] / base['tokens_system']) * 100
        ahorro_total = (1 - r['tokens_total'] / base['tokens_total']) * 100
        delta_prec = r['precision'] - base['precision']
        print(f"  → {r['nombre']}: {ahorro_sys:.0f}% menos tokens de system | "
              f"{ahorro_total:.0f}% menos tokens totales | "
              f"precisión: {delta_prec:+.0f}pp")

    print(f"\n  Regla del prompt golf:")
    print(f"  • Cada token del system prompt se paga en CADA request")
    print(f"  • Con {10000} req/día, la diferencia de {base['tokens_system'] - resultados[-1]['tokens_system']} "
          f"tokens = ${(base['tokens_system'] - resultados[-1]['tokens_system']) * 0.80 / 1e6 * 10000:.2f}/día")
    print(f"{'='*60}")


if __name__ == "__main__":
    main()
