"""Mini-proyecto: El mismo problema, 4 bucles.

Toma un problema concreto (resolver una acertijo lógico o calcular algo)
y lo resuelve con los 4 patrones de loop: ReAct, Plan-and-Execute,
Tree of Thoughts y Reflexion. Compara trazas, tokens y latencia.

Requiere: ANTHROPIC_API_KEY

Uso:
    python mini-4-bucles.py
    python mini-4-bucles.py --patron react
    python mini-4-bucles.py --patron todos --problema "¿cuántos pasos..."
"""

import argparse
import os
import sys
import time
from dataclasses import dataclass, field

try:
    import anthropic
except ImportError:
    print("Error: pip install anthropic")
    sys.exit(1)

MODEL = os.environ.get("MODEL", "claude-haiku-4-5-20251001")  # modelo económico para comparar patrones

PROBLEMA_DEFAULT = (
    "Un granjero tiene que cruzar un río con un zorro, una gallina y un saco de maíz. "
    "El bote solo lleva al granjero más una cosa. "
    "Si el granjero deja solos al zorro y la gallina, el zorro se come a la gallina. "
    "Si deja solas a la gallina y el maíz, la gallina se come el maíz. "
    "¿Cómo puede cruzar todo al otro lado sin pérdidas?"
)


# ── métricas ───────────────────────────────────────────────────────────────────

@dataclass
class Metricas:
    patron: str
    tokens_input: int = 0
    tokens_output: int = 0
    llamadas_llm: int = 0
    duracion_s: float = 0.0
    pasos: list[str] = field(default_factory=list)
    respuesta_final: str = ""
    error: str = ""

    @property
    def tokens_total(self) -> int:
        return self.tokens_input + self.tokens_output

    @property
    def costo_usd(self) -> float:
        return (self.tokens_input * 0.80 + self.tokens_output * 4.00) / 1_000_000


def llamar_llm(client: anthropic.Anthropic, system: str, mensajes: list[dict]) -> tuple[str, int, int]:
    resp = client.messages.create(
        model=MODEL,
        max_tokens=1024,
        system=system,
        messages=mensajes,
    )
    return resp.content[0].text, resp.usage.input_tokens, resp.usage.output_tokens


# ── ReAct ─────────────────────────────────────────────────────────────────────

def patron_react(client: anthropic.Anthropic, problema: str) -> Metricas:
    m = Metricas(patron="ReAct")
    t0 = time.time()

    system = (
        "Resuelve el problema paso a paso usando el formato:\n"
        "Thought: razonamiento sobre el estado actual\n"
        "Action: acción concreta a tomar\n"
        "Observation: resultado de la acción\n\n"
        "Continúa hasta resolver el problema. Termina con 'SOLUCIÓN: <respuesta>'."
    )

    mensajes = [{"role": "user", "content": f"Problema: {problema}"}]
    for paso in range(8):
        texto, ti, to = llamar_llm(client, system, mensajes)
        m.tokens_input += ti
        m.tokens_output += to
        m.llamadas_llm += 1

        lineas = [l.strip() for l in texto.strip().split("\n") if l.strip()]
        for l in lineas:
            if l.startswith(("Thought:", "Action:", "Observation:", "SOLUCIÓN:")):
                m.pasos.append(f"  [{paso+1}] {l}")

        if "SOLUCIÓN:" in texto:
            idx = texto.index("SOLUCIÓN:")
            m.respuesta_final = texto[idx + 9:].strip()
            break

        mensajes.append({"role": "assistant", "content": texto})
        mensajes.append({"role": "user", "content": "Continúa."})

    m.duracion_s = time.time() - t0
    return m


# ── Plan-and-Execute ──────────────────────────────────────────────────────────

def patron_plan_execute(client: anthropic.Anthropic, problema: str) -> Metricas:
    m = Metricas(patron="Plan-and-Execute")
    t0 = time.time()

    # Fase 1: Planner
    system_planner = (
        "Eres un planificador. Dado un problema, genera un plan numerado de pasos concretos "
        "para resolverlo. Sé específico. Termina con: PLAN_COMPLETO"
    )
    plan_texto, ti, to = llamar_llm(client, system_planner,
                                    [{"role": "user", "content": f"Problema: {problema}"}])
    m.tokens_input += ti
    m.tokens_output += to
    m.llamadas_llm += 1
    m.pasos.append(f"  [PLANNER] Plan generado ({len(plan_texto.split(chr(10)))} líneas)")

    # Fase 2: Executor — ejecuta cada paso del plan
    system_executor = (
        "Eres un ejecutor. Tienes este plan:\n\n"
        f"{plan_texto}\n\n"
        "Ejecuta el paso indicado y describe el resultado detalladamente."
    )
    pasos_plan = [l for l in plan_texto.split("\n") if l.strip() and l.strip()[0].isdigit()]
    for i, paso in enumerate(pasos_plan[:5]):  # max 5 pasos
        exec_texto, ti, to = llamar_llm(
            client, system_executor,
            [{"role": "user", "content": f"Ejecuta: {paso}"}]
        )
        m.tokens_input += ti
        m.tokens_output += to
        m.llamadas_llm += 1
        m.pasos.append(f"  [{i+1}] {paso[:60]}...")

    # Fase 3: Sintetizador
    system_final = "Sintetiza los pasos ejecutados y da la solución final al problema."
    final_texto, ti, to = llamar_llm(
        client, system_final,
        [{"role": "user", "content": f"Problema: {problema}\nPlan ejecutado:\n{plan_texto}"}]
    )
    m.tokens_input += ti
    m.tokens_output += to
    m.llamadas_llm += 1
    m.respuesta_final = final_texto.strip()[:200]
    m.duracion_s = time.time() - t0
    return m


