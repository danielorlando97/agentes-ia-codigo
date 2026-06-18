// Test de localizacion: clasifica un sistema en el espectro smolagents.

// Cómo ejecutar: make ts SCRIPT=typescript/01-que-es-un-agente/clasificador-nivel.ts
// Qué esperar: 6 casos clasificados en el espectro de autonomia. Sin llamadas API.

type Features = {
  multiAgente?: boolean;
  codeAgent?: boolean;
  loopNoAcotadoYDecideTools?: boolean;
  loopAcotadoConTools?: boolean;
  llmEligeRutaSinLoop?: boolean;
};

function classify(f: Features): string {
  if (f.multiAgente) return "★★★ multi-agente";
  if (f.codeAgent) return "★★★ code agent";
  if (f.loopNoAcotadoYDecideTools) return "★★☆ multi-step";
  if (f.loopAcotadoConTools) return "★★☆ tool caller";
  if (f.llmEligeRutaSinLoop) return "★☆☆ router";
  return "☆☆☆ procesador";
}

const CASES: Array<[string, Features]> = [
  ["traduccion sin tools", {}],
  ["router por intent", { llmEligeRutaSinLoop: true }],
  ["RAG simple (1 retrieve + 1 generate)", { loopAcotadoConTools: true }],
  ["ReAct hasta end_turn", { loopNoAcotadoYDecideTools: true }],
  ["supervisor + workers", { multiAgente: true }],
  ["agente que escribe codigo Python", { codeAgent: true }],
];

for (const [name, f] of CASES) {
  console.log(classify(f).padEnd(25) + "  " + name);
}
