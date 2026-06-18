// Mini-proyecto interactivo: espectro de autonomia.
//
// El lector configura las dos perillas (agencia, modalidad) y observa
// como cambia el comportamiento del sistema. No usa API real — simula
// cada nivel para que el lector vea la diferencia de control de flujo.
//
// Uso:
//   go run espectro-autonomia.go
//   go run espectro-autonomia.go -agencia 2 -modalidad cli

// Cómo ejecutar: make go FILE=go/01-que-es-un-agente/espectro-autonomia.go

package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

type modalidad struct {
	label      string
	tools      []string
	latencia   string
	tokensObs  string
}

type nivelAgencia struct {
	label string
}

var modModalidades = map[string]modalidad{
	"api": {
		label:      "API / JSON",
		tools:      []string{"search_db", "send_email", "get_order", "update_order"},
		latencia:   "1-5 s",
		tokensObs:  "~50",
	},
	"cli": {
		label:      "Terminal / CLI",
		tools:      []string{"bash", "read_file", "edit_file", "grep"},
		latencia:   "2-8 s",
		tokensObs:  "~200-800",
	},
	"browser": {
		label:      "Browser DOM",
		tools:      []string{"click_element", "type_text", "scroll", "screenshot"},
		latencia:   "~3 s",
		tokensObs:  "~3k (DOM markdown)",
	},
	"desktop": {
		label:      "Desktop GUI",
		tools:      []string{"left_click", "right_click", "type_keys", "screenshot_desktop"},
		latencia:   "5-15 s",
		tokensObs:  "~1.5k (screenshot)",
	},
	"mobile": {
		label:      "Mobile",
		tools:      []string{"tap", "swipe", "type_mobile", "screenshot_mobile"},
		latencia:   "~5-10 s",
		tokensObs:  "~1.5k (screenshot)",
	},
}

var niveles = map[int]nivelAgencia{
	0: {"☆☆☆ Procesador"},
	1: {"★☆☆ Router"},
	2: {"★★☆ Multi-step agent"},
	3: {"★★★ Multi-agent"},
	4: {"★★★ Code agent"},
}

func simularProcesador(tarea string) {
	fmt.Println("=== Nivel: ☆☆☆ Procesador ===")
	fmt.Printf("Tarea: %s\n", tarea)
	fmt.Println()
	fmt.Println("[1 llamada al LLM, sin loop, sin tools]")
	fmt.Println()
	fmt.Println("  Usuario ──> LLM ──> Respuesta final")
	fmt.Println()
	fmt.Println("El output del LLM no afecta el control de flujo.")
	fmt.Println("El programa siempre ejecuta el mismo paso despues de la llamada.")
	fmt.Println()
	fmt.Printf("Resultado: Resumen de: '%s' (el LLM genera esto y el programa continua)\n", tarea)
	fmt.Println()
	fmt.Println("Iteraciones: 1 | Tools llamadas: 0 | Latencia estimada: <2s")
}

func simularRouter(tarea string) {
	fmt.Println("=== Nivel: ★☆☆ Router ===")
	fmt.Printf("Tarea: %s\n", tarea)
	fmt.Println()
	fmt.Println("[1 llamada al LLM, clasificacion en N rutas predefinidas]")
	fmt.Println()
	fmt.Println("  Usuario ──> LLM (clasifica) ──> if/else ──> handler_X()")
	fmt.Println()
	fmt.Println("El LLM elige una de varias rutas escritas en codigo.")
	fmt.Println("No hay loop. No hay tools. El control de flujo lo tiene el if/else.")
	fmt.Println()
	ruta := "facturacion"
	if strings.Contains(strings.ToLower(tarea), "cae") || strings.Contains(strings.ToLower(tarea), "error") {
		ruta = "soporte_tecnico"
	}
	fmt.Printf("Ruta elegida: %s\n", ruta)
	fmt.Println()
	fmt.Println("Iteraciones: 1 | Tools llamadas: 0 | Latencia estimada: <2s")
}

func simularToolCaller(tarea, modKey string) {
	mod := modModalidades[modKey]
	fmt.Println("=== Nivel: ★★☆ Tool caller (1 iteracion bounded) ===")
	fmt.Printf("Tarea: %s\n", tarea)
	fmt.Printf("Modalidad: %s\n", mod.label)
	fmt.Println()
	fmt.Println("[1 llamada al LLM + 1 tool call + 1 llamada final]")
	fmt.Println()
	fmt.Println("  Usuario ──> LLM ──> tool_use ──> ejecutar ──> LLM ──> respuesta")
	fmt.Println()
	fmt.Printf("Tools disponibles: %s\n", strings.Join(mod.tools, ", "))
	fmt.Println()
	toolElegida := mod.tools[0]
	if modKey != "api" && len(mod.tools) > 1 {
		toolElegida = mod.tools[1]
	}
	fmt.Printf("Tool llamada: %s\n", toolElegida)
	fmt.Printf("Resultado: {'status': 'ok', 'data': '...'} (%s tokens)\n", mod.tokensObs)
	fmt.Println()
	fmt.Printf("Iteraciones: 2 | Tools llamadas: 1 | Latencia estimada: %s x 2\n", mod.latencia)
}

