"""
Sampling — efecto de temperature en varianza, diversidad y fiabilidad.

Qué demuestra:
    El pipeline de sampling (temperature → top-p → top-k) traduce logits
    en texto. Este script mide empiricamente el efecto de temperature en:
    1. Varianza de output — Type-Token Ratio como proxy de diversidad lexica
    2. Longitud y coherencia — misma pregunta, outputs distintos
    3. Fiabilidad en tool calling — % de JSON malformado con T=0.0/0.5/1.0/1.5

Regla practica confirmada empiricamente:
    T=0.0 → determinista, maxima fiabilidad en JSON/tool calls
    T=0.5 → buen equilibrio para Q&A factual y codigo
    T=1.0 → variedad para conversacion general
    T>1.0 → creatividad maxima pero mayor tasa de errores estructurales

Por qué importa para agentes:
    Tool calling con T>1.0 puede producir JSON con campos invalidos o faltantes.
    En produccion se usa T=0.0 o T=0.3 para tool calls, T=0.7-1.0 para texto libre.

Cómo ejecutar:
    make py SCRIPT=python/03-motor-llm/sampling.py

Qué esperar:
    Tabla de varianza por temperatura (3 repeticiones) y tabla de tasa de error
    en tool calling (5 intentos por temperatura). Tarda ~30-60 segundos.

Variables de entorno:
    MODEL        — modelo principal (default: claude-sonnet-4-6)
    SMALL_MODEL  — modelo para demos (default: claude-haiku-4-5-20251001)
"""
import os
import json
import re
import time
from collections import Counter

import anthropic

MODEL = os.environ.get("MODEL", "claude-sonnet-4-6")
SMALL_MODEL = os.environ.get("SMALL_MODEL", "claude-haiku-4-5-20251001")


# ─── 1. Helpers de métricas ─────────────────────────────────────────────────

def diversidad_lexica(texto: str) -> float:
    """Type-Token Ratio (TTR): tipos únicos / total tokens léxicos."""
    palabras = re.findall(r"\b\w+\b", texto.lower())
    if not palabras:
        return 0.0
    return len(set(palabras)) / len(palabras)


def es_json_valido(texto: str) -> bool:
    """Intenta parsear el texto como JSON."""
    try:
        json.loads(texto)
        return True
    except json.JSONDecodeError:
        return False


# ─── 2. Comparar outputs con distintas temperatures ──────────────────────────

PROMPT_CREATIVO = (
    "En dos oraciones, explica por qué el cielo es azul. "
    "Sé creativo y variado en tu respuesta."
)

def medir_varianza_temperature(
    temperatures: list[float],
    repeticiones: int = 3,
) -> None:
    client = anthropic.Anthropic()
    print("\n[varianza de output por temperature]")
    print(f"  Prompt: '{PROMPT_CREATIVO[:60]}...'")
    print(f"  Repeticiones por temperatura: {repeticiones}\n")

    for temp in temperatures:
        longitudes: list[int] = []
        ttrs: list[float] = []
        outputs: list[str] = []

        for rep in range(repeticiones):
            kwargs: dict = {
                "model": SMALL_MODEL,
                "max_tokens": 120,
                "messages": [{"role": "user", "content": PROMPT_CREATIVO}],
            }
            # temperature=0.0 → greedy; Anthropic no acepta top_p junto con temperature
            if temp > 0.0:
                kwargs["temperature"] = temp

            resp = client.messages.create(**kwargs)
            texto = "".join(
                b.text for b in resp.content if b.type == "text"
            )
            longitudes.append(len(texto.split()))
            ttrs.append(diversidad_lexica(texto))
            outputs.append(texto)

        avg_len = sum(longitudes) / len(longitudes)
        avg_ttr = sum(ttrs) / len(ttrs)
        rango_len = max(longitudes) - min(longitudes)

        label = f"T={temp:.1f}"
        print(f"  {label:6s}  avg_palabras={avg_len:5.1f}  rango_len={rango_len:3d}  TTR={avg_ttr:.3f}")
        for i, out in enumerate(outputs):
            print(f"         rep{i+1}: {out[:90]!r}")
        print()


