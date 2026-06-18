"""Construir un system prompt modular y medir el efecto del prompt caching.

Demuestra:
- System prompt con 5 bloques: identidad, instrucciones, herramientas, restricciones, ejemplos
- Bloque estático con cache_control para los bloques que no cambian entre requests
- Bloque dinámico sin cache para fecha y estado de sesión
- Medir tokens cacheados vs no cacheados: cache_creation_input_tokens, cache_read_input_tokens
- Calcular ahorro de tokens en un batch de 10 requests con el mismo system prompt

Cómo ejecutar:
    make py SCRIPT=python/04-prompts/anatomia-system-prompt.py

Qué esperar:
    Comparacion de tokens cacheados vs no cacheados en 10 requests consecutivos.
    Muestra el ahorro real de prompt caching en tokens y USD.

Variables de entorno:
    MODEL — modelo a usar (default: claude-sonnet-4-6)
"""

from datetime import datetime
import os
import anthropic

MODEL = os.environ.get("MODEL", "claude-sonnet-4-6")

# ─── 1. Bloques estáticos del system prompt ───────────────────────────────────
# Estos bloques no cambian entre requests → se cachean.

BLOQUE_IDENTIDAD = """\
Eres TechBot, el asistente de soporte técnico de TechStore.
Tu única función es resolver dudas sobre los productos y servicios de TechStore.
Eres directo, preciso y siempre confirmas si has entendido bien la pregunta antes de responder."""

BLOQUE_INSTRUCCIONES = """\
<instrucciones>
  Antes de responder, verifica que la pregunta está dentro de tu dominio (productos TechStore).
  Si la pregunta es sobre facturación, deriva al equipo de ventas sin dar precios.
  Si la pregunta es técnica, intenta resolverla en máximo 3 pasos.
  Termina siempre con: ¿Te ha sido útil esta respuesta?
</instrucciones>"""

BLOQUE_HERRAMIENTAS = """\
<herramientas_disponibles>
  - buscar_producto(nombre): busca información de un producto en el catálogo
  - consultar_estado_pedido(id_pedido): devuelve el estado de un pedido
  - crear_ticket_soporte(descripcion, prioridad): abre un ticket en el sistema
  Nota: no tienes acceso a información de precios ni a cuentas de usuario.
</herramientas_disponibles>"""

BLOQUE_RESTRICCIONES = """\
<restricciones>
  NUNCA inventes precios. Si se pregunta por un precio, di: "No tengo ese dato. Contacta con ventas."
  NUNCA compartas información personal de otros clientes.
  NUNCA ejecutes acciones destructivas (cancelar pedidos, eliminar datos).
  Solo responde preguntas sobre TechStore. Fuera de dominio: redirige amablemente.
</restricciones>"""

BLOQUE_EJEMPLOS = """\
<ejemplos>
  <ejemplo>
    <usuario>Mi pedido #12345 no ha llegado</usuario>
    <asistente>Entendido. Consultaré el estado de tu pedido. ¿Tienes el número de seguimiento del transportista? ¿Te ha sido útil esta respuesta?</asistente>
  </ejemplo>
  <ejemplo>
    <usuario>¿Cuánto cuesta el Laptop ProX?</usuario>
    <asistente>No tengo acceso a información de precios actualizada. Por favor contacta con nuestro equipo de ventas en ventas@techstore.com. ¿Te ha sido útil esta respuesta?</asistente>
  </ejemplo>
</ejemplos>"""

# ─── 2. Construcción del system prompt modular ──────────────────────────────

def build_system_prompt_cached(dynamic_info: str) -> list[dict]:
    """
    Construye el system prompt como lista de bloques con caching.

    Los bloques estáticos llevan cache_control: el primero en ser cacheado
    paga cache_creation_input_tokens; los siguientes pagan cache_read_input_tokens.

    El bloque dinámico (fecha, estado de sesión) no se cachea porque cambia
    en cada request. Mantenerlo separado es lo que permite el caching efectivo
    de los otros bloques.
    """
    static_content = "\n\n".join([
        BLOQUE_IDENTIDAD,
        BLOQUE_INSTRUCCIONES,
        BLOQUE_HERRAMIENTAS,
        BLOQUE_RESTRICCIONES,
        BLOQUE_EJEMPLOS,
    ])

    return [
        {
            "type": "text",
            "text": static_content,
            "cache_control": {"type": "ephemeral"},  # TTL: 5 minutos, se renueva en cada hit
        },
        {
            "type": "text",
            "text": dynamic_info,
            # Sin cache_control: este bloque siempre paga costo completo
        },
    ]


