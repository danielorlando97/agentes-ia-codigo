"""Comparación directa vs CoT explícito vs zero-shot CoT en problema aritmético multi-paso.

Demuestra:
- Variante 1: prompt directo — el modelo responde sin razonar
- Variante 2: CoT explícito — el prompt describe los pasos intermedios a seguir
- Variante 3: zero-shot CoT — trigger phrase "piensa paso a paso antes de responder"
- Métricas: accuracy, tokens de output (proxy de razonamiento), latencia

Cómo ejecutar:
    make py SCRIPT=python/04-prompts/chain-of-thought.py

Qué esperar:
    Tres respuestas al mismo problema: directa, CoT explicito, zero-shot CoT.
    Tabla comparativa de accuracy, tokens y latencia.

Variables de entorno:
    MODEL — modelo a usar (default: claude-sonnet-4-6)
"""

import os
import time
import anthropic

MODEL = os.environ.get("MODEL", "claude-sonnet-4-6")

# ─── 1. Problemas con trampa ─────────────────────────────────────────────────
# Problemas que parecen simples pero requieren razonamiento cuidadoso.
# Una respuesta apresurada produce el resultado equivocado.

PROBLEMS = [
    {
        "question": (
            "Una tienda vende manzanas a 3 por €1. "
            "Juan compra 12 manzanas y paga con un billete de €10. "
            "¿Cuánto cambio recibe? "
            "Nota: la tienda tiene una oferta especial hoy: si compras más de 10 manzanas, "
            "obtienes un 20% de descuento en el total."
        ),
        "answer": "€6",  # Sin oferta: 12/3 = 4€. Con descuento 20%: 4 * 0.8 = 3.2€. Cambio: 10 - 3.2 = 6.8€ ≈ €6.80
        "answer_exact": "6.80",
        "explanation": (
            "12 manzanas a 3/€1 = €4.00 base. "
            "Descuento 20% por >10 manzanas: €4.00 × 0.80 = €3.20. "
            "Cambio: €10.00 - €3.20 = €6.80"
        ),
    },
    {
        "question": (
            "Un tren parte de Madrid a las 8:00 y llega a Barcelona a las 10:30. "
            "Otro tren parte de Barcelona a las 9:00 y llega a Madrid a las 11:30. "
            "Los trenes viajan en sentidos opuestos por la misma vía. "
            "¿A qué hora se cruzan si Madrid y Barcelona están a 600 km?"
        ),
        "answer": "9:30",
        "answer_exact": "9:30",
        "explanation": (
            "Tren A: 600 km en 2.5 h → 240 km/h. Sale a las 8:00. "
            "Tren B: 600 km en 2.5 h → 240 km/h. Sale a las 9:00. "
            "A la hora 9:00, el tren A lleva 1 h → ha recorrido 240 km. Quedan 360 km entre ellos. "
            "Se acercan a 480 km/h combinados. 360/480 = 0.75 h = 45 min. "
            "Se cruzan a las 9:45."
            # Nota: la respuesta correcta exacta es 9:45. Se usa para probar si el modelo razona.
        ),
        "answer_exact": "9:45",
    },
    {
        "question": (
            "Una pizzería vende pizzas pequeñas por €8 y grandes por €14. "
            "Ayer vendió 15 pizzas en total y ganó €162. "
            "¿Cuántas pizzas grandes vendió?"
        ),
        "answer": "7",
        "answer_exact": "7",
        "explanation": (
            "Sea g = grandes, p = pequeñas. "
            "g + p = 15 → p = 15 - g. "
            "14g + 8p = 162 → 14g + 8(15-g) = 162 → 14g + 120 - 8g = 162 → 6g = 42 → g = 7."
        ),
    },
]


# ─── 2. System prompts por variante ─────────────────────────────────────────

SYSTEM_DIRECT = (
    "Resuelve el siguiente problema matemático. "
    "Responde solo con el número o valor final, sin explicaciones."
)

SYSTEM_COT_EXPLICIT = (
    "Resuelve el siguiente problema matemático siguiendo estos pasos:\n"
    "1. Identifica los datos conocidos\n"
    "2. Escribe la ecuación o proceso necesario\n"
    "3. Realiza el cálculo paso a paso\n"
    "4. Verifica el resultado\n"
    "5. Da la respuesta final claramente indicada\n"
    "Muestra cada paso explícitamente."
)

