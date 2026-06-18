"""
Router — LLM clasifica un mensaje en una de N rutas predefinidas.

Qué demuestra:
    Nivel ★☆☆ del espectro de autonomia: el LLM elige UNA ruta entre
    varias escritas en codigo. Sin loop, sin tools — 1 llamada al LLM,
    luego el codigo ejecuta el handler correspondiente.
    La "agencia" es minima: el LLM decide a donde va el flujo, pero el
    codigo define todas las rutas posibles.

Patron clave:
    System prompt lista las rutas exactas.
    max_tokens=16 fuerza una respuesta corta (solo el nombre de la ruta).
    Validacion: si el LLM devuelve algo no esperado, cae a "otro".

Cómo ejecutar:
    make py SCRIPT=python/01-que-es-un-agente/agente-router.py

Qué esperar:
    4 casos de prueba, cada uno muestra la ruta elegida y el mensaje original.
    Ej: "facturacion        No me llego la factura de marzo"

Variables de entorno:
    MODEL — modelo a usar (default: claude-sonnet-4-6)
"""
import os
import anthropic

MODEL = os.environ.get("MODEL", "claude-sonnet-4-6")
RUTAS = ["facturacion", "soporte_tecnico", "ventas", "otro"]
SYSTEM = (
    "Clasifica el mensaje del usuario en exactamente una de estas rutas: "
    + ", ".join(RUTAS)
    + ". Responde solo con el nombre de la ruta, sin explicacion ni puntuacion."
)


def route(user_input: str) -> str:
    client = anthropic.Anthropic()
    response = client.messages.create(
        model=MODEL,
        max_tokens=16,
        system=SYSTEM,
        messages=[{"role": "user", "content": user_input}],
    )
    text = "".join(b.text for b in response.content if b.type == "text").strip().lower()
    return text if text in RUTAS else "otro"


if __name__ == "__main__":
    cases = [
        "No me llego la factura de marzo",
        "El servicio se cae cada vez que entro",
        "Quiero cambiar al plan empresarial",
        "Hace buen tiempo hoy",
    ]
    for c in cases:
        print(f"{route(c):<18}  {c}")