def build_system_prompt_no_cache(dynamic_info: str) -> str:
    """
    Construye el mismo system prompt pero como string único (sin caching).
    Se usa para comparar el costo sin la optimización.
    """
    return "\n\n".join([
        BLOQUE_IDENTIDAD,
        BLOQUE_INSTRUCCIONES,
        BLOQUE_HERRAMIENTAS,
        BLOQUE_RESTRICCIONES,
        BLOQUE_EJEMPLOS,
        dynamic_info,
    ])


# ─── 3. Batch de requests ───────────────────────────────────────────────────

QUESTIONS = [
    "¿Dónde puedo ver el estado de mi pedido #45678?",
    "El adaptador HDMI que compré no funciona con mi televisor Samsung.",
    "¿Tienen garantía extendida para laptops?",
    "Necesito abrir un ticket porque recibí el producto equivocado.",
    "¿Cómo puedo devolver un artículo defectuoso?",
    "La aplicación de TechStore no me deja iniciar sesión.",
    "¿Tienen repuestos para el teclado MechType K85?",
    "Mi factura del mes pasado tiene un error de importe.",
    "¿Cuánto tiempo tarda el envío estándar?",
    "El mouse inalámbrico pierde conexión constantemente.",
]


def run_batch_cached(client: anthropic.Anthropic, questions: list[str]) -> list[dict]:
    """Ejecuta el batch usando system prompt con caching."""
    results = []
    for i, question in enumerate(questions):
        dynamic_info = (
            f"Fecha y hora: {datetime.now().strftime('%Y-%m-%d %H:%M:%S')}\n"
            f"ID de sesión: session-demo-{i+1:04d}"
        )
        system = build_system_prompt_cached(dynamic_info)
        response = client.messages.create(
            model=MODEL,
            max_tokens=200,
            system=system,
            messages=[{"role": "user", "content": question}],
        )
        usage = response.usage
        results.append({
            "question_idx": i + 1,
            "question": question[:50],
            "input_tokens": usage.input_tokens,
            "output_tokens": usage.output_tokens,
            "cache_creation_tokens": getattr(usage, "cache_creation_input_tokens", None) or 0,
            "cache_read_tokens": getattr(usage, "cache_read_input_tokens", None) or 0,
        })
        print(f"  Request {i+1:2d}/{len(questions)}: "
              f"input={usage.input_tokens:5d}, "
              f"cache_write={getattr(usage, 'cache_creation_input_tokens', None) or 0:5d}, "
              f"cache_read={getattr(usage, 'cache_read_input_tokens', None) or 0:5d}")
    return results


def run_batch_no_cache(client: anthropic.Anthropic, questions: list[str]) -> list[dict]:
    """Ejecuta el mismo batch sin caching (para comparación)."""
    results = []
    for i, question in enumerate(questions):
        dynamic_info = (
            f"Fecha y hora: {datetime.now().strftime('%Y-%m-%d %H:%M:%S')}\n"
            f"ID de sesión: session-demo-{i+1:04d}"
        )
        system_str = build_system_prompt_no_cache(dynamic_info)
        response = client.messages.create(
            model=MODEL,
            max_tokens=200,
            system=system_str,
            messages=[{"role": "user", "content": question}],
        )
        usage = response.usage
        results.append({
            "question_idx": i + 1,
            "question": question[:50],
            "input_tokens": usage.input_tokens,
            "output_tokens": usage.output_tokens,
            "cache_creation_tokens": 0,
            "cache_read_tokens": 0,
        })
    return results


# ─── 4. Análisis de resultados ───────────────────────────────────────────────