SYSTEM_ZERO_SHOT_COT = (
    "Resuelve el siguiente problema matemático. "
    "Piensa paso a paso antes de responder. "
    "Muestra tu razonamiento completo y da la respuesta final al final."
)


# ─── 3. Resolución con métricas ──────────────────────────────────────────────

def solve_problem(
    client: anthropic.Anthropic,
    system: str,
    question: str,
    variant_name: str,
) -> dict:
    """Resuelve un problema midiendo accuracy, tokens y latencia."""
    t0 = time.perf_counter()
    response = client.messages.create(
        model=MODEL,
        max_tokens=800,
        system=system,
        messages=[{"role": "user", "content": question}],
    )
    latency_ms = (time.perf_counter() - t0) * 1000

    output_text = response.content[0].text.strip()

    return {
        "variant": variant_name,
        "output": output_text,
        "tokens_input": response.usage.input_tokens,
        "tokens_output": response.usage.output_tokens,
        "latency_ms": latency_ms,
    }


def check_answer(output: str, answer_exact: str) -> bool:
    """Verifica si la respuesta contiene el valor correcto."""
    return answer_exact.lower() in output.lower()


# ─── 4. Impresión de resultados ──────────────────────────────────────────────

def print_problem_results(problem: dict, results: list[dict]):
    """Imprime resultados de un problema en las tres variantes."""
    print(f"\n{'═' * 72}")
    print(f"  PROBLEMA: {problem['question'][:80]}...")
    print(f"  Respuesta correcta: {problem['answer_exact']}")
    print(f"  Lógica: {problem['explanation']}")
    print(f"{'─' * 72}")

    for r in results:
        correct = check_answer(r["output"], problem["answer_exact"])
        status = "✓ CORRECTO" if correct else "✗ INCORRECTO"
        print(f"\n  [{r['variant']}] {status}")
        print(f"  Tokens input/output: {r['tokens_input']} / {r['tokens_output']}")
        print(f"  Latencia: {r['latency_ms']:.0f} ms")
        # Truncar output largo
        output_preview = r["output"][:200] + ("..." if len(r["output"]) > 200 else "")
        print(f"  Output: {output_preview}")


def print_summary(all_results: list[list[dict]], problems: list[dict]):
    """Imprime tabla comparativa agregada."""
    # Agrupar por variante
    variant_names = [r["variant"] for r in all_results[0]]
    stats: dict[str, dict] = {v: {"correct": 0, "tokens_out": 0, "latency": 0} for v in variant_names}

    for prob_idx, prob_results in enumerate(all_results):
        problem = problems[prob_idx]
        for r in prob_results:
            if check_answer(r["output"], problem["answer_exact"]):
                stats[r["variant"]]["correct"] += 1
            stats[r["variant"]]["tokens_out"] += r["tokens_output"]
            stats[r["variant"]]["latency"] += r["latency_ms"]

    n_problems = len(problems)
    print(f"\n{'═' * 72}")
    print("  TABLA COMPARATIVA AGREGADA")
    print(f"{'═' * 72}")
    print(f"  {'Variante':<30} {'Accuracy':>10} {'Tokens out':>12} {'Latencia':>12}")
    print(f"  {'-' * 64}")
    for v, s in stats.items():
        acc = s["correct"] / n_problems
        avg_tok = s["tokens_out"] / n_problems
        avg_lat = s["latency"] / n_problems
        print(f"  {v:<30} {acc:>9.0%} {avg_tok:>11.0f} {avg_lat:>10.0f} ms")

    print(f"\n  Nota: 'Tokens out' es proxy del razonamiento generado.")
    print(f"  CoT produce más tokens porque muestra pasos intermedios.")
    print(f"  Si accuracy es similar, el prompt directo es más eficiente.")


# ─── 5. Main ─────────────────────────────────────────────────────────────────

if __name__ == "__main__":
    client = anthropic.Anthropic()

    variants = [
        ("Directo (sin CoT)", SYSTEM_DIRECT),
        ("CoT explícito", SYSTEM_COT_EXPLICIT),
        ("Zero-shot CoT", SYSTEM_ZERO_SHOT_COT),
    ]

    all_results = []

    for problem in PROBLEMS:
        problem_results = []
        for name, system in variants:
            result = solve_problem(client, system, problem["question"], name)
            problem_results.append(result)
        print_problem_results(problem, problem_results)
        all_results.append(problem_results)

    print_summary(all_results, PROBLEMS)
