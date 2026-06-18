// Mini-proyecto interactivo: espectro de autonomia.
//
// El lector configura las dos perillas (agencia, modalidad) y observa
// como cambia el comportamiento del sistema. No usa API real — simula
// cada nivel para que el lector vea la diferencia de control de flujo.
//
// Uso:
//   bun espectro-autonomia.ts
//   bun espectro-autonomia.ts --agencia 1        // router
//   bun espectro-autonomia.ts --agencia 2         // tool caller / multi-step
//   bun espectro-autonomia.ts --agencia 3         // multi-agent / code agent
//   bun espectro-autonomia.ts --agencia 2 --modalidad cli

// Cómo ejecutar: make ts SCRIPT=typescript/01-que-es-un-agente/espectro-autonomia.ts -- --agencia 2 --modalidad api
// Qué esperar: simulacion del nivel de agencia elegido. Sin llamadas API.


const RUTAS = ["facturacion", "soporte_tecnico", "ventas", "otro"];

const MODALIDADES: Record<string, { label: string; tools: string[]; latencia: string; tokensObs: string }> = {
  api: {
    label: "API / JSON",
    tools: ["search_db", "send_email", "get_order", "update_order"],
    latencia: "1-5 s",
    tokensObs: "~50",
  },
  cli: {
    label: "Terminal / CLI",
    tools: ["bash", "read_file", "edit_file", "grep"],
    latencia: "2-8 s",
    tokensObs: "~200-800",
  },
  browser: {
    label: "Browser DOM",
    tools: ["click_element", "type_text", "scroll", "screenshot"],
    latencia: "~3 s",
    tokensObs: "~3k (DOM markdown)",
  },
  desktop: {
    label: "Desktop GUI",
    tools: ["left_click", "right_click", "type_keys", "screenshot_desktop"],
    latencia: "5-15 s",
    tokensObs: "~1.5k (screenshot)",
  },
  mobile: {
    label: "Mobile",
    tools: ["tap", "swipe", "type_mobile", "screenshot_mobile"],
    latencia: "~5-10 s",
    tokensObs: "~1.5k (screenshot)",
  },
};

function simularProcesador(tarea: string): void {
  console.log("=== Nivel: ☆☆☆ Procesador ===");
  console.log(`Tarea: ${tarea}`);
  console.log();
  console.log("[1 llamada al LLM, sin loop, sin tools]");
  console.log();
  console.log("  Usuario ──> LLM ──> Respuesta final");
  console.log();
  console.log("El output del LLM no afecta el control de flujo.");
  console.log("El programa siempre ejecuta el mismo paso despues de la llamada.");
  console.log();
  console.log(`Resultado: Resumen de: '${tarea}' (el LLM genera esto y el programa continua)`);
  console.log();
  console.log("Iteraciones: 1 | Tools llamadas: 0 | Latencia estimada: <2s");
}

function simularRouter(tarea: string): void {
  console.log("=== Nivel: ★☆☆ Router ===");
  console.log(`Tarea: ${tarea}`);
  console.log();
  console.log("[1 llamada al LLM, clasificacion en N rutas predefinidas]");
  console.log();
  console.log("  Usuario ──> LLM (clasifica) ──> if/else ──> handler_X()");
  console.log();
  console.log("El LLM elige una de varias rutas escritas en codigo.");
  console.log("No hay loop. No hay tools. El control de flujo lo tiene el if/else.");
  console.log();
  const ruta = tarea.toLowerCase().includes("cae") || tarea.toLowerCase().includes("error")
    ? "soporte_tecnico" : "facturacion";
  console.log(`Ruta elegida: ${ruta}`);
  console.log();
  console.log("Iteraciones: 1 | Tools llamadas: 0 | Latencia estimada: <2s");
}

function simularToolCaller(tarea: string, modalidad: string): void {
  const mod = MODALIDADES[modalidad];
  console.log("=== Nivel: ★★☆ Tool caller (1 iteracion bounded) ===");
  console.log(`Tarea: ${tarea}`);
  console.log(`Modalidad: ${mod.label}`);
  console.log();
  console.log("[1 llamada al LLM + 1 tool call + 1 llamada final]");
  console.log();
  console.log("  Usuario ──> LLM ──> tool_use ──> ejecutar ──> LLM ──> respuesta");
  console.log();
  console.log(`Tools disponibles: ${mod.tools.join(", ")}`);
  console.log();
  const toolElegida = modalidad === "api" ? mod.tools[0] : mod.tools[1];
  console.log(`Tool llamada: ${toolElegida}`);
  console.log(`Resultado: {'status': 'ok', 'data': '...'} (${mod.tokensObs} tokens)`);
  console.log();
  console.log(`Iteraciones: 2 | Tools llamadas: 1 | Latencia estimada: ${mod.latencia} x 2`);
}

