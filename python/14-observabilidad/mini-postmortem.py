"""Mini-proyecto: El post-mortem automatizado.

Genera un historial de trazas simuladas de un agente, introduce
anomalías conocidas, y ejecuta un análisis automatizado que detecta
los problemas y produce un reporte de post-mortem.

Sin API key — todo el análisis es local con reglas deterministas.

Uso:
    python mini-postmortem.py
    python mini-postmortem.py --incidente latencia
    python mini-postmortem.py --incidente costos
    python mini-postmortem.py --incidente loop_infinito
    python mini-postmortem.py --incidente todos

Cómo ejecutar:
    make py SCRIPT=python/14-observabilidad/mini-postmortem.py

Qué esperar:
    Historial de trazas con anomalías conocidas. El análisis automatizado
    detecta los problemas y genera un reporte de post-mortem estructurado.

Variables de entorno:
    MODEL — modelo juez para el análisis (default: claude-sonnet-4-6)
"""

import argparse
import json
import random
from dataclasses import dataclass, field
from typing import Literal

# ── tipos ──────────────────────────────────────────────────────────────────────

@dataclass
class Span:
    span_id: str
    tipo: Literal["llm_call", "tool_call", "session"]
    nombre: str
    duracion_ms: float
    tokens_input: int = 0
    tokens_output: int = 0
    finish_reason: str = "end_turn"
    tool_name: str = ""
    tool_success: bool = True
    error: str = ""
    turno: int = 0


@dataclass
class Sesion:
    session_id: str
    turnos: int
    spans: list[Span] = field(default_factory=list)
    completada: bool = True

    def tokens_totales(self) -> int:
        return sum(s.tokens_input + s.tokens_output for s in self.spans)

    def duracion_total_ms(self) -> float:
        return sum(s.duracion_ms for s in self.spans)

    def costo_usd(self, precio_input=3.0, precio_output=15.0) -> float:
        ti = sum(s.tokens_input for s in self.spans)
        to = sum(s.tokens_output for s in self.spans)
        return (ti * precio_input + to * precio_output) / 1_000_000


# ── generador de trazas ────────────────────────────────────────────────────────

def span_id(n: int) -> str:
    return f"span_{n:04d}"


def generar_sesion_normal(session_id: str, rng: random.Random, n: int) -> Sesion:
    turnos = rng.randint(3, 8)
    spans = []
    k = 0
    for t in range(turnos):
        llm = Span(
            span_id=span_id(n * 100 + k), tipo="llm_call", nombre="claude",
            duracion_ms=rng.uniform(800, 1800),
            tokens_input=rng.randint(800, 2000),
            tokens_output=rng.randint(100, 600),
            finish_reason="end_turn", turno=t,
        )
        spans.append(llm)
        k += 1
        if rng.random() < 0.6:
            tool = Span(
                span_id=span_id(n * 100 + k), tipo="tool_call",
                nombre="tool_call", tool_name=rng.choice(["search_docs", "read_file", "run_code"]),
                duracion_ms=rng.uniform(50, 300), tool_success=True, turno=t,
            )
            spans.append(tool)
            k += 1
    return Sesion(session_id=session_id, turnos=turnos, spans=spans, completada=True)


def inyectar_latencia_alta(sesion: Sesion, rng: random.Random) -> Sesion:
    for span in sesion.spans:
        if span.tipo == "llm_call" and rng.random() < 0.4:
            span.duracion_ms = rng.uniform(12_000, 25_000)  # >10s
    return sesion


def inyectar_costos_altos(sesion: Sesion, rng: random.Random) -> Sesion:
    for span in sesion.spans:
        if span.tipo == "llm_call":
            span.tokens_input = rng.randint(18_000, 35_000)  # contexto inflado
    return sesion


def inyectar_loop_infinito(sesion: Sesion, rng: random.Random) -> Sesion:
    turno_base = sesion.turnos
    k = len(sesion.spans) * 10
    # el agente repite max_tokens muchas veces sin terminar
    for extra in range(12):
        sesion.spans.append(Span(
            span_id=span_id(k + extra), tipo="llm_call", nombre="claude",
            duracion_ms=rng.uniform(800, 1600),
            tokens_input=rng.randint(5000, 8000),
            tokens_output=600,
            finish_reason="max_tokens",  # señal del loop
            turno=turno_base + extra,
        ))
    sesion.turnos += 12
    sesion.completada = False
    return sesion


