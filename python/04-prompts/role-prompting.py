"""Comparación de outputs con: sin rol, rol corto (~20 tokens), rol largo (~150 tokens).

Demuestra:
- Cómo el rol afecta el formato de respuesta, nivel de detalle y tokens usados
- La tarea: categorizar tickets de soporte técnico por prioridad
- Variante 1: sin rol (solo instrucción directa)
- Variante 2: rol corto — "Eres un agente de soporte técnico."
- Variante 3: rol largo — persona detallada con experiencia, estilo y valores
- Métricas: categoría asignada, tokens usados, diferencias de estilo detectadas

Cómo ejecutar:
    make py SCRIPT=python/04-prompts/role-prompting.py

Qué esperar:
    Tres respuestas al mismo ticket de soporte con distintos niveles de rol.
    Comparacion de formato, detalle y tokens consumidos.

Variables de entorno:
    MODEL — modelo a usar (default: claude-sonnet-4-6)
"""

import os
import anthropic

MODEL = os.environ.get("MODEL", "claude-sonnet-4-6")

# ─── 1. Tickets de prueba ────────────────────────────────────────────────────

TICKETS = [
    {
        "id": "T-001",
        "text": (
            "Nuestro sistema de pagos dejó de funcionar hace 10 minutos. "
            "No podemos procesar ninguna transacción. Perdemos dinero cada segundo."
        ),
        "expected_priority": "urgente",
    },
    {
        "id": "T-002",
        "text": (
            "El botón de exportar a CSV en el módulo de reportes no funciona. "
            "Aparece un error 500. Lo necesitamos para el informe mensual del lunes."
        ),
        "expected_priority": "alta",
    },
    {
        "id": "T-003",
        "text": (
            "¿Podrían añadir un modo oscuro a la interfaz? "
            "Sería más cómodo para trabajar de noche."
        ),
        "expected_priority": "baja",
    },
    {
        "id": "T-004",
        "text": (
            "Necesito cambiar el correo de mi cuenta. "
            "He seguido los pasos de la documentación pero el botón de confirmar no aparece."
        ),
        "expected_priority": "media",
    },
    {
        "id": "T-005",
        "text": (
            "La aplicación móvil se cierra inesperadamente al intentar adjuntar archivos "
            "de más de 10 MB. Ocurre en iOS 17 y Android 14. "
            "Varios clientes nos han reportado esto esta semana."
        ),
        "expected_priority": "alta",
    },
]


# ─── 2. System prompts por variante ─────────────────────────────────────────

# Variante 1: Sin rol — solo instrucción directa
SYSTEM_NO_ROLE = """\
Categoriza tickets de soporte técnico por prioridad.

Las prioridades posibles son:
- urgente: el sistema está caído o hay pérdida económica inmediata
- alta: funcionalidad crítica bloqueada, afecta a múltiples usuarios
- media: funcionalidad degradada, hay workaround disponible
- baja: mejoras, preguntas o problemas menores sin impacto operativo

Responde con el siguiente formato JSON exacto:
{"prioridad": "<urgente|alta|media|baja>", "razon": "<una oración breve>"}"""


# Variante 2: Rol corto (~20 tokens)
SYSTEM_SHORT_ROLE = """\
Eres un agente de soporte técnico.

Categoriza tickets por prioridad.

Las prioridades posibles son:
- urgente: el sistema está caído o hay pérdida económica inmediata
- alta: funcionalidad crítica bloqueada, afecta a múltiples usuarios
- media: funcionalidad degradada, hay workaround disponible
- baja: mejoras, preguntas o problemas menores sin impacto operativo

Responde con el siguiente formato JSON exacto:
{"prioridad": "<urgente|alta|media|baja>", "razon": "<una oración breve>"}"""


# Variante 3: Rol largo (~150 tokens)
SYSTEM_LONG_ROLE = """\
Eres Elena Martínez, ingeniera de soporte técnico senior con 8 años de experiencia \
en plataformas SaaS B2B. Especialista en triaging de incidencias críticas, llevas \
el registro de tiempo de resolución más bajo del equipo. Tu filosofía: priorizar \
con precisión quirúrgica porque una mala priorización ralentiza todo el equipo. \
Eres directa, metódica y nunca escatimas en claridad al justificar una decisión. \
Conoces de memoria las SLAs del equipo: urgente=1h, alta=4h, media=24h, baja=72h.

Categoriza tickets por prioridad siguiendo los criterios SLA:
- urgente: el sistema está caído o hay pérdida económica inmediata (SLA: 1h)
- alta: funcionalidad crítica bloqueada, afecta a múltiples usuarios (SLA: 4h)
- media: funcionalidad degradada, hay workaround disponible (SLA: 24h)
- baja: mejoras, preguntas o problemas menores sin impacto operativo (SLA: 72h)

Responde con el siguiente formato JSON exacto:
{"prioridad": "<urgente|alta|media|baja>", "razon": "<una oración breve>"}"""


# ─── 3. Clasificación ────────────────────────────────────────────────────────

