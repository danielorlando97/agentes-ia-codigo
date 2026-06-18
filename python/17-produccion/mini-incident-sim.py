"""Mini-proyecto: El simulador de incidentes de producción.

Simula los 5 tipos de fallo de un agente en producción y ejecuta las
estrategias de recuperación correspondientes. Observa el comportamiento
del circuit breaker, el retry con jitter y el rollback de prompt.

Sin API key — todo el comportamiento es simulado localmente.

Uso:
    python mini-incident-sim.py
    python mini-incident-sim.py --fallo timeout
    python mini-incident-sim.py --fallo context_overflow
    python mini-incident-sim.py --fallo todos

Cómo ejecutar:
    make py SCRIPT=python/17-produccion/mini-incident-sim.py

Qué esperar:
    5 tipos de fallo simulados con sus estrategias de recuperación.
    Muestra circuit breaker, retry con jitter y rollback de prompt en acción.

Variables de entorno:
    MODEL — modelo a usar (default: claude-sonnet-4-6)
"""

import argparse
import math
import random
import time
from dataclasses import dataclass, field
from typing import Literal

# ── tipos ──────────────────────────────────────────────────────────────────────

TipoFallo = Literal[
    "timeout", "output_malformado", "context_overflow",
    "tool_fallo", "budget_excedido"
]


@dataclass
class Resultado:
    exito: bool
    intentos: int
    duracion_ms: float
    tokens_usados: int = 0
    error: str = ""
    estrategia: str = ""
    detalles: list[str] = field(default_factory=list)


# ── circuit breaker ────────────────────────────────────────────────────────────

@dataclass
class CircuitBreaker:
    nombre: str
    umbral_fallos: int = 3
    timeout_s: float = 30.0
    _fallos: int = 0
    _estado: str = "closed"  # closed | open | half-open
    _ultimo_fallo: float = 0.0

    def puede_pasar(self) -> bool:
        if self._estado == "closed":
            return True
        if self._estado == "open":
            if time.time() - self._ultimo_fallo > self.timeout_s:
                self._estado = "half-open"
                return True
            return False
        return True  # half-open: permite un intento

    def registrar_exito(self) -> None:
        self._fallos = 0
        self._estado = "closed"

    def registrar_fallo(self) -> None:
        self._fallos += 1
        self._ultimo_fallo = time.time()
        if self._fallos >= self.umbral_fallos:
            self._estado = "open"

    @property
    def estado(self) -> str:
        return self._estado


# ── estrategias de recuperación ───────────────────────────────────────────────

def jitter(base_ms: float, intento: int) -> float:
    """Backoff exponencial con jitter: base × 2^intento ± 25%."""
    espera = base_ms * (2 ** intento)
    jitter_val = espera * 0.25 * (random.random() * 2 - 1)
    return max(100, espera + jitter_val)


def recuperar_timeout(rng: random.Random, max_intentos: int = 3) -> Resultado:
    detalles = []
    for i in range(max_intentos):
        espera = jitter(500, i)
        detalles.append(f"  Intento {i+1}: espera {espera:.0f}ms antes de reintentar")
        exito = rng.random() > 0.4  # 60% de éxito tras el retry
        if exito:
            detalles.append(f"  ✓ Llamada LLM completada en intento {i+1}")
            return Resultado(exito=True, intentos=i+1, duracion_ms=1200 + i*800,
                           estrategia="retry_con_jitter", detalles=detalles)
    detalles.append("  ✗ Todos los reintentos agotados — escalando al supervisor")
    return Resultado(exito=False, intentos=max_intentos, duracion_ms=5000,
                   error="LLM timeout después de 3 reintentos", estrategia="retry_con_jitter",
                   detalles=detalles)