function simularMultiStep(tarea: string, modalidad: string): void {
  const mod = MODALIDADES[modalidad];
  console.log("=== Nivel: ★★☆ Multi-step agent (loop) ===");
  console.log(`Tarea: ${tarea}`);
  console.log(`Modalidad: ${mod.label}`);
  console.log();
  console.log("[Loop: el LLM decide iterar hasta end_turn o max_iter]");
  console.log();
  console.log("  Usuario ──> [Percepcion ──> LLM ──> stop_reason?]");
  console.log("                │                         │");
  console.log("                │<── Observacion <── tool_use");
  console.log("                │                         │");
  console.log("                └── end_turn ──> Respuesta final");
  console.log();
  console.log(`Tools disponibles: ${mod.tools.join(", ")}`);
  console.log();

  const nTools = Math.min(3, mod.tools.length);
  const totalIter = 1 + nTools + 1;
  console.log(`Simulacion de ${nTools} tool calls en ${totalIter} iteraciones:`);
  for (let i = 1; i <= totalIter; i++) {
    if (i <= nTools) {
      const t = mod.tools[(i - 1) % mod.tools.length];
      console.log(`  iter=${i}/${totalIter}  stop_reason=tool_use  -> ${t}`);
    } else {
      console.log(`  iter=${i}/${totalIter}  stop_reason=end_turn   -> respuesta final`);
    }
  }
  console.log();
  console.log(`Iteraciones: ${totalIter} | Tools llamadas: ${nTools} | Latencia estimada: ${mod.latencia} x ${totalIter}`);
}

function simularMultiAgent(tarea: string, modalidad: string): void {
  const mod = MODALIDADES[modalidad];
  console.log("=== Nivel: ★★★ Multi-agent ===");
  console.log(`Tarea: ${tarea}`);
  console.log(`Modalidad: ${mod.label}`);
  console.log();
  console.log("[Supervisor delega a sub-agentes con sus propios loops]");
  console.log();
  console.log("  Usuario ──> Supervisor ──> sub_agente_1 (loop propio)");
  console.log("                       ──> sub_agente_2 (loop propio)");
  console.log("                       ──> sub_agente_3 (loop propio)");
  console.log("                       ──> respuesta final");
  console.log();
  console.log(`Tools del supervisor: delegar_a_subagente, planificar_tarea`);
  console.log(`Tools de cada sub-agente: ${mod.tools.slice(0, 3).join(", ")}`);
  console.log();

  const nSub = 3;
  const iterPorSub = 4;
  const totalIter = 1 + nSub * iterPorSub + 1;
  const totalTools = nSub * 3;
  console.log(`Simulacion: ${nSub} sub-agentes x ~${iterPorSub} iteraciones c/u:`);
  console.log(`  supervisor: 1 llamada (planificacion)`);
  for (let s = 1; s <= nSub; s++) {
    console.log(`  sub-agente_${s}: ~${iterPorSub} iteraciones, ~3 tool calls`);
  }
  console.log(`  supervisor: 1 llamada (sintesis)`);
  console.log();
  console.log(`Iteraciones totales: ~${totalIter} | Tools llamadas: ~${totalTools} | Latencia estimada: ${mod.latencia} x ${totalIter}`);
  console.log();
  console.log("NOTA: cada sub-agente tiene su propia ventana de contexto.");
  console.log(`Coste de tokens = supervisor ~${totalIter} + sub-agentes ~${nSub * iterPorSub}.`);
  console.log(`Si p(sub-agente) = 0.8, p(todos exiten) = 0.8^3 = ${(0.8 ** 3).toFixed(2)} en el peor caso.`);
}