func simularMultiStep(tarea, modKey string) {
	mod := modModalidades[modKey]
	fmt.Println("=== Nivel: ★★☆ Multi-step agent (loop) ===")
	fmt.Printf("Tarea: %s\n", tarea)
	fmt.Printf("Modalidad: %s\n", mod.label)
	fmt.Println()
	fmt.Println("[Loop: el LLM decide iterar hasta end_turn o max_iter]")
	fmt.Println()
	fmt.Println("  Usuario ──> [Percepcion ──> LLM ──> stop_reason?]")
	fmt.Println("                │                         │")
	fmt.Println("                │<── Observacion <── tool_use")
	fmt.Println("                │                         │")
	fmt.Println("                └── end_turn ──> Respuesta final")
	fmt.Println()
	fmt.Printf("Tools disponibles: %s\n", strings.Join(mod.tools, ", "))
	fmt.Println()

	nTools := 3
	if len(mod.tools) < nTools {
		nTools = len(mod.tools)
	}
	totalIter := 1 + nTools + 1
	fmt.Printf("Simulacion de %d tool calls en %d iteraciones:\n", nTools, totalIter)
	for i := 1; i <= totalIter; i++ {
		if i <= nTools {
			t := mod.tools[(i-1)%len(mod.tools)]
			fmt.Printf("  iter=%d/%d  stop_reason=tool_use  -> %s\n", i, totalIter, t)
		} else {
			fmt.Printf("  iter=%d/%d  stop_reason=end_turn   -> respuesta final\n", i, totalIter)
		}
	}
	fmt.Println()
	fmt.Printf("Iteraciones: %d | Tools llamadas: %d | Latencia estimada: %s x %d\n", totalIter, nTools, mod.latencia, totalIter)
}

func simularMultiAgent(tarea, modKey string) {
	mod := modModalidades[modKey]
	fmt.Println("=== Nivel: ★★★ Multi-agent ===")
	fmt.Printf("Tarea: %s\n", tarea)
	fmt.Printf("Modalidad: %s\n", mod.label)
	fmt.Println()
	fmt.Println("[Supervisor delega a sub-agentes con sus propios loops]")
	fmt.Println()
	fmt.Println("  Usuario ──> Supervisor ──> sub_agente_1 (loop propio)")
	fmt.Println("                       ──> sub_agente_2 (loop propio)")
	fmt.Println("                       ──> sub_agente_3 (loop propio)")
	fmt.Println("                       ──> respuesta final")
	fmt.Println()
	fmt.Println("Tools del supervisor: delegar_a_subagente, planificar_tarea")
	tools3 := mod.tools
	if len(tools3) > 3 {
		tools3 = tools3[:3]
	}
	fmt.Printf("Tools de cada sub-agente: %s\n", strings.Join(tools3, ", "))
	fmt.Println()

	nSub := 3
	iterPorSub := 4
	totalIter := 1 + nSub*iterPorSub + 1
	totalTools := nSub * 3
	fmt.Printf("Simulacion: %d sub-agentes x ~%d iteraciones c/u:\n", nSub, iterPorSub)
	fmt.Println("  supervisor: 1 llamada (planificacion)")
	for s := 1; s <= nSub; s++ {
		fmt.Printf("  sub-agente_%d: ~%d iteraciones, ~3 tool calls\n", s, iterPorSub)
	}
	fmt.Println("  supervisor: 1 llamada (sintesis)")
	fmt.Println()
	fmt.Printf("Iteraciones totales: ~%d | Tools llamadas: ~%d | Latencia estimada: %s x %d\n", totalIter, totalTools, mod.latencia, totalIter)
	fmt.Println()
	fmt.Println("NOTA: cada sub-agente tiene su propia ventana de contexto.")
	fmt.Printf("Coste de tokens = supervisor ~%d + sub-agentes ~%d.\n", totalIter, nSub*iterPorSub)
	fmt.Println("Si p(sub-agente) = 0.8, p(todos exiten) = 0.8^3 = 0.51 en el peor caso.")
}