# ── Tree of Thoughts (simplificado) ──────────────────────────────────────────

def patron_tot(client: anthropic.Anthropic, problema: str) -> Metricas:
    m = Metricas(patron="Tree of Thoughts")
    t0 = time.time()

    # Genera 3 pensamientos iniciales
    system_gen = (
        "Genera 3 enfoques diferentes y prometedores para resolver este problema. "
        "Numera cada uno. Sé conciso."
    )
    gen_texto, ti, to = llamar_llm(client, system_gen,
                                   [{"role": "user", "content": f"Problema: {problema}"}])
    m.tokens_input += ti
    m.tokens_output += to
    m.llamadas_llm += 1
    m.pasos.append(f"  [GENERACIÓN] 3 ramas iniciales")

    # Evalúa qué rama es más prometedora
    system_eval = (
        "Evalúa estos enfoques para resolver el problema. "
        "Elige el más prometedor y explica por qué. "
        "Responde con: MEJOR_ENFOQUE: <número>\nRAZÓN: <explicación>"
    )
    eval_texto, ti, to = llamar_llm(
        client, system_eval,
        [{"role": "user", "content": f"Problema: {problema}\nEnfoques:\n{gen_texto}"}]
    )
    m.tokens_input += ti
    m.tokens_output += to
    m.llamadas_llm += 1
    m.pasos.append(f"  [EVALUACIÓN] Rama seleccionada")

    # Expande la mejor rama
    system_expand = (
        "Desarrolla completamente el enfoque seleccionado para llegar a la solución. "
        "Termina con SOLUCIÓN: <respuesta>"
    )
    expand_texto, ti, to = llamar_llm(
        client, system_expand,
        [{"role": "user", "content": f"Problema: {problema}\nEnfoque a desarrollar:\n{eval_texto}"}]
    )
    m.tokens_input += ti
    m.tokens_output += to
    m.llamadas_llm += 1
    m.pasos.append(f"  [EXPANSIÓN] Rama desarrollada")

    if "SOLUCIÓN:" in expand_texto:
        m.respuesta_final = expand_texto[expand_texto.index("SOLUCIÓN:") + 9:].strip()[:200]
    else:
        m.respuesta_final = expand_texto.strip()[:200]

    m.duracion_s = time.time() - t0
    return m


# ── Reflexion ─────────────────────────────────────────────────────────────────

def patron_reflexion(client: anthropic.Anthropic, problema: str) -> Metricas:
    m = Metricas(patron="Reflexion")
    t0 = time.time()

    system_base = "Resuelve el siguiente problema. Sé conciso y directo."
    system_reflexion = (
        "Analiza críticamente la solución dada. Identifica:\n"
        "1. Errores o suposiciones incorrectas\n"
        "2. Pasos faltantes\n"
        "3. Cómo mejorar la solución\n"
        "Si la solución es correcta, di: SOLUCIÓN_CORRECTA"
    )
    system_mejora = (
        "Basándote en la reflexión crítica, produce una solución mejorada. "
        "Termina con SOLUCIÓN_FINAL: <respuesta>"
    )

    # Intento 1
    resp1, ti, to = llamar_llm(client, system_base,
                               [{"role": "user", "content": f"Problema: {problema}"}])
    m.tokens_input += ti
    m.tokens_output += to
    m.llamadas_llm += 1
    m.pasos.append(f"  [INTENTO 1] Solución inicial generada")

    # Reflexión sobre intento 1
    ref1, ti, to = llamar_llm(
        client, system_reflexion,
        [{"role": "user", "content": f"Problema: {problema}\nSolución propuesta:\n{resp1}"}]
    )
    m.tokens_input += ti
    m.tokens_output += to
    m.llamadas_llm += 1

    if "SOLUCIÓN_CORRECTA" in ref1:
        m.pasos.append(f"  [REFLEXIÓN 1] Solución aceptada — sin iteración necesaria")
        m.respuesta_final = resp1.strip()[:200]
        m.duracion_s = time.time() - t0
        return m

    m.pasos.append(f"  [REFLEXIÓN 1] Problemas detectados — iterando")

    # Mejora basada en reflexión
    resp2, ti, to = llamar_llm(
        client, system_mejora,
        [{"role": "user", "content": f"Problema: {problema}\nIntento previo:\n{resp1}\nReflexión:\n{ref1}"}]
    )
    m.tokens_input += ti
    m.tokens_output += to
    m.llamadas_llm += 1
    m.pasos.append(f"  [INTENTO 2] Solución mejorada con feedback")

    if "SOLUCIÓN_FINAL:" in resp2:
        m.respuesta_final = resp2[resp2.index("SOLUCIÓN_FINAL:") + 15:].strip()[:200]
    else:
        m.respuesta_final = resp2.strip()[:200]

    m.duracion_s = time.time() - t0
    return m


