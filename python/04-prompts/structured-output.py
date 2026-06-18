"""Extracción de datos estructurados de texto libre usando tres métodos.

Demuestra:
- Método 1: instrucción libre — "devuelve JSON con campos X, Y, Z"
- Método 2: JSON schema en el prompt — schema explícito con tipos y restricciones
- Método 3: Pydantic + tool_use forzado — constrained decoding via herramienta
- Métricas: tasa de fallo de parsing, precisión de extracción, tokens consumidos

Dependencias: anthropic, pydantic (pip install anthropic pydantic)

Cómo ejecutar:
    make py SCRIPT=python/04-prompts/structured-output.py

Qué esperar:
    Extraccion del mismo texto con 3 metodos. Tabla de tasa de exito de
    parsing, precision de campos y tokens por metodo.

Variables de entorno:
    MODEL — modelo a usar (default: claude-sonnet-4-6)
"""

import os
import json
import re
from typing import Optional
import anthropic
from pydantic import BaseModel, ValidationError

MODEL = os.environ.get("MODEL", "claude-sonnet-4-6")


# ─── 1. Datos de entrada ─────────────────────────────────────────────────────

REVIEWS = [
    {
        "text": (
            "Compré el 'Altavoz Bluetooth Pro X200' por 79.99€ el mes pasado. "
            "La calidad de sonido es impresionante para su precio. "
            "Le doy 4.5 sobre 5 estrellas. "
            "El único problema es que la batería dura solo 6 horas, menos de lo prometido."
        ),
        "expected": {
            "nombre_producto": "Altavoz Bluetooth Pro X200",
            "precio": 79.99,
            "rating": 4.5,
            "aspecto_positivo": "calidad de sonido",
            "aspecto_negativo": "batería dura 6 horas en lugar de lo prometido",
        },
    },
    {
        "text": (
            "El 'Ratón Ergonómico ErgoMaster 3000' cuesta 149€ y es una maravilla. "
            "Llevo 3 meses usándolo sin ningún problema de muñeca. "
            "Puntuación: 5/5. No tiene ningún defecto reseñable."
        ),
        "expected": {
            "nombre_producto": "Ratón Ergonómico ErgoMaster 3000",
            "precio": 149.0,
            "rating": 5.0,
            "aspecto_positivo": "sin problemas de muñeca",
            "aspecto_negativo": None,
        },
    },
    {
        "text": (
            "El Teclado Mecánico TechType K85 que compré por 89 euros es un fiasco total. "
            "Teclas que se atascan, ruido excesivo y el software no funciona en Mac. "
            "No doy más de 1.5 de 5. Muy decepcionado."
        ),
        "expected": {
            "nombre_producto": "Teclado Mecánico TechType K85",
            "precio": 89.0,
            "rating": 1.5,
            "aspecto_positivo": None,
            "aspecto_negativo": "teclas que se atascan, ruido excesivo, software no funciona en Mac",
        },
    },
]


# ─── 2. Modelo Pydantic ──────────────────────────────────────────────────────

class ProductReview(BaseModel):
    nombre_producto: str
    precio: float
    rating: float  # 1.0 a 5.0
    aspecto_positivo: Optional[str] = None
    aspecto_negativo: Optional[str] = None


# ─── 3. Método 1: Instrucción libre ──────────────────────────────────────────

SYSTEM_FREE = """\
Extrae los datos de la siguiente reseña de producto.
Devuelve SOLO JSON válido con estos campos exactos:
{
  "nombre_producto": "nombre del producto",
  "precio": <número con decimales>,
  "rating": <número entre 1 y 5>,
  "aspecto_positivo": "descripción o null",
  "aspecto_negativo": "descripción o null"
}
Sin texto antes ni después del JSON."""


def extract_free_instruction(client: anthropic.Anthropic, text: str) -> dict:
    """Método 1: instrucción libre sin garantía de formato."""
    response = client.messages.create(
        model=MODEL,
        max_tokens=300,
        system=SYSTEM_FREE,
        messages=[{"role": "user", "content": text}],
    )
    output = response.content[0].text.strip()

    # Intentar parsear — puede fallar
    try:
        data = json.loads(output)
        parse_ok = True
        error = None
    except json.JSONDecodeError as e:
        data = {}
        parse_ok = False
        error = str(e)

    return {
        "method": "1-instruccion-libre",
        "raw_output": output,
        "data": data,
        "parse_ok": parse_ok,
        "error": error,
        "tokens_input": response.usage.input_tokens,
        "tokens_output": response.usage.output_tokens,
    }


# ─── 4. Método 2: JSON schema en el prompt ───────────────────────────────────

