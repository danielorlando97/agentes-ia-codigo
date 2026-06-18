"""
Mini-costometro — calcula el costo real de tu system prompt.

Qué demuestra:
    Herramienta practica para estimar cuanto costara un agente en produccion
    antes de desplegarlo. Dado un system prompt, calcula:
    - Tokens del system prompt y overhead por request
    - Costo por sesion (con diferentes modelos: haiku/sonnet/opus)
    - Costo mensual proyectado segun numero de sesiones
    - Presupuesto restante para tools y respuesta del modelo

Por qué importa antes de desplegar:
    Un system prompt de 2000 tokens x 100k sesiones/mes = 200M tokens de input.
    Con Sonnet: 200M x $3/M = $600/mes SOLO en el system prompt.
    Con Haiku: 200M x $0.80/M = $160/mes — 3.75x mas barato.
    Esta herramienta hace visible esa diferencia antes de elegir el modelo.

Cómo ejecutar:
    make py SCRIPT=python/03-motor-llm/mini-costometro.py
    make py SCRIPT=python/03-motor-llm/mini-costometro.py -- --sesiones 5000
    make py SCRIPT=python/03-motor-llm/mini-costometro.py -- --prompt mi_prompt.txt

Qué esperar:
    Tabla comparativa de modelos con costo por sesion y mensual,
    desglose del presupuesto de tokens por request.
    No hace llamadas a la API — todo es calculo local.
"""

import argparse
import re
import sys

# Snapshot de precios: mayo 2026 — verificar en docs del proveedor
PRECIOS = {
    "haiku":  {"input": 0.80,  "output": 4.00},
    "sonnet": {"input": 3.00,  "output": 15.00},
    "opus":   {"input": 15.00, "output": 75.00},
}
VENTANAS = {"haiku": 200_000, "sonnet": 200_000, "opus": 200_000}

PROMPT_EJEMPLO = """\
Eres un agente de revisión de código Python. Tu trabajo es analizar el
código que te envíen y producir un informe estructurado en JSON.

REGLAS:
1. Responde SIEMPRE en JSON con el schema exacto indicado abajo.
2. Clasifica hallazgos por severidad: critical, high, medium, low.
3. No expliques el código; solo reporta problemas concretos.
4. Si no hay bugs, devuelve hallazgos = [].

SCHEMA:
{
  "hallazgos": [
    {
      "linea": <int|null>,
      "severidad": "<critical|high|medium|low>",
      "tipo": "<bug|estilo|rendimiento|seguridad>",
      "descripcion": "<qué está mal>",
      "sugerencia": "<cómo corregirlo>"
    }
  ],
  "resumen": "<un párrafo de resumen>"
}

GUÍAS DE ESTILO DEL EQUIPO:
- PEP 8 obligatorio
- Type hints en todas las funciones públicas
- Docstrings en clases y métodos públicos
- Cobertura de tests mínima: 80%
- No usar print() en producción, usar logging
"""