def inyectar_tool_failures(sesion: Sesion, rng: random.Random) -> Sesion:
    for span in sesion.spans:
        if span.tipo == "tool_call" and rng.random() < 0.7:
            span.tool_success = False
            span.error = "ConnectionError: timeout after 5s"
    return sesion


def generar_historial(n_sesiones: int, incidente: str, rng: random.Random) -> list[Sesion]:
    sesiones = []
    for i in range(n_sesiones):
        s = generar_sesion_normal(f"sess_{i:03d}", rng, i)
        if incidente in ("latencia", "todos") and i >= n_sesiones // 2:
            s = inyectar_latencia_alta(s, rng)
        if incidente in ("costos", "todos") and i >= n_sesiones // 3:
            s = inyectar_costos_altos(s, rng)
        if incidente in ("loop_infinito", "todos") and i >= n_sesiones * 2 // 3:
            s = inyectar_loop_infinito(s, rng)
        if incidente in ("tool_failures", "todos") and i >= n_sesiones // 4:
            s = inyectar_tool_failures(s, rng)
        sesiones.append(s)
    return sesiones


# ── análisis de post-mortem ───────────────────────────────────────────────────

@dataclass
class Hallazgo:
    tipo: str
    severidad: Literal["info", "warning", "critical"]
    descripcion: str
    metrica: str
    umbral: str
    valor_observado: str
    sesiones_afectadas: int


def analizar_latencia(sesiones: list[Sesion]) -> list[Hallazgo]:
    hallazgos = []
    duraciones = []
    sesiones_lentas = 0
    for s in sesiones:
        for span in s.spans:
            if span.tipo == "llm_call":
                duraciones.append(span.duracion_ms)
                if span.duracion_ms > 10_000:
                    sesiones_lentas += 1

    if not duraciones:
        return []
    p95 = sorted(duraciones)[int(len(duraciones) * 0.95)]
    media = sum(duraciones) / len(duraciones)

    if p95 > 8_000:
        hallazgos.append(Hallazgo(
            tipo="latencia_p95",
            severidad="critical" if p95 > 15_000 else "warning",
            descripcion=f"P95 de latencia LLM supera umbral operacional.",
            metrica="llm_call.duration_ms p95",
            umbral="< 5,000ms",
            valor_observado=f"{p95:,.0f}ms",
            sesiones_afectadas=sesiones_lentas,
        ))
    if media > 5_000:
        hallazgos.append(Hallazgo(
            tipo="latencia_media",
            severidad="warning",
            descripcion="Latencia media LLM elevada — degradación de experiencia.",
            metrica="llm_call.duration_ms mean",
            umbral="< 2,000ms",
            valor_observado=f"{media:,.0f}ms",
            sesiones_afectadas=sesiones_lentas,
        ))
    return hallazgos


def analizar_costos(sesiones: list[Sesion]) -> list[Hallazgo]:
    hallazgos = []
    costos = [s.costo_usd() for s in sesiones]
    tokens_input_medios = []
    for s in sesiones:
        for span in s.spans:
            if span.tipo == "llm_call":
                tokens_input_medios.append(span.tokens_input)

    costo_medio = sum(costos) / max(len(costos), 1)
    tok_medio = sum(tokens_input_medios) / max(len(tokens_input_medios), 1)

    if costo_medio > 0.05:
        hallazgos.append(Hallazgo(
            tipo="costo_por_sesion",
            severidad="critical" if costo_medio > 0.10 else "warning",
            descripcion="Costo por sesión supera presupuesto operacional.",
            metrica="session.cost_usd mean",
            umbral="< $0.05",
            valor_observado=f"${costo_medio:.4f}",
            sesiones_afectadas=sum(1 for c in costos if c > 0.05),
        ))
    if tok_medio > 10_000:
        hallazgos.append(Hallazgo(
            tipo="contexto_inflado",
            severidad="warning",
            descripcion="Tokens de input por llamada LLM anormalmente alto — historial sin compactar.",
            metrica="llm_call.tokens_input mean",
            umbral="< 5,000",
            valor_observado=f"{tok_medio:,.0f}",
            sesiones_afectadas=sum(1 for t in tokens_input_medios if t > 10_000),
        ))
    return hallazgos