SYSTEM_SCHEMA = """\
Extrae los datos de la reseña de producto. El output debe ser JSON válido que siga este schema:

```json-schema
{
  "type": "object",
  "required": ["nombre_producto", "precio", "rating"],
  "properties": {
    "nombre_producto": {
      "type": "string",
      "description": "Nombre exacto del producto mencionado"
    },
    "precio": {
      "type": "number",
      "description": "Precio en euros como número decimal (ej: 79.99)"
    },
    "rating": {
      "type": "number",
      "description": "Puntuación del 1.0 al 5.0"
    },
    "aspecto_positivo": {
      "type": ["string", "null"],
      "description": "Principal aspecto positivo mencionado, o null si no hay"
    },
    "aspecto_negativo": {
      "type": ["string", "null"],
      "description": "Principal aspecto negativo mencionado, o null si no hay"
    }
  }
}
```

Responde SOLO con el JSON. Sin explicaciones ni texto adicional."""


def extract_with_schema(client: anthropic.Anthropic, text: str) -> dict:
    """Método 2: JSON schema explícito en el prompt."""
    response = client.messages.create(
        model=MODEL,
        max_tokens=300,
        system=SYSTEM_SCHEMA,
        messages=[{"role": "user", "content": text}],
    )
    output = response.content[0].text.strip()
    # Eliminar posibles backticks de markdown
    clean = re.sub(r'^```(?:json)?\n?', '', output)
    clean = re.sub(r'\n?```$', '', clean).strip()

    try:
        data = json.loads(clean)
        parse_ok = True
        error = None
    except json.JSONDecodeError as e:
        data = {}
        parse_ok = False
        error = str(e)

    return {
        "method": "2-json-schema-prompt",
        "raw_output": output,
        "data": data,
        "parse_ok": parse_ok,
        "error": error,
        "tokens_input": response.usage.input_tokens,
        "tokens_output": response.usage.output_tokens,
    }


# ─── 5. Método 3: Pydantic + tool_use forzado ────────────────────────────────

TOOL_DEFINITION = {
    "name": "guardar_reseña",
    "description": "Guarda los datos estructurados extraídos de la reseña",
    "input_schema": {
        "type": "object",
        "required": ["nombre_producto", "precio", "rating"],
        "properties": {
            "nombre_producto": {
                "type": "string",
                "description": "Nombre exacto del producto",
            },
            "precio": {
                "type": "number",
                "description": "Precio en euros",
            },
            "rating": {
                "type": "number",
                "description": "Puntuación del 1.0 al 5.0",
            },
            "aspecto_positivo": {
                "type": "string",
                "description": "Principal aspecto positivo, vacío si no hay",
            },
            "aspecto_negativo": {
                "type": "string",
                "description": "Principal aspecto negativo, vacío si no hay",
            },
        },
    },
}


def extract_with_tool(client: anthropic.Anthropic, text: str) -> dict:
    """Método 3: tool_use forzado + validación Pydantic."""
    response = client.messages.create(
        model=MODEL,
        max_tokens=300,
        tools=[TOOL_DEFINITION],
        tool_choice={"type": "tool", "name": "guardar_reseña"},
        messages=[
            {
                "role": "user",
                "content": f"Extrae los datos de esta reseña:\n\n{text}",
            }
        ],
    )

    # El modelo siempre llama al tool cuando se usa tool_choice forzado
    tool_block = next(
        (b for b in response.content if b.type == "tool_use"), None
    )

    if not tool_block:
        return {
            "method": "3-pydantic-tool",
            "raw_output": str(response.content),
            "data": {},
            "parse_ok": False,
            "pydantic_ok": False,
            "error": "No se recibió tool_use block",
            "tokens_input": response.usage.input_tokens,
            "tokens_output": response.usage.output_tokens,
        }

    raw_input = tool_block.input
    parse_ok = True

    # Normalizar campos opcionales: strings vacíos → None para Pydantic
    normalized = dict(raw_input)
    for field in ("aspecto_positivo", "aspecto_negativo"):
        if normalized.get(field) == "":
            normalized[field] = None

    # Validar con Pydantic
    try:
        review = ProductReview(**normalized)
        pydantic_ok = True
        pydantic_error = None
        validated_data = review.model_dump()
    except ValidationError as e:
        pydantic_ok = False
        pydantic_error = str(e)
        validated_data = normalized

    return {
        "method": "3-pydantic-tool",
        "raw_output": json.dumps(raw_input, ensure_ascii=False),
        "data": validated_data,
        "parse_ok": parse_ok,
        "pydantic_ok": pydantic_ok,
        "error": pydantic_error,
        "tokens_input": response.usage.input_tokens,
        "tokens_output": response.usage.output_tokens,
    }


# ─── 6. Evaluación de precisión ─────────────────────────────────────────────