def classify_ticket(
    client: anthropic.Anthropic,
    system: str,
    ticket_text: str,
) -> dict:
    """Clasifica un ticket y devuelve resultado con métricas."""
    response = client.messages.create(
        model=MODEL,
        max_tokens=200,
        system=system,
        messages=[{"role": "user", "content": f"Ticket: {ticket_text}"}],
    )
    output = response.content[0].text.strip()

    # Extraer prioridad del JSON (tolerante a errores)
    import re, json
    match = re.search(r'\{.*\}', output, re.DOTALL)
    if match:
        try:
            data = json.loads(match.group())
            priority = data.get("prioridad", "desconocida")
            reason = data.get("razon", "")
        except json.JSONDecodeError:
            priority = "parse_error"
            reason = output
    else:
        priority = "no_json"
        reason = output

    return {
        "priority": priority,
        "reason": reason,
        "raw_output": output,
        "tokens_input": response.usage.input_tokens,
        "tokens_output": response.usage.output_tokens,
    }


# ─── 4. Análisis de estilo ───────────────────────────────────────────────────

def detect_style_markers(output: str) -> dict:
    """Detecta marcadores de estilo en el output."""
    output_lower = output.lower()
    return {
        "menciona_sla": any(w in output_lower for w in ["sla", "hora", "horas", "plazo"]),
        "usa_jerga_tecnica": any(w in output_lower for w in ["triaging", "incidencia", "escalad"]),
        "menciona_impacto": any(w in output_lower for w in ["usuario", "cliente", "pérdida", "impacto"]),
        "longitud_chars": len(output),
    }


# ─── 5. Impresión de resultados ──────────────────────────────────────────────

def print_ticket_comparison(ticket: dict, results: list[dict]):
    """Imprime comparación de las tres variantes para un ticket."""
    print(f"\n{'═' * 72}")
    print(f"  TICKET {ticket['id']}: {ticket['text'][:70]}...")
    print(f"  Prioridad esperada: {ticket['expected_priority']}")
    print(f"{'─' * 72}")

    for name, r in results:
        correct = "✓" if r["priority"] == ticket["expected_priority"] else "✗"
        markers = detect_style_markers(r["raw_output"])
        print(f"\n  [{name}] Prioridad: {r['priority']} {correct}")
        print(f"  Razón: {r['reason']}")
        print(f"  Tokens: {r['tokens_input']} input / {r['tokens_output']} output")
        print(f"  Estilo → SLA: {markers['menciona_sla']}, "
              f"Impacto: {markers['menciona_impacto']}, "
              f"Longitud: {markers['longitud_chars']} chars")


def print_summary(all_results: list, tickets: list):
    """Imprime tabla comparativa con métricas agregadas."""
    variant_names = [name for name, _ in all_results[0]]
    stats: dict = {v: {"correct": 0, "tokens_in": 0, "tokens_out": 0, "with_sla": 0} for v in variant_names}

    for ticket_idx, ticket_results in enumerate(all_results):
        ticket = tickets[ticket_idx]
        for name, r in ticket_results:
            if r["priority"] == ticket["expected_priority"]:
                stats[name]["correct"] += 1
            stats[name]["tokens_in"] += r["tokens_input"]
            stats[name]["tokens_out"] += r["tokens_output"]
            if detect_style_markers(r["raw_output"])["menciona_sla"]:
                stats[name]["with_sla"] += 1

    n = len(tickets)
    print(f"\n{'═' * 72}")
    print("  TABLA COMPARATIVA FINAL")
    print(f"{'═' * 72}")
    print(f"  {'Variante':<28} {'Accuracy':>10} {'Tokens/in':>10} {'Tokens/out':>12} {'Menciona SLA':>14}")
    print(f"  {'-' * 72}")
    for v, s in stats.items():
        print(
            f"  {v:<28} {s['correct']/n:>9.0%} "
            f"{s['tokens_in']/n:>9.0f} "
            f"{s['tokens_out']/n:>11.0f} "
            f"{s['with_sla']/n:>13.0%}"
        )

    print(f"\n  Observaciones clave:")
    print(f"  - El rol largo añade tokens de sistema pero puede cambiar el lenguaje de la razón")
    print(f"  - 'Menciona SLA' mide si el rol activa patrones de soporte profesional")
    print(f"  - Una alta accuracy con rol corto = el rol no añade valor semántico al resultado")
    print(f"  - Los tokens adicionales del rol largo son overhead puro si accuracy no mejora")


# ─── 6. Main ─────────────────────────────────────────────────────────────────

if __name__ == "__main__":
    client = anthropic.Anthropic()

    variants = [
        ("Sin rol",     SYSTEM_NO_ROLE),
        ("Rol corto",   SYSTEM_SHORT_ROLE),
        ("Rol largo",   SYSTEM_LONG_ROLE),
    ]

    all_results = []

    for ticket in TICKETS:
        ticket_results = []
        for name, system in variants:
            result = classify_ticket(client, system, ticket["text"])
            ticket_results.append((name, result))
        print_ticket_comparison(ticket, ticket_results)
        all_results.append(ticket_results)

    print_summary(all_results, TICKETS)