def analizar_loop(sesiones: list[Sesion]) -> list[Hallazgo]:
    hallazgos = []
    sesiones_max_tokens = 0
    sesiones_incompletas = 0
    turnos_excesivos = []

    for s in sesiones:
        max_tok_count = sum(1 for span in s.spans if span.finish_reason == "max_tokens")
        if max_tok_count > 3:
            sesiones_max_tokens += 1
        if not s.completada:
            sesiones_incompletas += 1
        if s.turnos > 15:
            turnos_excesivos.append(s.turnos)

    if sesiones_max_tokens > 0:
        hallazgos.append(Hallazgo(
            tipo="loop_max_tokens",
            severidad="critical",
            descripcion="Sesiones con múltiples finish_reason=max_tokens — probable loop sin condición de salida.",
            metrica="llm_call.finish_reason == max_tokens count",
            umbral="< 1 por sesión",
            valor_observado=f"{sesiones_max_tokens} sesiones con >3 max_tokens",
            sesiones_afectadas=sesiones_max_tokens,
        ))
    if sesiones_incompletas > 0:
        hallazgos.append(Hallazgo(
            tipo="sesiones_incompletas",
            severidad="critical",
            descripcion="Sesiones no completadas — agente terminó por agotamiento de recursos.",
            metrica="session.completada",
            umbral="100%",
            valor_observado=f"{sesiones_incompletas}/{len(sesiones)} incompletas",
            sesiones_afectadas=sesiones_incompletas,
        ))
    if turnos_excesivos:
        hallazgos.append(Hallazgo(
            tipo="turnos_excesivos",
            severidad="warning",
            descripcion="Sesiones con número de turnos anormalmente alto.",
            metrica="session.turnos max",
            umbral="< 15",
            valor_observado=f"max={max(turnos_excesivos)} turnos",
            sesiones_afectadas=len(turnos_excesivos),
        ))
    return hallazgos


def analizar_tools(sesiones: list[Sesion]) -> list[Hallazgo]:
    hallazgos = []
    total_tool_calls = 0
    tool_failures = 0

    for s in sesiones:
        for span in s.spans:
            if span.tipo == "tool_call":
                total_tool_calls += 1
                if not span.tool_success:
                    tool_failures += 1

    if total_tool_calls == 0:
        return []

    tasa_fallo = tool_failures / total_tool_calls * 100
    if tasa_fallo > 20:
        hallazgos.append(Hallazgo(
            tipo="tool_failure_rate",
            severidad="critical" if tasa_fallo > 50 else "warning",
            descripcion="Tasa de fallos de herramientas por encima de umbral operacional.",
            metrica="tool_call.success_rate",
            umbral="> 95%",
            valor_observado=f"{100-tasa_fallo:.1f}% ({tool_failures}/{total_tool_calls})",
            sesiones_afectadas=sum(
                1 for s in sesiones if any(not sp.tool_success for sp in s.spans if sp.tipo == "tool_call")
            ),
        ))
    return hallazgos


def analizar_sesiones(sesiones: list[Sesion]) -> list[Hallazgo]:
    return (
        analizar_latencia(sesiones)
        + analizar_costos(sesiones)
        + analizar_loop(sesiones)
        + analizar_tools(sesiones)
    )


# ── presentación ──────────────────────────────────────────────────────────────

ICONOS_SEV = {"info": "ℹ️ ", "warning": "⚠️ ", "critical": "🚨"}


