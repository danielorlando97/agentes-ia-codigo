# Recuperación ante fallos: retry con backoff, circuit breaker, context overflow, JSON inválido
#
# Cómo ejecutar:
#   make py SCRIPT=python/17-produccion/recuperacion.py
#
# Qué esperar:
#   Demo de 4 tipos de fallo: API error transitorio (retry+backoff),
#   circuit breaker, context overflow y JSON invalido con auto-correccion.
#
# Variables de entorno:
#   MODEL — modelo a usar (default: claude-sonnet-4-6)

import json
import os
import random
import time
from dataclasses import dataclass, field
from datetime import datetime, timedelta
from typing import Callable, TypeVar, Optional
import anthropic

cliente = anthropic.Anthropic()
T = TypeVar("T")


# ─── Retry con backoff exponencial ───────────────────────────────────────────

def con_retry(
    func: Callable[[], T],
    max_intentos: int = 3,
    backoff_base: float = 1.0,
    errores_retriables: tuple = (anthropic.APIStatusError, anthropic.APIConnectionError),
) -> T:
    ultimo_error = None
    for intento in range(max_intentos):
        try:
            return func()
        except errores_retriables as e:
            ultimo_error = e
            if intento == max_intentos - 1:
                break
            espera = backoff_base * (2 ** intento) + random.uniform(0, 0.5)
            print(f"[retry] Intento {intento + 1} fallido ({type(e).__name__}). "
                  f"Reintentando en {espera:.1f}s...")
            time.sleep(espera)

    raise RuntimeError(f"Agotados {max_intentos} intentos") from ultimo_error


# ─── Circuit breaker para herramientas externas ───────────────────────────────

@dataclass
class CircuitBreaker:
    nombre: str
    umbral_fallos: int = 5
    ventana_reset_min: int = 2
    _fallos: list = field(default_factory=list, repr=False)
    _abierto: bool = field(default=False, repr=False)
    _abierto_desde: Optional[datetime] = field(default=None, repr=False)

    def _limpiar_fallos_antiguos(self) -> None:
        corte = datetime.now() - timedelta(minutes=self.ventana_reset_min)
        self._fallos = [t for t in self._fallos if t > corte]

    def registrar_exito(self) -> None:
        self._fallos.clear()
        self._abierto = False

    def registrar_fallo(self) -> None:
        self._fallos.append(datetime.now())
        self._limpiar_fallos_antiguos()
        if len(self._fallos) >= self.umbral_fallos:
            self._abierto = True
            self._abierto_desde = datetime.now()
            print(f"[circuit] {self.nombre}: circuito ABIERTO tras {self.umbral_fallos} fallos")

    def puede_intentar(self) -> bool:
        if not self._abierto:
            return True
        # Auto-reset después de la ventana
        if datetime.now() - self._abierto_desde > timedelta(minutes=self.ventana_reset_min):
            self._abierto = False
            self._fallos.clear()
            print(f"[circuit] {self.nombre}: circuito CERRADO (reset automático)")
            return True
        return False

    def ejecutar(self, func: Callable[[], T]) -> T:
        if not self.puede_intentar():
            raise RuntimeError(f"Circuito abierto para {self.nombre} — servicio no disponible")
        try:
            resultado = func()
            self.registrar_exito()
            return resultado
        except Exception as e:
            self.registrar_fallo()
            raise


_breakers: dict[str, CircuitBreaker] = {}


def obtener_breaker(nombre: str) -> CircuitBreaker:
    if nombre not in _breakers:
        _breakers[nombre] = CircuitBreaker(nombre=nombre)
    return _breakers[nombre]


def ejecutar_herramienta_segura(nombre: str, params: dict) -> str:
    """Ejecuta herramienta con circuit breaker; devuelve error como string (no lanza)."""
    breaker = obtener_breaker(nombre)
    try:
        return breaker.ejecutar(lambda: _herramienta_stub(nombre, params))
    except Exception as e:
        return f"Error: {e}"


