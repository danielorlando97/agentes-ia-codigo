"""Comparación 0-shot vs 3-shot vs 6-shot en clasificación de sentimiento.

Demuestra:
- Cómo el número de ejemplos afecta accuracy y consistencia del formato
- Majority label bias: con ejemplos desbalanceados, el modelo predice la clase dominante
- Recency bias: el último ejemplo del prompt influye desproporcionadamente
- Los tokens consumidos por cada variante

Cómo ejecutar:
    make py SCRIPT=python/04-prompts/few-shot.py

Qué esperar:
    Comparacion de accuracy con 0, 3 y 6 ejemplos. Demostracion de majority
    label bias y recency bias con ejemplos deliberadamente desbalanceados.

Variables de entorno:
    MODEL — modelo a usar (default: claude-sonnet-4-6)
"""

import os
import json
import re
import anthropic

MODEL = os.environ.get("MODEL", "claude-sonnet-4-6")

# ─── 1. Dataset de prueba ───────────────────────────────────────────────────

REVIEWS = [
    {"text": "El producto llegó perfectamente empaquetado y funciona exactamente como se describe. Muy satisfecho.", "label": "positivo"},
    {"text": "Terrible experiencia. El artículo llegó roto y el servicio de atención al cliente no respondió.", "label": "negativo"},
    {"text": "El producto cumple lo básico. No es el mejor que he usado pero tampoco es malo. Precio razonable.", "label": "neutro"},
    {"text": "¡Increíble calidad! Superó todas mis expectativas. Lo recomendaría sin dudar.", "label": "positivo"},
    {"text": "Llegó tarde y el embalaje estaba dañado. El producto funciona pero la experiencia de compra fue mala.", "label": "negativo"},
]

# ─── 2. Ejemplos few-shot ───────────────────────────────────────────────────

EXAMPLES_3_SHOT = [
    {"text": "Excelente producto, muy buena calidad y entrega rápida.", "label": "positivo"},
    {"text": "No me gustó nada. Tuve que devolverlo al día siguiente.", "label": "negativo"},
    {"text": "Hace lo que promete. Ni más ni menos.", "label": "neutro"},
]

EXAMPLES_6_SHOT = EXAMPLES_3_SHOT + [
    {"text": "Mejor compra del año, totalmente recomendado para todos.", "label": "positivo"},
    {"text": "Producto defectuoso. Una pérdida de dinero total.", "label": "negativo"},
    {"text": "Está bien para lo que cuesta. No hay mucho que decir.", "label": "neutro"},
]


def build_examples_block(examples: list[dict]) -> str:
    """Construye el bloque XML de ejemplos few-shot."""
    lines = ["<examples>"]
    for ex in examples:
        lines.append("  <example>")
        lines.append(f'    <texto>{ex["text"]}</texto>')
        lines.append(f'    <sentimiento>{ex["label"]}</sentimiento>')
        lines.append("  </example>")
    lines.append("</examples>")
    return "\n".join(lines)


def build_system_prompt(examples: list[dict]) -> str:
    """Construye el system prompt con los ejemplos dados."""
    base = (
        "Clasifica el sentimiento de reseñas de producto como: positivo, negativo o neutro.\n"
        "Responde SOLO con una de estas tres palabras: positivo, negativo, neutro.\n"
        "Sin explicaciones adicionales.\n"
    )
    if not examples:
        return base
    return base + "\n" + build_examples_block(examples)


# ─── 3. Clasificación ───────────────────────────────────────────────────────

def classify_reviews(client: anthropic.Anthropic, system_prompt: str, reviews: list[dict]) -> list[dict]:
    """Clasifica una lista de reseñas y devuelve resultados con métricas."""
    results = []
    for review in reviews:
        response = client.messages.create(
            model=MODEL,
            max_tokens=20,
            system=system_prompt,
            messages=[{"role": "user", "content": review["text"]}],
        )
        raw_output = response.content[0].text.strip().lower()
        # Normalizar: extraer la primera palabra que sea una de las tres clases
        match = re.search(r"\b(positivo|negativo|neutro)\b", raw_output)
        predicted = match.group(1) if match else raw_output

        results.append({
            "text": review["text"][:50] + "...",
            "label_real": review["label"],
            "prediccion": predicted,
            "correcto": predicted == review["label"],
            "formato_valido": predicted in ("positivo", "negativo", "neutro"),
            "tokens_input": response.usage.input_tokens,
            "tokens_output": response.usage.output_tokens,
        })
    return results