def tokenizar(texto: str) -> int:
    """Cuenta tokens. Usa tiktoken si está instalado; si no, estima ±10%."""
    try:
        import tiktoken
        enc = tiktoken.get_encoding("cl100k_base")
        return len(enc.encode(texto))
    except ImportError:
        return max(1, len(texto) // 4)


def analizar_secciones(prompt: str) -> list[dict]:
    bloques = [b.strip() for b in re.split(r"\n{2,}", prompt) if b.strip()]
    return [
        {
            "label": bloque.split("\n")[0][:45],
            "tokens": tokenizar(bloque),
            "chars": len(bloque),
        }
        for bloque in bloques
    ]


def calcular_coste(tokens: int, modelo: str, tipo: str = "input") -> float:
    return tokens * PRECIOS[modelo][tipo] / 1_000_000


def imprimir_tabla_modelos(tokens_prompt: int, max_tokens_output: int, sesiones: int) -> None:
    print(f"\n{'Modelo':<10} {'Tokens input':>13} {'USD/req':>9} {'USD/día':>10} "
          f"{'Budget resp (tok)':>18} {'% ventana':>10}")
    print("-" * 75)
    for modelo in ["haiku", "sonnet", "opus"]:
        costo_req = calcular_coste(tokens_prompt, modelo, "input")
        costo_dia = costo_req * sesiones
        ventana = VENTANAS[modelo]
        budget = ventana - tokens_prompt - max_tokens_output
        budget_str = f"{budget:,}" if budget > 0 else f"OVERFLOW -{-budget:,}"
        pct = tokens_prompt / ventana * 100
        print(f"{modelo:<10} {tokens_prompt:>13,} ${costo_req:>8.5f} ${costo_dia:>9.4f} "
              f"{budget_str:>18} {pct:>9.1f}%")


def main():
    parser = argparse.ArgumentParser(description="Analiza tokens y coste de un system prompt.")
    parser.add_argument("--prompt", help="Archivo .txt con el system prompt")
    parser.add_argument("--sesiones", type=int, default=1000,
                        help="Sesiones/día para proyección (default: 1000)")
    parser.add_argument("--max-tokens", type=int, default=4096,
                        help="Tokens de output reservados (default: 4096)")
    args = parser.parse_args()

    if args.prompt:
        try:
            prompt = open(args.prompt).read()
        except FileNotFoundError:
            print(f"Error: no se encontró '{args.prompt}'")
            sys.exit(1)
    else:
        prompt = PROMPT_EJEMPLO
        print("[Usando prompt de ejemplo — pasa --prompt archivo.txt para usar el tuyo]\n")

    tokens_total = tokenizar(prompt)
    secciones = analizar_secciones(prompt)

    print("=" * 60)
    print("EL COSTÓMETRO — Análisis de system prompt")
    print("=" * 60)
    print(f"\nPrompt: {len(prompt):,} chars  |  ~{tokens_total:,} tokens")
    print(f"Proyección: {args.sesiones:,} sesiones/día  |  {args.max_tokens:,} tokens output reservados")

    print(f"\n{'Sección (primera línea)':<43} {'Tokens':>7} {'%':>5}")
    print("-" * 58)
    for sec in secciones:
        pct = sec["tokens"] / tokens_total * 100
        label = sec["label"][:41] + ".." if len(sec["label"]) > 43 else sec["label"]
        print(f"{label:<43} {sec['tokens']:>7,} {pct:>4.1f}%")
    print("-" * 58)
    print(f"{'TOTAL':<43} {tokens_total:>7,} {'100.0':>4}%")

    print(f"\n--- Coste por modelo ({args.sesiones:,} sesiones/día) ---")
    imprimir_tabla_modelos(tokens_total, args.max_tokens, args.sesiones)

    print(f"\n--- Efecto de truncar el prompt ---")
    print(f"\n{'% prompt':<12} {'Tokens':>8} {'USD/día (sonnet)':>17} {'Ahorro USD/día':>16}")
    print("-" * 56)
    coste_base = calcular_coste(tokens_total, "sonnet") * args.sesiones
    for pct in [100, 75, 50, 25]:
        tok = int(tokens_total * pct / 100)
        coste = calcular_coste(tok, "sonnet") * args.sesiones
        ahorro = coste_base - coste
        print(f"{pct:>10}%   {tok:>8,}  ${coste:>16.4f}  ${ahorro:>15.4f}")

    anual = calcular_coste(tokens_total, "sonnet") * args.sesiones * 365
    print(f"\n→ Coste anual proyectado (sonnet, {args.sesiones:,}/día): ${anual:,.2f}")
    print(f"→ Con caching (10× más barato en zona estática): ${anual/10:,.2f}/año")
    print(f"\n[Snapshot precios mayo 2026 — verificar en docs del proveedor]")
    try:
        import tiktoken
        print(f"[Tokenización exacta con tiktoken cl100k_base]")
    except ImportError:
        print(f"[Estimación ±10% — instala tiktoken para conteo exacto: pip install tiktoken]")


if __name__ == "__main__":
    main()
