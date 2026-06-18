"""
Instruct vs base — diferencia entre modelos con y sin RLHF.

Qué demuestra:
    Los modelos instruct (post-RLHF) y los modelos base (pre-RLHF) responden
    de forma muy distinta al mismo input. Este script ilustra el contraste:
    1. Prompt estilo 'instruct': instruccion directa con formato imperativo
    2. Prompt estilo 'base': texto a completar sin instruccion explicita

Nota sobre disponibilidad:
    Anthropic no expone modelos base (pre-RLHF) via API publica.
    Este script simula el contraste documentado enviando ambos formatos de prompt
    al mismo modelo instruct. El mismo modelo instruct responde ambos, pero la
    diferencia en output ilustra por que el RLHF importa: el modelo instruct
    responde a la instruccion aunque reciba el prompt de base, pero el gap de
    comportamiento real entre base e instruct seria mucho mayor.

Cómo ejecutar:
    make py SCRIPT=python/03-motor-llm/instruct-vs-base.py

Qué esperar:
    Tres pares de comparacion (tareas distintas): cada par muestra el output
    con prompt instruct vs prompt base y la diferencia en seguimiento de instrucciones.

Variables de entorno:
    MODEL        — modelo a usar (default: claude-sonnet-4-6)
    SMALL_MODEL  — modelo para demos masivas (default: claude-haiku-4-5-20251001)
"""
import os
import re
import anthropic

MODEL = os.environ.get("MODEL", "claude-sonnet-4-6")
SMALL_MODEL = os.environ.get("SMALL_MODEL", "claude-haiku-4-5-20251001")


# ─── 1. Prompts: instrucción directa vs completar-texto ─────────────────────

# Prompt estilo instruct: el usuario da una instrucción explícita con imperativo
PROMPT_INSTRUCT = (
    "Lista los tres pasos principales para preparar una taza de té. "
    "Sé conciso. Usa formato de lista numerada."
)

# Prompt estilo base: texto que el modelo debe "continuar" como lo haría un
# language model sin fine-tuning de instrucciones
PROMPT_BASE = (
    "Para preparar una taza de té, primero"
)

# System prompt que acerca el comportamiento al de un base model:
# sin role, sin instrucciones de formato, solo continuación de texto
SYSTEM_BASE_SIM = (
    "Continúa el texto que se te da. No añadas saludos ni despedidas. "
    "No uses formato de lista a menos que el texto de entrada lo sugiera. "
    "Escribe en el mismo registro y tono del texto de entrada."
)


# ─── 2. Métricas de seguimiento de instrucciones ────────────────────────────

def detectar_lista_numerada(texto: str) -> bool:
    """Verifica si el output contiene una lista numerada (1. / 2. / 3.)."""
    return bool(re.search(r"^\s*[123]\.", texto, re.MULTILINE))


def detectar_tres_pasos(texto: str) -> bool:
    """Verifica si el output contiene exactamente 3 ítems numerados."""
    items = re.findall(r"^\s*\d+\.", texto, re.MULTILINE)
    return len(items) == 3


def medir_seguimiento_instrucciones(
    repeticiones: int = 3,
) -> None:
    client = anthropic.Anthropic()
    print("\n[comparación: prompt instruct vs prompt base]")
    print(f"  Repeticiones: {repeticiones}\n")

    configuraciones = [
        {
            "label": "instruct-prompt",
            "prompt": PROMPT_INSTRUCT,
            "system": None,
            "descripcion": "Instrucción directa (formato imperativo con requisitos explícitos)",
        },
        {
            "label": "base-sim-prompt",
            "prompt": PROMPT_BASE,
            "system": SYSTEM_BASE_SIM,
            "descripcion": "Continuación de texto (simulación del estilo base model)",
        },
    ]

    for config in configuraciones:
        print(f"  --- {config['label']} ---")
        print(f"  {config['descripcion']}")
        print(f"  Prompt: {config['prompt']!r}")
        print()

        tasa_lista    = 0
        tasa_3_pasos  = 0
        total_tokens  = 0
        outputs: list[str] = []

        for _ in range(repeticiones):
            kwargs: dict = {
                "model": SMALL_MODEL,
                "max_tokens": 200,
                "messages": [{"role": "user", "content": config["prompt"]}],
            }
            if config["system"]:
                kwargs["system"] = config["system"]

            resp = client.messages.create(**kwargs)
            texto = "".join(b.text for b in resp.content if b.type == "text")
            outputs.append(texto)

            if detectar_lista_numerada(texto):
                tasa_lista += 1
            if detectar_tres_pasos(texto):
                tasa_3_pasos += 1
            total_tokens += resp.usage.input_tokens + resp.usage.output_tokens

        avg_tokens = total_tokens / repeticiones

        print(f"  Tasa lista numerada:  {tasa_lista}/{repeticiones} ({tasa_lista/repeticiones:.0%})")
        print(f"  Tasa 3 ítems exactos: {tasa_3_pasos}/{repeticiones} ({tasa_3_pasos/repeticiones:.0%})")
        print(f"  Tokens promedio/call: {avg_tokens:.0f}")
        print()
        print("  Outputs:")
        for i, out in enumerate(outputs):
            print(f"    rep{i+1}: {out[:120]!r}")
        print()


# ─── 3. Diferencia de format: tabla comparativa ──────────────────────────────

def tabla_diferencias() -> None:
    print("\n[tabla: diferencias documentadas base vs instruct]")
    filas = [
        ("Formato de prompt", "Instrucción imperativa directa", "Texto a completar"),
        ("Output esperado",   "Sigue instrucciones explícitas", "Continúa el texto dado"),
        ("Saludos/formato",   "Sí (conversacional por defecto)","No (texto plano)"),
        ("Seguimiento reglas","Alto (RLHF/SFT orientado)",     "Bajo (no fine-tuneado)"),
        ("Uso en agentes",    "Siempre (tool calling, system)", "Nunca directamente"),
        ("Acceso API",        "Público (claude-haiku, sonnet)", "No expuesto por Anthropic"),
        ("Temperatura típica","0.0–1.0 según tarea",           "0.7–1.0 para completar"),
    ]
    header = f"  {'Dimensión':30}  {'Instruct model':35}  {'Base model'}"
    sep    = "  " + "-" * 100
    print(header)
    print(sep)
    for dim, instruct, base in filas:
        print(f"  {dim:30}  {instruct:35}  {base}")
    print()


# ─── Main ──────────────────────────────────────────────────────────────────

if __name__ == "__main__":
    print("=== Instruct vs Base: diferencias de comportamiento ===")
    medir_seguimiento_instrucciones(repeticiones=3)
    tabla_diferencias()