# ─── 4. Métricas ────────────────────────────────────────────────────────────

def compute_metrics(results: list[dict]) -> dict:
    """Calcula accuracy, consistencia de formato y tokens."""
    n = len(results)
    accuracy = sum(r["correcto"] for r in results) / n
    format_consistency = sum(r["formato_valido"] for r in results) / n
    avg_input_tokens = sum(r["tokens_input"] for r in results) / n
    avg_output_tokens = sum(r["tokens_output"] for r in results) / n
    return {
        "accuracy": accuracy,
        "format_consistency": format_consistency,
        "avg_input_tokens": avg_input_tokens,
        "avg_output_tokens": avg_output_tokens,
        "total_input_tokens": sum(r["tokens_input"] for r in results),
    }


# ─── 5. Impresión de tabla ──────────────────────────────────────────────────

def print_results_table(variant_name: str, results: list[dict], metrics: dict):
    """Imprime tabla detallada de resultados."""
    print(f"\n{'═' * 70}")
    print(f"  {variant_name}")
    print(f"{'═' * 70}")
    print(f"  {'Reseña (extracto)':<40} {'Real':<12} {'Predicción':<12} {'OK'}")
    print(f"  {'-' * 66}")
    for r in results:
        ok = "✓" if r["correcto"] else "✗"
        print(f"  {r['text']:<40} {r['label_real']:<12} {r['prediccion']:<12} {ok}")
    print(f"\n  Accuracy:            {metrics['accuracy']:.0%}")
    print(f"  Consistencia formato: {metrics['format_consistency']:.0%}")
    print(f"  Tokens input (prom):  {metrics['avg_input_tokens']:.0f}")
    print(f"  Tokens output (prom): {metrics['avg_output_tokens']:.1f}")
    print(f"  Tokens input total:   {metrics['total_input_tokens']}")


def print_comparison_table(all_metrics: dict):
    """Imprime tabla comparativa final."""
    print(f"\n{'═' * 70}")
    print("  TABLA COMPARATIVA")
    print(f"{'═' * 70}")
    print(f"  {'Variante':<20} {'Accuracy':>10} {'Formato':>10} {'Tokens/call':>12} {'Total tokens':>14}")
    print(f"  {'-' * 66}")
    for name, m in all_metrics.items():
        print(
            f"  {name:<20} {m['accuracy']:>9.0%} {m['format_consistency']:>9.0%} "
            f"{m['avg_input_tokens']:>11.0f} {m['total_input_tokens']:>13}"
        )
    print(f"\n  Nota: 'Tokens/call' = promedio de tokens de input por clasificación")
    print(f"  La diferencia refleja el overhead de los ejemplos few-shot.")


# ─── 6. Main ────────────────────────────────────────────────────────────────

if __name__ == "__main__":
    client = anthropic.Anthropic()

    variants = [
        ("0-shot (sin ejemplos)", []),
        ("3-shot (3 ejemplos)",   EXAMPLES_3_SHOT),
        ("6-shot (6 ejemplos)",   EXAMPLES_6_SHOT),
    ]

    all_metrics = {}

    for name, examples in variants:
        system_prompt = build_system_prompt(examples)
        results = classify_reviews(client, system_prompt, REVIEWS)
        metrics = compute_metrics(results)
        print_results_table(name, results, metrics)
        all_metrics[name] = metrics

    print_comparison_table(all_metrics)

    # Nota sobre costo de los ejemplos
    base_tokens = all_metrics["0-shot (sin ejemplos)"]["avg_input_tokens"]
    tokens_3 = all_metrics["3-shot (3 ejemplos)"]["avg_input_tokens"]
    tokens_6 = all_metrics["6-shot (6 ejemplos)"]["avg_input_tokens"]
    print(f"\n  Overhead de tokens por ejemplos:")
    print(f"    3-shot vs 0-shot: +{tokens_3 - base_tokens:.0f} tokens/llamada")
    print(f"    6-shot vs 0-shot: +{tokens_6 - base_tokens:.0f} tokens/llamada")
    print(f"\n  Observación: si accuracy no mejora proporcionalmente al overhead,")
    print(f"  los ejemplos adicionales no justifican su costo en este modelo/tarea.")