def recuperar_output_malformado(rng: random.Random) -> Resultado:
    detalles = []
    detalles.append("  Output recibido: '{\"hallazgos\": [broken json...'")
    detalles.append("  Detección: json.JSONDecodeError en línea 1")
    detalles.append("  Estrategia: feedback al modelo con el error exacto")

    prompt_recovery = "Tu respuesta anterior no era JSON válido. Error: JSONDecodeError. Responde SOLO con JSON válido usando el schema exacto."
    detalles.append(f"  Prompt de recuperación: '{prompt_recovery[:60]}...'")

    exito = rng.random() > 0.2  # 80% exito con el retry
    if exito:
        detalles.append("  ✓ Segundo intento produjo JSON válido")
        return Resultado(exito=True, intentos=2, duracion_ms=1400,
                       estrategia="feedback_al_modelo", detalles=detalles)
    detalles.append("  ✗ Segundo intento también malformado — usando output por defecto")
    return Resultado(exito=False, intentos=2, duracion_ms=1800,
                   error="Output malformado en 2 intentos", estrategia="feedback_al_modelo",
                   detalles=detalles)


def recuperar_context_overflow(tokens_actuales: int, ventana: int) -> Resultado:
    detalles = []
    uso_pct = tokens_actuales / ventana * 100
    detalles.append(f"  Contexto actual: {tokens_actuales:,} tokens ({uso_pct:.1f}% de {ventana:,})")

    umbral_compresion = 0.75
    if uso_pct > umbral_compresion * 100:
        tokens_objetivo = int(ventana * 0.6)
        tokens_liberados = tokens_actuales - tokens_objetivo
        detalles.append(f"  Umbral superado ({umbral_compresion*100:.0f}%) — aplicando clearing de tool results")
        detalles.append(f"  Tool results borrados: ~{tokens_liberados:,} tokens liberados")
        detalles.append(f"  Contexto resultante: {tokens_objetivo:,} tokens ({tokens_objetivo/ventana*100:.1f}%)")
        return Resultado(exito=True, intentos=1, duracion_ms=50,
                       tokens_usados=tokens_objetivo,
                       estrategia="compresion_proactiva_75pct",
                       detalles=detalles)

    detalles.append("  Contexto dentro de límites. No requiere compresión.")
    return Resultado(exito=True, intentos=1, duracion_ms=10,
                   tokens_usados=tokens_actuales, estrategia="ninguna", detalles=detalles)


def recuperar_tool_fallo(cb: CircuitBreaker, rng: random.Random) -> Resultado:
    detalles = []
    detalles.append(f"  Circuit breaker '{cb.nombre}': estado={cb.estado}, fallos={cb._fallos}")

    if not cb.puede_pasar():
        detalles.append(f"  ✗ Circuit breaker ABIERTO — herramienta no disponible")
        detalles.append("  Fallback: usando caché de resultado anterior o resultado por defecto")
        return Resultado(exito=False, intentos=0, duracion_ms=5,
                       error=f"Circuit breaker abierto para '{cb.nombre}'",
                       estrategia="circuit_breaker_fallback", detalles=detalles)

    detalles.append("  Circuit breaker cerrado — intentando llamada a herramienta")
    exito = rng.random() > 0.5
    if exito:
        cb.registrar_exito()
        detalles.append(f"  ✓ Herramienta '{cb.nombre}' respondió correctamente")
        return Resultado(exito=True, intentos=1, duracion_ms=200,
                       estrategia="circuit_breaker_normal", detalles=detalles)
    else:
        cb.registrar_fallo()
        detalles.append(f"  ✗ Fallo registrado — fallos acumulados: {cb._fallos}/{cb.umbral_fallos}")
        if cb.estado == "open":
            detalles.append(f"  Circuit breaker ahora ABIERTO — abre durante {cb.timeout_s:.0f}s")
        return Resultado(exito=False, intentos=1, duracion_ms=5000,
                       error="Tool timeout", estrategia="circuit_breaker_normal",
                       detalles=detalles)


def recuperar_budget_excedido(costo_actual: float, budget: float) -> Resultado:
    detalles = []
    detalles.append(f"  Costo acumulado: ${costo_actual:.4f} USD")
    detalles.append(f"  Budget de tarea: ${budget:.4f} USD")
    exceso = costo_actual - budget
    detalles.append(f"  Exceso: ${exceso:.4f} USD ({exceso/budget*100:.0f}% sobre budget)")

    if costo_actual > budget:
        detalles.append("  Estrategia: degradación a modelo económico para pasos restantes")
        detalles.append("  Haiku ($0.80/Mtok) reemplaza a Sonnet ($3.00/Mtok) — 3.75× más barato")
        costo_restante_estimado = exceso * 0.27  # ~73% ahorro
        detalles.append(f"  Ahorro estimado en pasos restantes: ${exceso - costo_restante_estimado:.4f}")
        return Resultado(exito=True, intentos=1, duracion_ms=0,
                       estrategia="model_downgrade_budget",
                       detalles=detalles)

    detalles.append("  Budget no excedido. Continuando normalmente.")
    return Resultado(exito=True, intentos=1, duracion_ms=0,
                   estrategia="ninguna", detalles=detalles)


