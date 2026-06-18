"""Router en cascada: keyword → Jaccard → LLM.

Tres mecanismos ordenados por costo creciente. El router intenta cada
capa en orden; solo sube a la siguiente si la actual no produce match.

Requiere: pip install anthropic

Cómo ejecutar:
    make py SCRIPT=python/10-decisiones/routing.py

Qué esperar:
    Cada query se resuelve en la capa mas barata posible.
    Keyword: O(1). Jaccard: O(palabras). LLM: solo si las anteriores no matchean.
    Muestra qué capa resolvió cada query y el costo total.

Variables de entorno:
    MODEL — modelo para el nivel LLM (default: claude-sonnet-4-6)
"""
import os
import json
import re
from dataclasses import dataclass, field

import anthropic

MODEL = os.environ.get("MODEL", "claude-sonnet-4-6")
JACCARD_THRESHOLD = 0.15

PROMPT_ROUTER = """\
Clasifica el siguiente input en una de estas rutas:
{rutas}
- DEFAULT: ninguna de las anteriores

Input: {input}

Responde ÚNICAMENTE con JSON válido:
{{"destination": "<nombre_ruta>", "next_inputs": "<input reformulado si aplica, sino igual al original>"}}"""


@dataclass
class Route:
    name: str
    description: str
    keywords: list[str]
    examples: list[str]


DEFAULT_ROUTE = Route(
    name="DEFAULT",
    description="Ruta de fallback para inputs que no encajan en ninguna especialización",
    keywords=[],
    examples=[],
)


def jaccard(text_a: str, text_b: str) -> float:
    words_a = set(text_a.lower().split())
    words_b = set(text_b.lower().split())
    union = words_a | words_b
    if not union:
        return 0.0
    return len(words_a & words_b) / len(union)


def router_keyword(user_input: str, routes: list[Route]) -> Route | None:
    lower = user_input.lower()
    for route in routes:
        if any(kw.lower() in lower for kw in route.keywords):
            return route
    return None


def router_jaccard(user_input: str, routes: list[Route]) -> Route | None:
    scores = [
        (route, max((jaccard(user_input, ex) for ex in route.examples), default=0.0))
        for route in routes
    ]
    best_route, best_score = max(scores, key=lambda x: x[1])
    if best_score >= JACCARD_THRESHOLD:
        return best_route
    return None


def router_llm(user_input: str, routes: list[Route], client: anthropic.Anthropic) -> Route:
    route_list = "\n".join(f"- {r.name}: {r.description}" for r in routes)
    prompt = PROMPT_ROUTER.format(rutas=route_list, input=user_input)

    response = client.messages.create(
        model=MODEL,
        max_tokens=256,
        messages=[{"role": "user", "content": prompt}],
    )
    text = response.content[0].text.strip()

    # El LLM puede rodear el JSON con markdown — extraemos solo el objeto
    m = re.search(r"\{.*\}", text, re.DOTALL)
    if not m:
        return DEFAULT_ROUTE

    parsed = json.loads(m.group())
    destination = parsed.get("destination", "DEFAULT")

    route_map = {r.name: r for r in routes}
    return route_map.get(destination, DEFAULT_ROUTE)


def cascade_router(
    user_input: str,
    routes: list[Route],
    client: anthropic.Anthropic,
) -> tuple[Route, str]:
    """Retorna (ruta seleccionada, mecanismo usado)."""
    route = router_keyword(user_input, routes)
    if route:
        return route, "keyword"

    route = router_jaccard(user_input, routes)
    if route:
        return route, "jaccard"

    route = router_llm(user_input, routes, client)
    return route, "llm"


# --- Demo ---

ROUTES = [
    Route(
        name="soporte_tecnico",
        description="Problemas técnicos con el producto, errores, bugs, configuración",
        keywords=["error", "falla", "bug", "no funciona", "excepción", "crash"],
        examples=[
            "el endpoint de autenticación devuelve 500",
            "no puedo conectarme a la API",
            "el SDK lanza una excepción al inicializar",
            "la integración de webhook falla con timeout",
        ],
    ),
    Route(
        name="facturacion",
        description="Preguntas sobre pagos, facturas, planes, precios, suscripciones",
        keywords=["factura", "pago", "precio", "suscripción", "plan", "cobro"],
        examples=[
            "quiero cambiar mi plan de facturación mensual a anual",
            "no me llegó la factura de este mes",
            "cómo cancelo mi suscripción",
            "cuánto cuesta el plan enterprise",
        ],
    ),
    Route(
        name="general",
        description="Preguntas generales sobre la empresa, el producto, horarios, contacto",
        keywords=["horario", "contacto", "email", "teléfono", "dirección"],
        examples=[
            "cuál es el horario de atención al cliente",
            "cómo puedo contactar con soporte por correo",
            "dónde están ubicadas las oficinas",
        ],
    ),
]


def main() -> None:
    client = anthropic.Anthropic()

    queries = [
        # keyword match directo
        "Tengo un bug en el SDK que hace crash la app al iniciar",
        # Jaccard: vocabulario cercano a facturacion sin keyword exacta
        "necesito cambiar cómo me cobran cada mes al plan anual",
        # LLM: semánticamente específico, sin keywords ni vocabulario solapado
        "¿cuánto tiempo lleva aproximadamente resolver una disputa de cargo?",
    ]

    print("=== Router en cascada ===\n")
    for query in queries:
        route, mechanism = cascade_router(query, ROUTES, client)
        print(f"Input    : {query}")
        print(f"Ruta     : {route.name}")
        print(f"Mecanismo: {mechanism}")
        print()


if __name__ == "__main__":
    main()