def analyze_savings(cached_results: list[dict], no_cache_results: list[dict]):
    """Calcula y muestra el ahorro por el caching."""
    total_input_cached = sum(r["input_tokens"] for r in cached_results)
    total_input_no_cache = sum(r["input_tokens"] for r in no_cache_results)
    total_cache_writes = sum(r["cache_creation_tokens"] for r in cached_results)
    total_cache_reads = sum(r["cache_read_tokens"] for r in cached_results)

    # En Anthropic, cache_read cuesta ~10% del precio de input estándar
    # cache_creation cuesta ~125% del precio de input estándar
    # Usando precio de claude-sonnet-4-6: $3/MTok input, $0.30/MTok cache read
    price_input_per_mtok = 3.0
    price_cache_write_per_mtok = 3.75   # 125% de input
    price_cache_read_per_mtok = 0.30    # 10% de input

    cost_no_cache = (total_input_no_cache / 1_000_000) * price_input_per_mtok
    cost_cached = (
        (total_cache_writes / 1_000_000) * price_cache_write_per_mtok
        + (total_cache_reads / 1_000_000) * price_cache_read_per_mtok
        + ((total_input_cached - total_cache_reads) / 1_000_000) * price_input_per_mtok
    )

    n = len(cached_results)

    print(f"\n{'═' * 68}")
    print("  ANÁLISIS DE TOKENS Y AHORRO POR CACHING")
    print(f"{'═' * 68}")
    print(f"\n  Batch: {n} requests con el mismo system prompt estático")
    print(f"\n  {'Métrica':<45} {'Sin cache':>10} {'Con cache':>10}")
    print(f"  {'-' * 67}")
    print(f"  {'Tokens input totales':<45} {total_input_no_cache:>10,} {total_input_cached:>10,}")
    print(f"  {'Tokens cache_creation (escritura)':<45} {'—':>10} {total_cache_writes:>10,}")
    print(f"  {'Tokens cache_read (lectura)':<45} {'—':>10} {total_cache_reads:>10,}")
    print(f"  {'Tokens input promedio por request':<45} {total_input_no_cache/n:>10,.0f} {total_input_cached/n:>10,.0f}")
    print(f"\n  {'Costo estimado del batch (USD)':<45} ${cost_no_cache:>9.4f} ${cost_cached:>9.4f}")

    if cost_no_cache > 0:
        savings_pct = (1 - cost_cached / cost_no_cache) * 100
        savings_abs = cost_no_cache - cost_cached
        print(f"\n  Ahorro: ${savings_abs:.4f} USD ({savings_pct:.1f}%)")

    print(f"\n  Desglose por request (con cache):")
    print(f"  {'Req':>4}  {'Input':>7}  {'Cache write':>12}  {'Cache read':>11}")
    print(f"  {'-' * 40}")
    for r in cached_results:
        print(f"  {r['question_idx']:>4}  {r['input_tokens']:>7}  "
              f"{r['cache_creation_tokens']:>12}  {r['cache_read_tokens']:>11}")

    print(f"\n  Notas:")
    print(f"  - Request 1: paga cache_creation (escribir el cache por primera vez)")
    print(f"  - Requests 2+: pagan cache_read (~10% del precio de input estándar)")
    print(f"  - El TTL del cache es 5 minutos. Se renueva en cada hit.")
    print(f"  - Solo el bloque estático se cachea; el bloque dinámico paga costo completo.")


# ─── 5. Main ─────────────────────────────────────────────────────────────────

if __name__ == "__main__":
    client = anthropic.Anthropic()

    print("Midiendo tokens del system prompt estático...")
    static_content = "\n\n".join([
        BLOQUE_IDENTIDAD,
        BLOQUE_INSTRUCCIONES,
        BLOQUE_HERRAMIENTAS,
        BLOQUE_RESTRICCIONES,
        BLOQUE_EJEMPLOS,
    ])
    print(f"  Caracteres del bloque estático: {len(static_content)}")
    print(f"  Estimación de tokens (~4 chars/token): {len(static_content) // 4}")

    print(f"\n{'═' * 68}")
    print("  BATCH CON CACHING (10 requests)")
    print(f"{'═' * 68}")
    cached_results = run_batch_cached(client, QUESTIONS)

    print(f"\n{'═' * 68}")
    print("  BATCH SIN CACHING (10 requests) — para comparación")
    print(f"{'═' * 68}")
    no_cache_results = run_batch_no_cache(client, QUESTIONS)

    analyze_savings(cached_results, no_cache_results)