def imprimir_reporte(sesiones: list[Sesion], hallazgos: list[Hallazgo], incidente: str) -> None:
    total_tokens = sum(s.tokens_totales() for s in sesiones)
    total_costo = sum(s.costo_usd() for s in sesiones)
    duracion_media = sum(s.duracion_total_ms() for s in sesiones) / max(len(sesiones), 1)

    print(f"\n{'='*64}")
    print(f"  POST-MORTEM AUTOMATIZADO")
    print(f"  Incidente: {incidente}  |  {len(sesiones)} sesiones analizadas")
    print(f"{'='*64}")
    print(f"\n  Métricas globales:")
    print(f"  • Sesiones: {len(sesiones)} total, {sum(1 for s in sesiones if not s.completada)} incompletas")
    print(f"  • Tokens totales: {total_tokens:,}")
    print(f"  • Costo total: ${total_costo:.4f}")
    print(f"  • Duración media/sesión: {duracion_media/1000:.1f}s")

    criticos = [h for h in hallazgos if h.severidad == "critical"]
    warnings = [h for h in hallazgos if h.severidad == "warning"]

    print(f"\n  Hallazgos: {len(criticos)} críticos, {len(warnings)} warnings")
    print(f"  {'─'*56}")

    if not hallazgos:
        print("  ✅ Sin anomalías detectadas.")
    else:
        for h in sorted(hallazgos, key=lambda x: {"critical": 0, "warning": 1, "info": 2}[x.severidad]):
            print(f"\n  {ICONOS_SEV[h.severidad]} [{h.severidad.upper()}] {h.tipo}")
            print(f"     {h.descripcion}")
            print(f"     Métrica:   {h.metrica}")
            print(f"     Umbral:    {h.umbral}")
            print(f"     Observado: {h.valor_observado}")
            print(f"     Afectadas: {h.sesiones_afectadas}/{len(sesiones)} sesiones")

    print(f"\n  {'─'*56}")
    print(f"  Causa raíz probable:")
    if criticos:
        tipos = [h.tipo for h in criticos]
        if "loop_max_tokens" in tipos or "sesiones_incompletas" in tipos:
            print("  → Loop sin condición de salida correcta — revisar stop condition")
        if "latencia_p95" in tipos:
            print("  → Picos de latencia LLM — posible sobrecarga del proveedor o requests grandes")
        if "costo_por_sesion" in tipos or "contexto_inflado" in tipos:
            print("  → Historial sin compactación — aplicar clearing o sumarización")
        if "tool_failure_rate" in tipos:
            print("  → Servicio externo inestable — revisar circuit breaker y timeouts")
    else:
        print("  → Sin anomalías críticas. Sistema operando dentro de parámetros normales.")

    print(f"\n{'='*64}")
    print("  Acción inmediata recomendada:")
    if any(h.tipo == "loop_max_tokens" for h in hallazgos):
        print("  1. Agregar contador de turnos con límite explícito (max_turns=20)")
    if any(h.tipo == "contexto_inflado" for h in hallazgos):
        print("  2. Activar clearing de tool results en historial > 8k tokens")
    if any(h.tipo == "latencia_p95" for h in hallazgos):
        print("  3. Habilitar timeout de 10s por llamada LLM con retry exponencial")
    if any(h.tipo == "tool_failure_rate" for h in hallazgos):
        print("  4. Activar circuit breaker: abrir tras 3 fallos consecutivos en 60s")
    if not hallazgos:
        print("  Sin acciones urgentes. Continuar monitoreo.")
    print(f"{'='*64}")


# ── main ──────────────────────────────────────────────────────────────────────

def main():
    parser = argparse.ArgumentParser(description="Post-mortem automatizado de trazas de agente.")
    parser.add_argument(
        "--incidente",
        choices=["latencia", "costos", "loop_infinito", "tool_failures", "todos", "ninguno"],
        default="todos",
        help="Tipo de anomalía a inyectar (default: todos)",
    )
    parser.add_argument("--sesiones", type=int, default=20,
                        help="Número de sesiones a simular (default: 20)")
    args = parser.parse_args()

    rng = random.Random(42)
    sesiones = generar_historial(args.sesiones, args.incidente, rng)
    hallazgos = analizar_sesiones(sesiones)
    imprimir_reporte(sesiones, hallazgos, args.incidente)


if __name__ == "__main__":
    main()
