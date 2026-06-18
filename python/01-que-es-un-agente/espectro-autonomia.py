"""Mini-proyecto interactivo: espectro de autonomia.

El lector configura las dos perillas (agencia, modalidad) y observa
como cambia el comportamiento del sistema. No usa API real — simula
cada nivel para que el lector vea la diferencia de control de flujo.

Uso:
    python espectro-autonomia.py
    python espectro-autonomia.py --agencia 1        # router
    python espectro-autonomia.py --agencia 2        # tool caller / multi-step
    python espectro-autonomia.py --agencia 3        # multi-agent / code agent
    python espectro-autonomia.py --agencia 2 --modalidad cli
"""

import argparse
import json
import sys

RUTAS = ["facturacion", "soporte_tecnico", "ventas", "otro"]

TOOLS_API = ["search_db", "send_email", "get_order", "update_order"]
TOOLS_CLI = ["bash", "read_file", "edit_file", "grep"]
TOOLS_BROWSER = ["click_element", "type_text", "scroll", "screenshot"]
TOOLS_DESKTOP = ["left_click", "right_click", "type_keys", "screenshot_desktop"]
TOOLS_MOBILE = ["tap", "swipe", "type_mobile", "screenshot_mobile"]

MODALIDADES = {
    "api": {
        "label": "API / JSON",
        "tools": TOOLS_API,
        "latencia_paso": "1-5 s",
        "tokens_obs": "~50",
    },
    "cli": {
        "label": "Terminal / CLI",
        "tools": TOOLS_CLI,
        "latencia_paso": "2-8 s",
        "tokens_obs": "~200-800",
    },
    "browser": {
        "label": "Browser DOM",
        "tools": TOOLS_BROWSER,
        "latencia_paso": "~3 s",
        "tokens_obs": "~3k (DOM markdown)",
    },
    "desktop": {
        "label": "Desktop GUI",
        "tools": TOOLS_DESKTOP,
        "latencia_paso": "5-15 s",
        "tokens_obs": "~1.5k (screenshot)",
    },
    "mobile": {
        "label": "Mobile",
        "tools": TOOLS_MOBILE,
        "latencia_paso": "~5-10 s",
        "tokens_obs": "~1.5k (screenshot)",
    },
}


def simular_procesador(tarea: str) -> None:
    print("=== Nivel: ☆☆☆ Procesador ===")
    print(f"Tarea: {tarea}")
    print()
    print("[1 llamada al LLM, sin loop, sin tools]")
    print()
    print("  Usuario ──> LLM ──> Respuesta final")
    print()
    print("El output del LLM no afecta el control de flujo.")
    print("El programa siempre ejecuta el mismo paso despues de la llamada.")
    print()
    respuesta = f"Resumen de: '{tarea}' (el LLM genera esto y el programa continua)"
    print(f"Resultado: {respuesta}")
    print()
    print("Iteraciones: 1 | Tools llamadas: 0 | Latencia estimada: <2s")


def simular_router(tarea: str) -> None:
    print("=== Nivel: ★☆☆ Router ===")
    print(f"Tarea: {tarea}")
    print()
    print("[1 llamada al LLM, clasificacion en N rutas predefinidas]")
    print()
    print("  Usuario ──> LLM (clasifica) ──> if/else ──> handler_X()")
    print()
    print("El LLM elige una de varias rutas escritas en codigo.")
    print("No hay loop. No hay tools. El control de flujo lo tiene el if/else.")
    print()
    ruta = "soporte_tecnico" if "cae" in tarea.lower() or "error" in tarea.lower() else "facturacion"
    print(f"Ruta elegida: {ruta}")
    print()
    print("Iteraciones: 1 | Tools llamadas: 0 | Latencia estimada: <2s")


def simular_tool_caller(tarea: str, modalidad: str) -> None:
    mod = MODALIDADES[modalidad]
    print("=== Nivel: ★★☆ Tool caller (1 iteracion bounded) ===")
    print(f"Tarea: {tarea}")
    print(f"Modalidad: {mod['label']}")
    print()
    print("[1 llamada al LLM + 1 tool call + 1 llamada final]")
    print()
    print("  Usuario ──> LLM ──> tool_use ──> ejecutar ──> LLM ──> respuesta")
    print()
    print(f"Tools disponibles: {', '.join(mod['tools'])}")
    print()
    tool_elegida = mod["tools"][0] if modalidad == "api" else mod["tools"][1]
    print(f"Tool llamada: {tool_elegida}")
    print(f"Resultado: {{'status': 'ok', 'data': '...'}} (~{mod['tokens_obs']} tokens)")
    print()
    print(f"Iteraciones: 2 | Tools llamadas: 1 | Latencia estimada: {mod['latencia_paso']} x 2")


