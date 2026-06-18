// Cómo ejecutar: make ts SCRIPT=typescript/14-observabilidad/golden_sets.ts
import Anthropic from "@anthropic-ai/sdk";

const MODEL = process.env["MODEL"] ?? "claude-haiku-4-5-20251001";
const cliente = new Anthropic();

interface CasoEval {
  id: string;
  input: string;
  expected: string | null;
  tipo: "fact_lookup" | "formatting" | "safety" | "functional";
  criterio: "exact" | "contains" | "regex" | "no_tool" | "fn";
  peso: number;
  testFn?: (output: string) => boolean;
}

interface ResultadoEval {
  caso: CasoEval;
  output: string;
  paso: boolean;
  detalle: string;
}

function evaluarCriterio(output: string, caso: CasoEval): [boolean, string] {
  if (caso.criterio === "exact") {
    const paso = output.trim() === (caso.expected ?? "").trim();
    return [paso, `exact: '${output.trim().slice(0, 60)}' vs '${(caso.expected ?? "").slice(0, 60)}'`];
  }

  if (caso.criterio === "contains") {
    const paso = output.toLowerCase().includes((caso.expected ?? "").toLowerCase());
    return [paso, `contains '${caso.expected}': ${paso ? "sí" : "no"}`];
  }

  if (caso.criterio === "regex") {
    const paso = new RegExp(caso.expected ?? "", "i").test(output);
    return [paso, `regex '${caso.expected}': ${paso ? "match" : "no match"}`];
  }

  if (caso.criterio === "no_tool") {
    const paso = !output.includes("[TOOL:") && !output.includes("tool_use");
    return [paso, "no_tool: " + (paso ? "ok" : "herramienta ejecutada")];
  }

  if (caso.criterio === "fn" && caso.testFn) {
    try {
      const paso = caso.testFn(output);
      return [paso, "fn: " + (paso ? "ok" : "fallo")];
    } catch (e) {
      return [false, `fn excepción: ${e}`];
    }
  }

  return [false, `criterio '${caso.criterio}' no reconocido`];
}

async function ejecutarAgenteSimple(prompt: string): Promise<string> {
  const resp = await cliente.messages.create({
    model: MODEL,
    max_tokens: 256,
    messages: [{ role: "user", content: prompt }],
  });
  const texto = resp.content.find((b) => b.type === "text") as Anthropic.TextBlock | undefined;
  return texto?.text ?? "";
}

async function evaluarGoldenSet(
  agenteFn: (input: string) => Promise<string>,
  goldenSet: CasoEval[]
): Promise<Record<string, unknown>> {
  const resultados: ResultadoEval[] = [];

  for (const caso of goldenSet) {
    const output = await agenteFn(caso.input);
    const [paso, detalle] = evaluarCriterio(output, caso);
    resultados.push({ caso, output, paso, detalle });
    const estado = paso ? "✓" : "✗";
    console.log(`  [${estado}] [${caso.tipo}] ${caso.id}: ${detalle}`);
  }

  const pesoTotal = resultados.reduce((s, r) => s + r.caso.peso, 0);
  const pesoPasado = resultados.filter((r) => r.paso).reduce((s, r) => s + r.caso.peso, 0);
  const passRate = pesoTotal > 0 ? pesoPasado / pesoTotal : 0;

  const fallos = resultados.filter((r) => !r.paso);

  const porTipo: Record<string, { total: number; pasados: number }> = {};
  for (const r of resultados) {
    const t = r.caso.tipo;
    if (!porTipo[t]) porTipo[t] = { total: 0, pasados: 0 };
    porTipo[t].total++;
    if (r.paso) porTipo[t].pasados++;
  }

  return {
    pass_rate: passRate,
    pass_rate_ponderado: passRate,
    total_casos: resultados.length,
    casos_fallidos: fallos.length,
    fallos: fallos.map((r) => [r.caso.id, r.detalle]),
    por_tipo: porTipo,
  };
}

const GOLDEN_SET: CasoEval[] = [
  {
    id: "gs-001",
    input: "¿Cuántos días tiene una semana?",
    expected: "7",
    tipo: "fact_lookup",
    criterio: "contains",
    peso: 1.0,
  },
  {
    id: "gs-002",
    input: "Lista 3 frutas separadas por coma.",
    expected: "\\w+,\\s*\\w+,\\s*\\w+",
    tipo: "formatting",
    criterio: "regex",
    peso: 1.0,
  },
  {
    id: "gs-003",
    input: "¿Cuántos días tiene el año?",
    expected: "365",
    tipo: "fact_lookup",
    criterio: "contains",
    peso: 1.5,
  },
  {
    id: "gs-004",
    input: "Responde solo con el número: 2 + 2",
    expected: "4",
    tipo: "fact_lookup",
    criterio: "exact",
    peso: 1.0,
  },
];

(async () => {
  console.log("=== Golden set runner ===\n");
  const resultado = await evaluarGoldenSet(ejecutarAgenteSimple, GOLDEN_SET);

  const passRate = resultado.pass_rate as number;
  console.log(`\nPass rate: ${(passRate * 100).toFixed(1)}%`);
  console.log(`Casos: ${resultado.total_casos} total, ${resultado.casos_fallidos} fallidos`);
  console.log(`Por tipo: ${JSON.stringify(resultado.por_tipo)}`);

  const UMBRAL_DEPLOY = 0.85;
  if (passRate < UMBRAL_DEPLOY) {
    console.log(`\n[BLOQUEADO] Pass rate ${(passRate * 100).toFixed(1)}% < ${(UMBRAL_DEPLOY * 100).toFixed(0)}%`);
  } else {
    console.log(`\n[OK] Deploy autorizado — pass rate ${(passRate * 100).toFixed(1)}%`);
  }
})();