func simularCodeAgent(tarea, modKey string) {
	fmt.Println("=== Nivel: ★★★ Code agent ===")
	fmt.Printf("Tarea: %s\n", tarea)
	fmt.Printf("Modalidad: %s (pero el code agent escribe codigo, no usa tools prefijadas)\n", modKey)
	_ = modModalidades[modKey]
	fmt.Println()
	fmt.Println("[El LLM escribe codigo Python que se ejecuta en sandbox]")
	fmt.Println()
	fmt.Println("  Usuario ──> LLM ──> genera codigo ──> sandbox ──> resultado")
	fmt.Println("                ^                                        │")
	fmt.Println("                └───── observacion <──────────────────────┘")
	fmt.Println()
	fmt.Println("Tools: python_repl (cualquier codigo Python valido)")
	fmt.Println("Action space: INFINITO (cualquier programa, no una lista enumerada)")
	fmt.Println()
	fmt.Println("Simulacion de 3 iteraciones:")
	fmt.Println("  iter=1  stop_reason=tool_use  -> python_repl(code='<busqueda en datos>')")
	fmt.Println("  iter=2  stop_reason=tool_use  -> python_repl(code='<transformacion>')")
	fmt.Println("  iter=3  stop_reason=end_turn   -> respuesta final")
	fmt.Println()
	fmt.Println("Iteraciones: ~3 | Tools llamadas: 2 (pero cada una puede ser CUALQUIER codigo)")
	fmt.Println("Latencia estimada: variable (depende del codigo generado)")
	fmt.Println()
	fmt.Println("Tradeoff clave: expresividad maxima vs superficie de fallo maxima.")
	fmt.Println("Sin sandbox (E2B, Modal, Firecracker), esto es inseguro.")
}

func printTabla() {
	fmt.Println("Tabla de referencia rapida:")
	fmt.Println()
	fmt.Println("  | Agencia        | Modalidad cambia...                     |")
	fmt.Println("  |----------------|-----------------------------------------|")
	fmt.Println("  | ☆☆☆ Procesador | Nada (sin tools, sin loop)              |")
	fmt.Println("  | ★☆☆ Router     | Nada (sin tools, sin loop)              |")
	fmt.Println("  | ★★☆ Multi-step | Tools, latencia, tokens por iteracion   |")
	fmt.Println("  | ★★★ Multi-agen | Tools de cada sub-agente + coordinacion |")
	fmt.Println("  | ★★★ Code agent | Expresividad del sandbox (infinita)    |")
}

func main() {
	agenciaFlag := flag.Int("agencia", -1, "Nivel de agencia: 0-4")
	modalidadFlag := flag.String("modalidad", "", "Modalidad: api, cli, browser, desktop, mobile")
	tareaFlag := flag.String("tarea", "Resuelve el bug #1234 en el repositorio", "Tarea de ejemplo")
	flag.Parse()

	if *agenciaFlag >= 0 && *modalidadFlag != "" {
		agencia := *agenciaFlag
		modalidad := *modalidadFlag
		if _, ok := niveles[agencia]; !ok {
			fmt.Printf("Nivel invalido: %d. Usa 0-4.\n", agencia)
			os.Exit(1)
		}
		if _, ok := modModalidades[modalidad]; !ok {
			fmt.Printf("Modalidad invalida: %s. Usa: api, cli, browser, desktop, mobile\n", modalidad)
			os.Exit(1)
		}
		nivel, _ := niveles[agencia]
		mod, _ := modModalidades[modalidad]
		fmt.Printf("Agencia: %s | Modalidad: %s\n", nivel.label, mod.label)
		fmt.Println(strings.Repeat("=", 60))
		fmt.Println()
		switch agencia {
		case 0:
			simularProcesador(*tareaFlag)
		case 1:
			simularRouter(*tareaFlag)
		case 2:
			simularMultiStep(*tareaFlag, modalidad)
		case 3:
			simularMultiAgent(*tareaFlag, modalidad)
		case 4:
			simularCodeAgent(*tareaFlag, modalidad)
		}
		fmt.Println()
		fmt.Println(strings.Repeat("-", 60))
		fmt.Println()
		fmt.Printf("Ejecuta de nuevo con --agencia y --modalidad para comparar:\n")
		fmt.Printf("  go run espectro-autonomia.go -agencia %d -modalidad %s\n", agencia, modalidad)
		fmt.Println()
		printTabla()
		return
	}

	fmt.Println("Espectro de Autonomia - Mini-proyecto Interactivo")
	fmt.Println(strings.Repeat("=", 60))
	fmt.Println()
	fmt.Println("Configura las dos perillas para ver como cambia el comportamiento:")
	fmt.Println()
	fmt.Println("1. Agencia (cuanta decision cede el codigo al modelo):")
	for k := 0; k <= 4; k++ {
		fmt.Printf("   %d: %s\n", k, niveles[k].label)
	}
	fmt.Println()
	fmt.Println("2. Modalidad (como actua el modelo sobre el entorno):")
	for k, v := range modModalidades {
		fmt.Printf("   %s: %s\n", k, v.label)
	}
	fmt.Println()
	fmt.Println(strings.Repeat("-", 60))
	fmt.Println()
	fmt.Println("Ejecuta con los argumentos -agencia y -modalidad:")
	fmt.Println("  go run espectro-autonomia.go -agencia 2 -modalidad cli")
	fmt.Println()
	printTabla()
}