def _herramienta_stub(nombre: str, params: dict) -> str:
    # Simula fallo intermitente para demo
    if random.random() < 0.4:
        raise ConnectionError(f"Servicio {nombre} no disponible")
    return f"Resultado de {nombre}({params})"


# ─── Compresión de contexto ───────────────────────────────────────────────────

VENTANA_TOKENS = 200_000
UMBRAL_COMPRESION = 0.75


def comprimir_si_necesario(mensajes: list, tokens_usados: int) -> list:
    if tokens_usados < VENTANA_TOKENS * UMBRAL_COMPRESION:
        return mensajes

    print(f"[context] Comprimiendo historial ({tokens_usados} tokens > "
          f"{VENTANA_TOKENS * UMBRAL_COMPRESION:.0f} umbral)")

    mensajes_antiguos = mensajes[1:-4]
    resumen_resp = cliente.messages.create(
        model=os.environ.get("SMALL_MODEL", "claude-haiku-4-5-20251001"),
        max_tokens=512,
        messages=[{
            "role": "user",
            "content": f"Resume este historial en 3-5 bullets:\n{json.dumps(mensajes_antiguos[:5])}",
        }],
    )
    resumen = resumen_resp.content[0].text

    return [
        mensajes[0],
        {"role": "assistant", "content": f"[Resumen de pasos anteriores: {resumen}]"},
        *mensajes[-4:],
    ]


# ─── Retry para output JSON mal formado ──────────────────────────────────────

def obtener_json_valido(prompt: str, schema_desc: str, max_intentos: int = 3) -> dict:
    mensajes = [{"role": "user", "content": f"{prompt}\n\nDevuelve JSON con: {schema_desc}"}]

    for intento in range(max_intentos):
        respuesta = cliente.messages.create(
            model=os.environ.get("MODEL", "claude-sonnet-4-6"),
            max_tokens=512,
            messages=mensajes,
        )
        texto = respuesta.content[0].text

        # Extraer JSON del texto (puede estar envuelto en ```json ... ```)
        texto_limpio = texto.strip()
        if "```" in texto_limpio:
            inicio = texto_limpio.find("{")
            fin = texto_limpio.rfind("}") + 1
            texto_limpio = texto_limpio[inicio:fin] if inicio >= 0 else texto_limpio

        try:
            return json.loads(texto_limpio)
        except json.JSONDecodeError as e:
            if intento == max_intentos - 1:
                raise RuntimeError(f"El modelo no produjo JSON válido en {max_intentos} intentos") from e

            mensajes += [
                {"role": "assistant", "content": texto},
                {"role": "user", "content":
                    f"Tu respuesta no es JSON válido. Error: {e}. "
                    f"Devuelve exactamente el JSON especificado, sin texto adicional."},
            ]
            print(f"[json_retry] Intento {intento + 1} fallido — retrying con feedback")

    raise RuntimeError("Loop sin resultado")


if __name__ == "__main__":
    print("=== Retry con backoff ===")
    intentos = [0]
    def llamada_que_falla_dos_veces():
        intentos[0] += 1
        if intentos[0] < 3:
            raise anthropic.APIConnectionError(request=None)  # type: ignore
        return cliente.messages.create(
            model=os.environ.get("SMALL_MODEL", "claude-haiku-4-5-20251001"),
            max_tokens=32,
            messages=[{"role": "user", "content": "Di 'ok'"}],
        )
    # con_retry(llamada_que_falla_dos_veces)  # descomenta para probar retry real

    print("=== Circuit breaker ===")
    for i in range(8):
        resultado = ejecutar_herramienta_segura("search_docs", {"q": "test"})
        print(f"  Intento {i+1}: {resultado[:60]}")

    print("\n=== JSON con retry ===")
    try:
        datos = obtener_json_valido(
            "Describe en JSON un agente simple",
            '{"nombre": str, "herramientas": list[str], "pasos_max": int}',
        )
        print(f"JSON válido recibido: {datos}")
    except RuntimeError as e:
        print(f"Error tras retries: {e}")