# ── presentación ──────────────────────────────────────────────────────────────

def imprimir_metricas(m: Metricas) -> None:
    estado = "ERROR: " + m.error if m.error else "OK"
    print(f"\n  {'─'*56}")
    print(f"  Patrón: {m.patron}")
    print(f"  Estado: {estado}")
    print(f"  LLM calls: {m.llamadas_llm}  |  tokens: {m.tokens_total:,}  |  "
          f"duración: {m.duracion_s:.1f}s  |  costo: ${m.costo_usd:.5f}")
    print(f"\n  Traza:")
    for paso in m.pasos:
        print(paso)
    if m.respuesta_final:
        respuesta_corta = m.respuesta_final[:120] + ("..." if len(m.respuesta_final) > 120 else "")
        print(f"\n  Respuesta final: {respuesta_corta}")


def imprimir_comparativa(resultados: list[Metricas]) -> None:
    if len(resultados) < 2:
        return
    print(f"\n{'='*64}")
    print(f"  COMPARATIVA — {len(resultados)} patrones")
    print(f"{'='*64}")
    print(f"\n  {'Patrón':<22} {'Calls':>6} {'Tokens':>8} {'Dur(s)':>7} {'Costo':>10}")
    print(f"  {'─'*56}")
    for m in resultados:
        print(f"  {m.patron:<22} {m.llamadas_llm:>6} {m.tokens_total:>8,} "
              f"{m.duracion_s:>7.1f} ${m.costo_usd:>9.5f}")

    min_calls = min(m.llamadas_llm for m in resultados)
    min_tok = min(m.tokens_total for m in resultados)
    min_dur = min(m.duracion_s for m in resultados)
    min_cost = min(m.costo_usd for m in resultados)
    print(f"\n  Mínimos: calls={min_calls}, tokens={min_tok:,}, "
          f"dur={min_dur:.1f}s, costo=${min_cost:.5f}")
    print(f"\n  Observaciones:")
    max_tok = max(resultados, key=lambda x: x.tokens_total)
    print(f"  • Patrón más costoso en tokens: {max_tok.patron} ({max_tok.tokens_total:,})")
    min_calls_p = min(resultados, key=lambda x: x.llamadas_llm)
    max_calls_p = max(resultados, key=lambda x: x.llamadas_llm)
    print(f"  • Menos LLM calls: {min_calls_p.patron} ({min_calls_p.llamadas_llm})")
    print(f"  • Más LLM calls: {max_calls_p.patron} ({max_calls_p.llamadas_llm})")


# ── main ──────────────────────────────────────────────────────────────────────

def main():
    parser = argparse.ArgumentParser(
        description="Resuelve el mismo problema con 4 patrones de loop."
    )
    parser.add_argument("--patron",
                        choices=["react", "plan_execute", "tot", "reflexion", "todos"],
                        default="todos",
                        help="Patrón a ejecutar (default: todos)")
    parser.add_argument("--problema", default=PROBLEMA_DEFAULT,
                        help="Problema a resolver")
    args = parser.parse_args()

    api_key = os.environ.get("ANTHROPIC_API_KEY")
    if not api_key:
        print("Error: variable de entorno ANTHROPIC_API_KEY no configurada")
        sys.exit(1)

    client = anthropic.Anthropic(api_key=api_key)

    print(f"\n{'='*64}")
    print(f"  MINI-PROYECTO: EL MISMO PROBLEMA, 4 BUCLES")
    print(f"  Modelo: {MODEL}")
    print(f"{'='*64}")
    print(f"\n  Problema: {args.problema[:80]}...")

    patrones_map = {
        "react": patron_react,
        "plan_execute": patron_plan_execute,
        "tot": patron_tot,
        "reflexion": patron_reflexion,
    }

    patrones_a_ejecutar = (
        list(patrones_map.keys()) if args.patron == "todos" else [args.patron]
    )

    resultados = []
    for nombre in patrones_a_ejecutar:
        print(f"\n  → Ejecutando: {nombre}...")
        try:
            fn = patrones_map[nombre]
            m = fn(client, args.problema)
        except Exception as e:
            m = Metricas(patron=nombre, error=str(e))
        imprimir_metricas(m)
        resultados.append(m)

    if len(resultados) > 1:
        imprimir_comparativa(resultados)


if __name__ == "__main__":
    main()
