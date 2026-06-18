"""Mini-proyecto: El debate de 3.

Tres agentes con perspectivas diferentes debaten una propuesta técnica.
Al final, un juez sintetiza el consenso. Observa cómo los agentes
construyen sobre los argumentos de los otros y cuándo convergen.

Requiere: ANTHROPIC_API_KEY

Uso:
    python mini-debate-3.py
    python mini-debate-3.py --rondas 2
    python mini-debate-3.py --propuesta "Usar microservicios vs monolito"

Cómo ejecutar:
    make py SCRIPT=python/12-multi-agente/mini-debate-3.py

Qué esperar:
    Tres agentes con perspectivas distintas (pragmático, visionario, escéptico)
    debaten una propuesta técnica. El juez sintetiza el consenso al final.

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

PROPUESTA_DEFAULT = "Migrar el backend de Python a TypeScript para mejorar la mantenibilidad del equipo"

ROLES = {
    "optimista": {
        "system": (
            "Eres el Arquitecto Optimista en un panel técnico. Tu rol es defender los beneficios "
            "de la propuesta presentada. Argumentas con datos concretos, ejemplos reales y ROI. "
            "Sé conciso (3-4 frases). Puedes responder a objeciones específicas de los otros panelistas."
        ),
        "emoji": "🟢",
    },
    "escéptico": {
        "system": (
            "Eres el Ingeniero Escéptico en un panel técnico. Tu rol es identificar riesgos, "
            "costos ocultos y casos donde la propuesta podría fallar. "
            "Sé conciso (3-4 frases). No rechaces la propuesta en su totalidad — señala condiciones "
            "bajo las cuales sí tendría sentido."
        ),
        "emoji": "🔴",
    },
    "pragmatico": {
        "system": (
            "Eres el Lead Engineer Pragmático en un panel técnico. Tu rol es evaluar la propuesta "
            "desde la perspectiva de implementación real: timeline, recursos, migration path. "
            "Propones variantes o fases que reduzcan el riesgo. Sé conciso (3-4 frases)."
        ),
        "emoji": "🟡",
    },
}


def turno_agente(client, rol: str, propuesta: str, historial_debate: list) -> str:
    config = ROLES[rol]
    contexto = f"Propuesta: {propuesta}\n\n"
    if historial_debate:
        contexto += "Debate hasta ahora:\n"
        for entrada in historial_debate[-6:]:  # últimos 6 turnos
            contexto += f"{entrada['rol'].upper()}: {entrada['argumento']}\n"
    contexto += f"\nTu turno como {rol.upper()}:"

    resp = client.messages.create(
        model=MODEL, max_tokens=256,
        system=config["system"],
        messages=[{"role": "user", "content": contexto}],
    )
    return resp.content[0].text.strip()


def sintetizar_debate(client, propuesta: str, historial: list) -> str:
    system_juez = (
        "Eres un juez técnico imparcial. Has observado un debate sobre una propuesta. "
        "Tu tarea: sintetiza los puntos clave de acuerdo y desacuerdo, "
        "y produce una recomendación concreta (sí/no/condicional) con las condiciones específicas. "
        "Sé objetivo y basa la recomendación en los argumentos más sólidos del debate."
    )
    resumen_debate = "\n".join(
        f"{e['rol'].upper()}: {e['argumento']}"
        for e in historial
    )
    resp = client.messages.create(
        model=MODEL, max_tokens=512,
        system=system_juez,
        messages=[{"role": "user", "content": f"Propuesta: {propuesta}\n\nDebate:\n{resumen_debate}"}],
    )
    return resp.content[0].text.strip()


def main():
    parser = argparse.ArgumentParser(description="Debate multi-agente de 3 perspectivas.")
    parser.add_argument("--rondas", type=int, default=2, help="Rondas de debate (default: 2)")
    parser.add_argument("--propuesta", default=PROPUESTA_DEFAULT)
    args = parser.parse_args()

    api_key = os.environ.get("ANTHROPIC_API_KEY")
    if not api_key:
        print("Error: ANTHROPIC_API_KEY no configurada")
        sys.exit(1)

    client = anthropic.Anthropic(api_key=api_key)

    print(f"\n{'='*64}")
    print(f"  DEBATE DE 3 AGENTES")
    print(f"  Modelo: {MODEL}  |  Rondas: {args.rondas}")
    print(f"{'='*64}")
    print(f"\n  Propuesta: {args.propuesta}")

    historial = []
    orden_roles = ["optimista", "escéptico", "pragmatico"]

    for ronda in range(args.rondas):
        print(f"\n  ── Ronda {ronda + 1} ──")
        for rol in orden_roles:
            config = ROLES[rol]
            print(f"\n  {config['emoji']} {rol.upper()}:")
            argumento = turno_agente(client, rol, args.propuesta, historial)
            print(f"  {argumento}")
            historial.append({"rol": rol, "argumento": argumento, "ronda": ronda + 1})

    print(f"\n{'─'*64}")
    print(f"  ⚖️  SÍNTESIS DEL JUEZ:")
    sintesis = sintetizar_debate(client, args.propuesta, historial)
    print(f"\n  {sintesis}")

    print(f"\n{'='*64}")
    print(f"  Estadísticas del debate:")
    print(f"  • {len(historial)} intervenciones ({args.rondas} rondas × 3 agentes)")
    print(f"  • Tokens estimados: ~{len(historial) * 300} (sin contar síntesis)")
    print(f"  • Patrón: perspectivas divergentes → síntesis convergente")
    print(f"{'='*64}")


if __name__ == "__main__":
    main()