def simular_multi_step(tarea: str, modalidad: str) -> None:
    mod = MODALIDADES[modalidad]
    print("=== Nivel: ★★☆ Multi-step agent (loop) ===")
    print(f"Tarea: {tarea}")
    print(f"Modalidad: {mod['label']}")
    print()
    print("[Loop: el LLM decide iterar hasta end_turn o max_iter]")
    print()
    print("  Usuario ──> [Percepcion ──> LLM ──> stop_reason?]")
    print("                │                         │")
    print("                │<── Observacion <── tool_use")
    print("                │                         │")
    print("                └── end_turn ──> Respuesta final")
    print()
    print(f"Tools disponibles: {', '.join(mod['tools'])}")
    print()

    n_tools = min(3, len(mod["tools"]))
    total_iter = 1 + n_tools + 1
    print(f"Simulacion de {n_tools} tool calls en {total_iter} iteraciones:")
    for i in range(1, total_iter + 1):
        if i <= n_tools:
            t = mod["tools"][(i - 1) % len(mod["tools"])]
            print(f"  iter={i}/{total_iter}  stop_reason=tool_use  -> {t}")
        else:
            print(f"  iter={i}/{total_iter}  stop_reason=end_turn   -> respuesta final")
    print()
    total_latency = f"{mod['latencia_paso']} x {total_iter}"
    print(f"Iteraciones: {total_iter} | Tools llamadas: {n_tools} | Latencia estimada: {total_latency}")


def simular_multi_agent(tarea: str, modalidad: str) -> None:
    mod = MODALIDADES[modalidad]
    print("=== Nivel: ★★★ Multi-agent ===")
    print(f"Tarea: {tarea}")
    print(f"Modalidad: {mod['label']}")
    print()
    print("[Supervisor delega a sub-agentes con sus propios loops]")
    print()
    print("  Usuario ──> Supervisor ──> sub_agente_1 (loop propio)")
    print("                       ──> sub_agente_2 (loop propio)")
    print("                       ──> sub_agente_3 (loop propio)")
    print("                       ──> respuesta final")
    print()
    print(f"Tools del supervisor: delegar_a_subagente, planificar_tarea")
    print(f"Tools de cada sub-agente: {', '.join(mod['tools'][:3])}")
    print()

    n_sub = 3
    iter_por_sub = 4
    total_iter = 1 + (n_sub * iter_por_sub) + 1
    total_tools = n_sub * 3
    print(f"Simulacion: {n_sub} sub-agentes x ~{iter_por_sub} iteraciones c/u:")
    print(f"  supervisor: 1 llamada (planificacion)")
    for s in range(1, n_sub + 1):
        print(f"  sub-agente_{s}: ~{iter_por_sub} iteraciones, ~3 tool calls")
    print(f"  supervisor: 1 llamada (sintesis)")
    print()
    print(f"Iteraciones totales: ~{total_iter} | Tools llamadas: ~{total_tools} | "
          f"Latencia estimada: {mod['latencia_paso']} x {total_iter}")
    print()
    print(f"Coste de tokens = supervisor ~{total_iter} + sub-agentes ~{n_sub * iter_por_sub}.")
    print(f"Si p(sub-agente) = 0.8, p(todos exiten) = 0.8^3 = {0.8 ** 3:.2f} en el peor caso.")


def simular_code_agent(tarea: str, modalidad: str) -> None:
    mod = MODALIDADES[modalidad]
    print("=== Nivel: ★★★ Code agent ===")
    print(f"Tarea: {tarea}")
    print(f"Modalidad: {modalidad} (pero el code agent escribe codigo, no usa tools prefijadas)")
    print()
    print("[El LLM escribe codigo Python que se ejecuta en sandbox]")
    print()
    print("  Usuario ──> LLM ──> genera codigo ──> sandbox ──> resultado")
    print("                ^                                        │")
    print("                └───── observacion <─────────────────────┘")
    print()
    print("Tools: python_repl (cualquier codigo Python valido)")
    print("Action space: INFINITO (cualquier programa, no una lista enumerada)")
    print()
    print("Simulacion de 3 iteraciones:")
    print("  iter=1  stop_reason=tool_use  -> python_repl(code='<busqueda en datos>')")
    print("  iter=2  stop_reason=tool_use  -> python_repl(code='<transformacion>')")
    print("  iter=3  stop_reason=end_turn   -> respuesta final")
    print()
    print("Iteraciones: ~3 | Tools llamadas: 2 (pero cada una puede ser CUALQUIER codigo)")
    print("Latencia estimada: variable (depende del codigo generado)")
    print()
    print("Tradeoff clave: expresividad maxima vs superficie de fallo maxima.")
    print("Sin sandbox (E2B, Modal, Firecracker), esto es inseguro.")