# ─── 3. Tasa de JSON malformado en tool calling ──────────────────────────────

TOOL_SCHEMA = [
    {
        "name": "crear_tarea",
        "description": "Crea una tarea en el gestor de proyectos.",
        "input_schema": {
            "type": "object",
            "properties": {
                "titulo": {"type": "string", "description": "Título corto de la tarea"},
                "prioridad": {
                    "type": "string",
                    "enum": ["alta", "media", "baja"],
                    "description": "Nivel de prioridad",
                },
                "estimacion_horas": {
                    "type": "number",
                    "description": "Estimación en horas (número decimal)",
                },
            },
            "required": ["titulo", "prioridad", "estimacion_horas"],
        },
    }
]

PROMPT_TOOL = (
    "Crea una tarea para revisar el informe de ventas del Q3. "
    "Prioridad alta, estimación 2.5 horas."
)

def medir_tasa_json_malformado(
    temperatures: list[float],
    intentos: int = 5,
) -> None:
    client = anthropic.Anthropic()
    print("\n[tasa de JSON malformado en tool calling]")
    print(f"  Intentos por temperatura: {intentos}\n")

    for temp in temperatures:
        fallos = 0
        errores: list[str] = []

        for _ in range(intentos):
            kwargs: dict = {
                "model": SMALL_MODEL,
                "max_tokens": 256,
                "tools": TOOL_SCHEMA,
                "messages": [{"role": "user", "content": PROMPT_TOOL}],
            }
            if temp > 0.0:
                kwargs["temperature"] = temp

            resp = client.messages.create(**kwargs)

            # Verificar que el modelo usó la herramienta correctamente
            tool_uses = [b for b in resp.content if b.type == "tool_use"]
            if not tool_uses:
                fallos += 1
                errores.append("sin tool_use en respuesta")
                continue

            tool_input = tool_uses[0].input
            # Validar que el input es serializable y tiene los campos requeridos
            try:
                raw = json.dumps(tool_input)
                parsed = json.loads(raw)
                required = {"titulo", "prioridad", "estimacion_horas"}
                if not required.issubset(parsed.keys()):
                    fallos += 1
                    errores.append(f"campos faltantes: {required - parsed.keys()}")
                elif parsed["prioridad"] not in {"alta", "media", "baja"}:
                    fallos += 1
                    errores.append(f"prioridad inválida: {parsed['prioridad']!r}")
            except (TypeError, json.JSONDecodeError) as exc:
                fallos += 1
                errores.append(str(exc))

        tasa = fallos / intentos
        label = f"T={temp:.1f}"
        print(f"  {label:6s}  fallos={fallos}/{intentos}  tasa_error={tasa:.0%}")
        for err in errores:
            print(f"         ✗ {err}")
    print()


# ─── 4. Tabla resumen ────────────────────────────────────────────────────────

def tabla_resumen() -> None:
    print("\n[tabla resumen: temperatura vs uso recomendado]")
    filas = [
        ("0.0", "Greedy",     "Mínima",   "Máxima local", "Tool calling, JSON, extracción estructurada"),
        ("0.5", "Concentrada","Baja",      "Alta",         "Q&A factual, código, análisis"),
        ("1.0", "Original",   "Media",     "Buena",        "Chatbot conversacional, text general"),
        ("1.5", "Plana",      "Alta",      "Menor",        "Escritura creativa (usar min-p=0.05)"),
    ]
    header = f"  {'T':>4}  {'Distribución':15}  {'Diversidad':12}  {'Coherencia':12}  Uso recomendado"
    sep    = "  " + "-" * (len(header) - 2)
    print(header)
    print(sep)
    for temp, dist, div, coh, uso in filas:
        print(f"  {temp:>4}  {dist:15}  {div:12}  {coh:12}  {uso}")
    print()


# ─── Main ──────────────────────────────────────────────────────────────────

if __name__ == "__main__":
    print("=== Sampling: temperatura, diversidad y fiabilidad ===")

    medir_varianza_temperature(temperatures=[0.0, 0.5, 1.0], repeticiones=3)
    medir_tasa_json_malformado(temperatures=[0.0, 0.5, 1.0, 1.5], intentos=5)
    tabla_resumen()
