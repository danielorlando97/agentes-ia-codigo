/**
 * Mini-proyecto: El costómetro (TypeScript).
 *
 * Uso:
 *   npx ts-node mini-costometro.ts
 *   npx ts-node mini-costometro.ts --sesiones 5000
 *   npx ts-node mini-costometro.ts --max-tokens 8192
 */

// Snapshot precios mayo 2026 — verificar en docs del proveedor

// Cómo ejecutar: make ts SCRIPT=typescript/03-motor-llm/mini-costometro.ts

const PRECIOS: Record<string, { input: number; output: number }> = {
  haiku:  { input: 0.80,  output: 4.00  },
  sonnet: { input: 3.00,  output: 15.00 },
  opus:   { input: 15.00, output: 75.00 },
};
const VENTANAS: Record<string, number> = {
  haiku: 200_000, sonnet: 200_000, opus: 200_000,
};

const PROMPT_EJEMPLO = `\
Eres un agente de revisión de código Python. Tu trabajo es analizar el
código que te envíen y producir un informe estructurado en JSON.

REGLAS:
1. Responde SIEMPRE en JSON con el schema exacto indicado abajo.
2. Clasifica hallazgos por severidad: critical, high, medium, low.
3. No expliques el código; solo reporta problemas concretos.
4. Si no hay bugs, devuelve hallazgos = [].

SCHEMA:
{
  "hallazgos": [{"linea": null, "severidad": "...", "tipo": "...",
                 "descripcion": "...", "sugerencia": "..."}],
  "resumen": "..."
}

GUÍAS DE ESTILO DEL EQUIPO:
- PEP 8 obligatorio
- Type hints en todas las funciones públicas
- Docstrings en clases y métodos públicos
- Cobertura de tests mínima: 80%
`;

function estimarTokens(texto: string): number {
  return Math.max(1, Math.floor(texto.length / 4));
}

function analizarSecciones(prompt: string): Array<{ label: string; tokens: number }> {
  return prompt
    .split(/\n{2,}/)
    .map(b => b.trim())
    .filter(b => b.length > 0)
    .map(bloque => ({
      label: bloque.split("\n")[0].slice(0, 45),
      tokens: estimarTokens(bloque),
    }));
}

function calcularCoste(tokens: number, modelo: string, tipo: "input" | "output" = "input"): number {
  return (tokens * PRECIOS[modelo][tipo]) / 1_000_000;
}

function pad(s: string, n: number, right = false): string {
  return right ? s.padStart(n) : s.padEnd(n);
}

function imprimirTablaModelos(tokensPrompt: number, maxTokensOutput: number, sesiones: number): void {
  console.log(`\n${"Modelo".padEnd(10)} ${"Tokens input".padStart(13)} ${"USD/req".padStart(9)} ${"USD/día".padStart(10)} ${"Budget resp".padStart(18)} ${"% ventana".padStart(10)}`);
  console.log("-".repeat(75));
  for (const modelo of ["haiku", "sonnet", "opus"]) {
    const costoReq = calcularCoste(tokensPrompt, modelo);
    const costoDia = costoReq * sesiones;
    const ventana = VENTANAS[modelo];
    const budget = ventana - tokensPrompt - maxTokensOutput;
    const budgetStr = budget > 0 ? budget.toLocaleString() : `OVERFLOW -${(-budget).toLocaleString()}`;
    const pct = (tokensPrompt / ventana * 100).toFixed(1);
    console.log(
      `${pad(modelo, 10)} ${pad(tokensPrompt.toLocaleString(), 13, true)} $${pad(costoReq.toFixed(5), 8, true)} $${pad(costoDia.toFixed(4), 9, true)} ${pad(budgetStr, 18, true)} ${pad(pct + "%", 10, true)}`
    );
  }
}

function main(): void {
  const args = process.argv.slice(2);
  const getArg = (flag: string, def: string) => {
    const i = args.indexOf(flag);
    return i >= 0 && args[i + 1] ? args[i + 1] : def;
  };

  const sesiones = parseInt(getArg("--sesiones", "1000"));
  const maxTokens = parseInt(getArg("--max-tokens", "4096"));
  const promptFile = getArg("--prompt", "");

  let prompt = PROMPT_EJEMPLO;
  if (promptFile) {
    try {
      const fs = require("fs");
      prompt = fs.readFileSync(promptFile, "utf8");
    } catch {
      console.error(`Error: no se encontró '${promptFile}'`);
      process.exit(1);
    }
  } else {
    console.log("[Usando prompt de ejemplo — pasa --prompt archivo.txt para usar el tuyo]\n");
  }

  const tokensTotal = estimarTokens(prompt);
  const secciones = analizarSecciones(prompt);

  console.log("=".repeat(60));
  console.log("EL COSTÓMETRO — Análisis de system prompt");
  console.log("=".repeat(60));
  console.log(`\nPrompt: ${prompt.length.toLocaleString()} chars  |  ~${tokensTotal.toLocaleString()} tokens`);
  console.log(`Proyección: ${sesiones.toLocaleString()} sesiones/día  |  ${maxTokens.toLocaleString()} tokens output reservados`);

  console.log(`\n${"Sección (primera línea)".padEnd(43)} ${"Tokens".padStart(7)} ${"%" .padStart(5)}`);
  console.log("-".repeat(58));
  for (const sec of secciones) {
    const pct = (sec.tokens / tokensTotal * 100).toFixed(1);
    const label = sec.label.length > 43 ? sec.label.slice(0, 41) + ".." : sec.label;
    console.log(`${label.padEnd(43)} ${sec.tokens.toLocaleString().padStart(7)} ${pct.padStart(4)}%`);
  }
  console.log("-".repeat(58));
  console.log(`${"TOTAL".padEnd(43)} ${tokensTotal.toLocaleString().padStart(7)} ${"100.0".padStart(4)}%`);

  console.log(`\n--- Coste por modelo (${sesiones.toLocaleString()} sesiones/día) ---`);
  imprimirTablaModelos(tokensTotal, maxTokens, sesiones);

  console.log(`\n--- Efecto de truncar el prompt ---`);
  console.log(`\n${"% prompt".padEnd(12)} ${"Tokens".padStart(8)} ${"USD/día (sonnet)".padStart(17)} ${"Ahorro USD/día".padStart(16)}`);
  console.log("-".repeat(56));
  const costeBase = calcularCoste(tokensTotal, "sonnet") * sesiones;
  for (const pct of [100, 75, 50, 25]) {
    const tok = Math.floor(tokensTotal * pct / 100);
    const coste = calcularCoste(tok, "sonnet") * sesiones;
    const ahorro = costeBase - coste;
    console.log(`${(pct + "%").padStart(10)}   ${tok.toLocaleString().padStart(8)}  $${coste.toFixed(4).padStart(16)}  $${ahorro.toFixed(4).padStart(15)}`);
  }

  const anual = calcularCoste(tokensTotal, "sonnet") * sesiones * 365;
  console.log(`\n→ Coste anual proyectado (sonnet, ${sesiones.toLocaleString()}/día): $${anual.toFixed(2)}`);
  console.log(`→ Con caching (10× más barato en zona estática): $${(anual / 10).toFixed(2)}/año`);
  console.log(`\n[Estimación ±10% — instala tiktoken (Python) para conteo exacto]`);
  console.log(`[Snapshot precios mayo 2026 — verificar en docs del proveedor]`);
}

main();