def evaluate_extraction(data: dict, expected: dict) -> dict:
    """Compara los datos extraídos con los esperados."""
    checks = {}

    # nombre_producto: substring match (insensible a capitalización)
    exp_name = (expected.get("nombre_producto") or "").lower()
    got_name = str(data.get("nombre_producto") or "").lower()
    checks["nombre_producto"] = exp_name[:15] in got_name

    # precio: dentro de ±0.5€
    try:
        checks["precio"] = abs(float(data.get("precio", 0)) - expected["precio"]) <= 0.5
    except (TypeError, ValueError):
        checks["precio"] = False

    # rating: dentro de ±0.5
    try:
        checks["rating"] = abs(float(data.get("rating", 0)) - expected["rating"]) <= 0.5
    except (TypeError, ValueError):
        checks["rating"] = False

    correct = sum(checks.values())
    return {"field_checks": checks, "correct_fields": correct, "total_fields": len(checks)}


# ─── 7. Impresión de resultados ──────────────────────────────────────────────

def print_review_comparison(review: dict, results: list[dict]):
    """Imprime comparación de los tres métodos para una reseña."""
    print(f"\n{'═' * 74}")
    print(f"  RESEÑA: {review['text'][:80]}...")
    print(f"{'─' * 74}")

    for r in results:
        pydantic_info = ""
        if "pydantic_ok" in r:
            pydantic_info = f", Pydantic: {'✓' if r['pydantic_ok'] else '✗'}"

        print(f"\n  [{r['method']}]")
        print(f"  Parsing: {'✓' if r['parse_ok'] else '✗'}{pydantic_info}")
        if r["error"]:
            print(f"  Error: {r['error'][:80]}")

        if r["data"]:
            eval_result = evaluate_extraction(r["data"], review["expected"])
            field_symbols = "  ".join(
                f"{k}: {'✓' if v else '✗'}"
                for k, v in eval_result["field_checks"].items()
            )
            print(f"  Campos correctos: {eval_result['correct_fields']}/{eval_result['total_fields']}")
            print(f"  {field_symbols}")
            print(f"  Extraído: nombre={r['data'].get('nombre_producto', 'N/A')[:30]}, "
                  f"precio={r['data'].get('precio')}, rating={r['data'].get('rating')}")

        print(f"  Tokens: {r['tokens_input']} input / {r['tokens_output']} output")


def print_summary(all_results: list, reviews: list):
    """Imprime tabla comparativa con métricas agregadas."""
    method_names = [r["method"] for r in all_results[0]]
    stats: dict = {
        m: {"parse_ok": 0, "pydantic_ok": 0, "correct_fields": 0, "tokens_in": 0, "tokens_out": 0}
        for m in method_names
    }

    n = len(reviews)
    n_fields = 3  # nombre, precio, rating

    for review_idx, review_results in enumerate(all_results):
        review = reviews[review_idx]
        for r in review_results:
            m = r["method"]
            if r["parse_ok"]:
                stats[m]["parse_ok"] += 1
            if r.get("pydantic_ok", r["parse_ok"]):
                stats[m]["pydantic_ok"] += 1
            if r["data"]:
                ev = evaluate_extraction(r["data"], review["expected"])
                stats[m]["correct_fields"] += ev["correct_fields"]
            stats[m]["tokens_in"] += r["tokens_input"]
            stats[m]["tokens_out"] += r["tokens_output"]

    print(f"\n{'═' * 74}")
    print("  TABLA COMPARATIVA FINAL")
    print(f"{'═' * 74}")
    print(f"  {'Método':<28} {'Parse OK':>9} {'Validación':>12} {'Precisión':>11} {'Tokens/in':>10}")
    print(f"  {'-' * 72}")
    for m, s in stats.items():
        parse_rate = s["parse_ok"] / n
        pydantic_rate = s["pydantic_ok"] / n
        precision = s["correct_fields"] / (n * n_fields)
        avg_tokens_in = s["tokens_in"] / n
        print(
            f"  {m:<28} {parse_rate:>8.0%} {pydantic_rate:>11.0%} "
            f"{precision:>10.0%} {avg_tokens_in:>9.0f}"
        )

    print(f"\n  'Parse OK':  el output era JSON parseable")
    print(f"  'Validación': el JSON pasó validación Pydantic (solo Método 3)")
    print(f"  'Precisión':  campos nombre/precio/rating con valor correcto (±tolerancia)")
    print(f"\n  El Método 3 garantiza estructura válida via tool_use forzado.")
    print(f"  Si los tres métodos tienen alta precisión, la instrucción libre es suficiente.")
    print(f"  Si Método 1 tiene fallos de parsing, el Método 3 los elimina sin overhead mayor.")


# ─── 8. Main ─────────────────────────────────────────────────────────────────

if __name__ == "__main__":
    client = anthropic.Anthropic()

    all_results = []

    for review in REVIEWS:
        results = [
            extract_free_instruction(client, review["text"]),
            extract_with_schema(client, review["text"]),
            extract_with_tool(client, review["text"]),
        ]
        print_review_comparison(review, results)
        all_results.append(results)

    print_summary(all_results, REVIEWS)