function simularCodeAgent(tarea: string, modalidad: string): void {
  const mod = MODALIDADES[modalidad];
  console.log("=== Nivel: ★★★ Code agent ===");
  console.log(`Tarea: ${tarea}`);
  console.log(`Modalidad: ${modalidad} (pero el code agent escribe codigo, no usa tools prefijadas)`);
  console.log();
  console.log("[El LLM escribe codigo Python que se ejecuta en sandbox]");
  console.log();
  console.log("  Usuario ──> LLM ──> genera codigo ──> sandbox ──> resultado");
  console.log("                ^                                        │");
  console.log("                └───── observacion <──────────────────────┘");
  console.log();
  console.log("Tools: python_repl (cualquier codigo Python valido)");
  console.log("Action space: INFINITO (cualquier programa, no una lista enumerada)");
  console.log();
  console.log("Simulacion de 3 iteraciones:");
  console.log("  iter=1  stop_reason=tool_use  -> python_repl(code='<busqueda en datos>')");
  console.log("  iter=2  stop_reason=tool_use  -> python_repl(code='<transformacion>')");
  console.log("  iter=3  stop_reason=end_turn   -> respuesta final");
  console.log();
  console.log("Iteraciones: ~3 | Tools llamadas: 2 (pero cada una puede ser CUALQUIER codigo)");
  console.log("Latencia estimada: variable (depende del codigo generado)");
  console.log();
  console.log("Tradeoff clave: expresividad maxima vs superficie de fallo maxima.");
  console.log("Sin sandbox (E2B, Modal, Firecracker), esto es inseguro.");
}

type SimFn = (tarea: string, modalidad?: string) => void;

const AGENCIA: Record<number, [string, SimFn]> = {
  0: ["☆☆☆ Procesador", (t) => simularProcesador(t)],
  1: ["★☆☆ Router", (t) => simularRouter(t)],
  2: ["★★☆ Multi-step agent", (t, m) => simularMultiStep(t, m!)],
  3: ["★★★ Multi-agent", (t, m) => simularMultiAgent(t, m!)],
  4: ["★★★ Code agent", (t, m) => simularCodeAgent(t, m!)],
};

const args = process.argv.slice(2);
function getArg(name: string): string | undefined {
  const idx = args.indexOf(`--${name}`);
  return idx >= 0 ? args[idx + 1] : undefined;
}

const agenciaStr = getArg("agencia");
const modalidadStr = getArg("modalidad");
const tarea = getArg("tarea") || "Resuelve el bug #1234 en el repositorio";

if (agenciaStr !== undefined && modalidadStr !== undefined) {
  const agencia = parseInt(agenciaStr, 10);
  const modalidad = modalidadStr;
  if (AGENCIA[agencia] && MODALIDADES[modalidad]) {
    const [nivelLabel, simFn] = AGENCIA[agencia];
    console.log(`Agencia: ${nivelLabel} | Modalidad: ${MODALIDADES[modalidad].label}`);
    console.log("=".repeat(60));
    console.log();
    if (agencia <= 1) {
      simFn(tarea);
    } else {
      (simFn as (t: string, m: string) => void)(tarea, modalidad);
    }
  } else {
    console.error("Nivel o modalidad invalido.");
    process.exit(1);
  }
} else {
  console.log("Espectro de Autonomia - Mini-proyecto Interactivo");
  console.log("=".repeat(60));
  console.log();
  console.log("Configura las dos perillas para ver como cambia el comportamiento:");
  console.log();
  console.log("1. Agencia (cuanta decision cede el codigo al modelo):");
  for (const [k, [label]] of Object.entries(AGENCIA)) {
    console.log(`   ${k}: ${label}`);
  }
  console.log();
  console.log("2. Modalidad (como actua el modelo sobre el entorno):");
  for (const [k, v] of Object.entries(MODALIDADES)) {
    console.log(`   ${k}: ${v.label}`);
  }
  console.log();
  console.log("-".repeat(60));
  console.log();
  console.log("Ejecuta con los argumentos --agencia y --modalidad:");
  console.log("  bun espectro-autonomia.ts --agencia 2 --modalidad cli");
  console.log();
  console.log("Tabla de referencia rapida:");
  console.log();
  console.log("  | Agencia        | Modalidad cambia...                     |");
  console.log("  |----------------|-----------------------------------------|");
  console.log("  | ☆☆☆ Procesador | Nada (sin tools, sin loop)              |");
  console.log("  | ★☆☆ Router     | Nada (sin tools, sin loop)              |");
  console.log("  | ★★☆ Multi-step | Tools, latencia, tokens por iteracion   |");
  console.log("  | ★★★ Multi-agen | Tools de cada sub-agente + coordinacion |");
  console.log("  | ★★★ Code agent | Expresividad del sandbox (infinita)    |");
}