AGENCIA = {
    0: ("☆☆☆ Procesador", simular_procesador),
    1: ("★☆☆ Router", simular_router),
    2: ("★★☆ Multi-step agent", simular_multi_step),
    3: ("★★★ Multi-agent", simular_multi_agent),
    4: ("★★★ Code agent", simular_code_agent),
}


def main():
    parser = argparse.ArgumentParser(
        description="Mini-proyecto: configura agencia y modalidad y observa el comportamiento."
    )
    parser.add_argument(
        "--agencia", type=int, choices=range(5), default=None,
        help="Nivel de agencia: 0=procesador, 1=router, 2=multi-step, 3=multi-agent, 4=code-agent"
    )
    parser.add_argument(
        "--modalidad", choices=list(MODALIDADES.keys()), default=None,
        help="Modalidad de accion: api, cli, browser, desktop, mobile"
    )
    parser.add_argument(
        "--tarea", type=str, default="Resuelve el bug #1234 en el repositorio",
        help="Tarea de ejemplo para la simulacion"
    )
    args = parser.parse_args()

    if args.agencia is not None and args.modalidad is not None:
        nivel_label, sim_fn = AGENCIA[args.agencia]
        print(f"Agencia: {nivel_label} | Modalidad: {MODALIDADES[args.modalidad]['label']}")
        print("=" * 60)
        print()
        if args.agencia == 0:
            sim_fn(args.tarea)
        elif args.agencia == 1:
            sim_fn(args.tarea)
        elif args.agencia == 2:
            sim_fn(args.tarea, args.modalidad)
        elif args.agencia == 3:
            sim_fn(args.tarea, args.modalidad)
        elif args.agencia == 4:
            sim_fn(args.tarea, args.modalidad)
        return

    print("Espectro de Autonomia - Mini-proyecto Interactivo")
    print("=" * 60)
    print()
    print("Configura las dos perillas para ver como cambia el comportamiento:")
    print()
    print("1. Agencia (cuanta decision cede el codigo al modelo):")
    for k, (label, _) in AGENCIA.items():
        print(f"   {k}: {label}")
    print()
    print("2. Modalidad (como actua el modelo sobre el entorno):")
    for k, v in MODALIDADES.items():
        print(f"   {k}: {v['label']}")
    print()
    print("-" * 60)

    agencia = int(input("Nivel de agencia (0-4): "))
    modalidad = input("Modalidad (api/cli/browser/desktop/mobile): ").strip()
    tarea = input("Tarea de ejemplo (Enter para default): ").strip() or args.tarea

    if agencia not in AGENCIA:
        print(f"Nivel invalido: {agencia}. Usa 0-4.")
        sys.exit(1)
    if modalidad not in MODALIDADES:
        print(f"Modalidad invalida: {modalidad}. Usa: {', '.join(MODALIDADES)}")
        sys.exit(1)

    nivel_label, sim_fn = AGENCIA[agencia]
    print()
    print(f"Agencia: {nivel_label} | Modalidad: {MODALIDADES[modalidad]['label']}")
    print("=" * 60)
    print()

    if agencia <= 1:
        sim_fn(tarea)
    else:
        sim_fn(tarea, modalidad)

    print()
    print("-" * 60)
    print()
    print("Ejecuta de nuevo con --agencia y --modalidad para comparar:")
    print(f"  python espectro-autonomia.py --agencia {agencia} --modalidad {modalidad}")
    print()
    print("Tabla de reference rapida:")
    print()
    print("  | Agencia        | Modalidad cambia...                     |")
    print("  |----------------|-----------------------------------------|")
    print("  | ☆☆☆ Procesador | Nada (sin tools, sin loop)              |")
    print("  | ★☆☆ Router     | Nada (sin tools, sin loop)              |")
    print("  | ★★☆ Multi-step | Tools, latencia, tokens por iteracion   |")
    print("  | ★★★ Multi-agen | Tools de cada sub-agente + coordinacion |")
    print("  | ★★★ Code agent | Expresividad del sandbox (infinita)    |")


if __name__ == "__main__":
    main()