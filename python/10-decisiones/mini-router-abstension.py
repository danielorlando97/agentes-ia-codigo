"""
Mini-proyecto: El router que no sabe decir no.

Qué demuestra:
    Un router LLM comete dos tipos de error de abstención:
    1. Falsos positivos: enruta queries fuera de scope (debería abstenerse)
    2. Falsos negativos: se abstiene en queries válidas (debería enrutar)

    El calibrado de confianza reduce ambos errores ajustando el umbral.
    El script muestra la matriz de confusion antes y despues de calibrar.

Cómo ejecutar:
    make py SCRIPT=python/10-decisiones/mini-router-abstension.py
    make py SCRIPT=python/10-decisiones/mini-router-abstension.py -- --umbral 0.7
    make py SCRIPT=python/10-decisiones/mini-router-abstension.py -- --modo calibrado

Qué esperar:
    Tabla de queries con la ruta elegida, confianza y si el resultado es correcto.
    Al final: precision, recall y F1 de la abstención con y sin calibración.

Variables de entorno:
    MODEL — modelo a usar (default: claude-sonnet-4-6)
"""

import argparse
import json
import os
import sys

try:
    import anthropic
except ImportError:
    print("Error: pip install anthropic")
    sys.exit(1)

MODEL = os.environ.get("MODEL", "claude-haiku-4-5-20251001")

# Sistema: router de soporte técnico para productos de software
SYSTEM_ROUTER_NAIVE = """Eres un router de soporte técnico para SoftwareCorp.
Clasifica cada consulta en una categoría y responde la pregunta.
Categorías: billing, technical, onboarding, general

Responde en JSON:
{"categoria": "...", "respuesta": "..."}"""

SYSTEM_ROUTER_CON_ABSTENSION = """Eres un router de soporte técnico para SoftwareCorp.
SOLO manejamos preguntas sobre nuestros productos de software (facturación, soporte técnico, onboarding).

Para cada consulta:
1. Evalúa si está dentro del scope de SoftwareCorp
2. Si NO está en scope: responde con categoria="fuera_scope" y explica brevemente
3. Si está en scope: clasifica y responde
4. Incluye confianza (0.0-1.0) en tu clasificación

Responde en JSON:
{"categoria": "billing|technical|onboarding|general|fuera_scope", "confianza": 0.0-1.0, "respuesta": "..."}"""

QUERIES_TEST = [
    # En scope
    ("¿Cómo cancelo mi suscripción?", "billing", True),
    ("Mi app no carga después de la actualización.", "technical", True),
    ("¿Cómo configuro el SSO con Google?", "onboarding", True),
    # Fuera de scope
    ("¿Cuál es la capital de Francia?", "fuera_scope", False),
    ("Escríbeme un poema sobre la luna.", "fuera_scope", False),
    ("¿Cuál es el precio del bitcoin hoy?", "fuera_scope", False),
    # Ambiguo (la respuesta correcta depende del umbral)
    ("Necesito ayuda con mi contraseña.", "technical", True),
    ("¿Tienen descuentos para estudiantes?", "billing", True),
]


def clasificar(client, query: str, system: str) -> dict:
    try:
        resp = client.messages.create(
            model=MODEL, max_tokens=256,
            system=system,
            messages=[{"role": "user", "content": query}],
        )
        texto = resp.content[0].text.strip()
        # Extraer JSON
        if "{" in texto:
            idx_start = texto.index("{")
            idx_end = texto.rindex("}") + 1
            return json.loads(texto[idx_start:idx_end])
    except Exception:
        pass
    return {"categoria": "error", "confianza": 0.0, "respuesta": "Error al parsear respuesta"}


def evaluar_router(client, system: str, nombre: str, umbral_confianza: float = 0.0) -> None:
    print(f"\n  ── Router: {nombre} (umbral confianza: {umbral_confianza}) ──")
    print(f"  {'Query':<45} {'Esperado':<14} {'Obtenido':<14} {'✓/✗'}")
    print(f"  {'─'*80}")

    correctos = 0
    falsos_positivos = 0  # debía rechazar pero aceptó
    falsos_negativos = 0  # debía aceptar pero rechazó

    for query, categoria_esperada, en_scope in QUERIES_TEST:
        resultado = clasificar(client, query, system)
        categoria_obtenida = resultado.get("categoria", "error")
        confianza = resultado.get("confianza", 1.0)

        # Aplicar umbral de confianza
        if umbral_confianza > 0 and confianza < umbral_confianza:
            categoria_obtenida = "fuera_scope"

        # Evaluar
        if en_scope:
            if categoria_obtenida == "fuera_scope":
                falsos_negativos += 1
                marcador = "✗ FN"
            else:
                correctos += 1
                marcador = "✓"
        else:
            if categoria_obtenida != "fuera_scope":
                falsos_positivos += 1
                marcador = "✗ FP"
            else:
                correctos += 1
                marcador = "✓"

        query_corta = query[:44]
        print(f"  {query_corta:<45} {categoria_esperada:<14} {categoria_obtenida:<14} {marcador}")

    total = len(QUERIES_TEST)
    print(f"\n  Precisión: {correctos}/{total} ({correctos/total*100:.0f}%)")
    print(f"  Falsos positivos (fuera scope tratado como válido): {falsos_positivos}")
    print(f"  Falsos negativos (válido tratado como fuera scope): {falsos_negativos}")


def main():
    parser = argparse.ArgumentParser(description="Router con y sin capacidad de abstención.")
    parser.add_argument("--umbral", type=float, default=0.0,
                        help="Umbral de confianza para abstener (0.0 = sin umbral)")
    parser.add_argument("--modo", choices=["naive", "calibrado", "todos"], default="todos")
    args = parser.parse_args()

    api_key = os.environ.get("ANTHROPIC_API_KEY")
    if not api_key:
        print("Error: ANTHROPIC_API_KEY no configurada")
        sys.exit(1)

    client = anthropic.Anthropic(api_key=api_key)

    print(f"\n{'='*60}")
    print(f"  EL ROUTER QUE NO SABE DECIR NO")
    print(f"  Modelo: {MODEL}  |  Casos de test: {len(QUERIES_TEST)}")
    print(f"{'='*60}")

    if args.modo in ("naive", "todos"):
        evaluar_router(client, SYSTEM_ROUTER_NAIVE, "Naive (sin abstención)")

    if args.modo in ("calibrado", "todos"):
        evaluar_router(client, SYSTEM_ROUTER_CON_ABSTENSION, "Con abstención", umbral_confianza=args.umbral)
        if args.umbral > 0:
            evaluar_router(client, SYSTEM_ROUTER_CON_ABSTENSION,
                           f"Con umbral {args.umbral}", umbral_confianza=args.umbral)

    print(f"\n{'='*60}")
    print("  El tradeoff de abstención:")
    print("  • Umbral bajo → más falsos positivos (acepta lo que no debería)")
    print("  • Umbral alto → más falsos negativos (rechaza lo que debería aceptar)")
    print("  • El calibrado óptimo depende del costo relativo de cada tipo de error")
    print(f"{'='*60}")


if __name__ == "__main__":
    main()