# ── presentación ──────────────────────────────────────────────────────────────

def imprimir_resultado(tipo_fallo: str, resultado: Resultado) -> None:
    estado = "✓ RECUPERADO" if resultado.exito else "✗ FALLIDO"
    print(f"\n  {'─'*56}")
    print(f"  Fallo: {tipo_fallo.upper()}")
    print(f"  Estado: {estado}  |  Estrategia: {resultado.estrategia}")
    print(f"  Intentos: {resultado.intentos}  |  Duración: {resultado.duracion_ms:.0f}ms")
    if resultado.error:
        print(f"  Error final: {resultado.error}")
    print(f"\n  Traza de recuperación:")
    for d in resultado.detalles:
        print(f"  {d}")


def imprimir_resumen(resultados: dict[str, Resultado]) -> None:
    print(f"\n{'='*60}")
    print(f"  RESUMEN — Simulador de Incidentes de Producción")
    print(f"{'='*60}")
    recuperados = sum(1 for r in resultados.values() if r.exito)
    print(f"\n  {recuperados}/{len(resultados)} fallos recuperados automáticamente")
    print(f"\n  {'Fallo':<22} {'Estado':<16} {'Estrategia'}")
    print(f"  {'─'*56}")
    for tipo, r in resultados.items():
        estado = "RECUPERADO" if r.exito else "FALLIDO"
        print(f"  {tipo:<22} {estado:<16} {r.estrategia}")

    print(f"\n  Lecciones clave:")
    print("  • Timeout: retry con jitter previene thundering herd")
    print("  • Output malformado: feedback exacto al modelo (no reintentar ciegamente)")
    print("  • Context overflow: compresión al 75% de uso, no al 100%")
    print("  • Tool fallo: circuit breaker evita cascada de timeouts")
    print("  • Budget excedido: degradar modelo antes de abortar la tarea")
    print(f"{'='*60}")


# ── main ──────────────────────────────────────────────────────────────────────

def main():
    parser = argparse.ArgumentParser(description="Simula fallos de producción y estrategias de recuperación.")
    parser.add_argument(
        "--fallo",
        choices=["timeout", "output_malformado", "context_overflow", "tool_fallo", "budget_excedido", "todos"],
        default="todos",
        help="Tipo de fallo a simular (default: todos)",
    )
    args = parser.parse_args()

    rng = random.Random(42)
    cb = CircuitBreaker(nombre="search_docs", umbral_fallos=3)

    print(f"\n{'='*60}")
    print(f"  SIMULADOR DE INCIDENTES DE PRODUCCIÓN")
    print(f"  Fallo: {args.fallo}")
    print(f"{'='*60}")

    fallos_a_simular = (
        ["timeout", "output_malformado", "context_overflow", "tool_fallo", "budget_excedido"]
        if args.fallo == "todos"
        else [args.fallo]
    )

    resultados = {}
    for fallo in fallos_a_simular:
        if fallo == "timeout":
            r = recuperar_timeout(rng)
        elif fallo == "output_malformado":
            r = recuperar_output_malformado(rng)
        elif fallo == "context_overflow":
            # simula un agente con 7200 tokens en ventana de 8192
            r = recuperar_context_overflow(tokens_actuales=7200, ventana=8192)
        elif fallo == "tool_fallo":
            # simula 4 fallos consecutivos para disparar el circuit breaker
            for _ in range(3):
                cb.registrar_fallo()
            r = recuperar_tool_fallo(cb, rng)
        elif fallo == "budget_excedido":
            r = recuperar_budget_excedido(costo_actual=0.082, budget=0.06)
        else:
            continue

        imprimir_resultado(fallo, r)
        resultados[fallo] = r

    if len(resultados) > 1:
        imprimir_resumen(resultados)


if __name__ == "__main__":
    main()
