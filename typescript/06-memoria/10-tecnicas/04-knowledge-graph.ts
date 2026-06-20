// Knowledge graph en memoria: entidades, relaciones tipadas y recuperación por BFS.
// Demuestra indexación incremental de episodios y expansión de grafo hasta H saltos.
//
// Cómo ejecutar: make ts SCRIPT=typescript/06-memoria/10-tecnicas/04-knowledge-graph.ts

interface Entidad {
  id: string;
  nombre: string;
  tipo: string;
}

interface Relacion {
  desdeId: string;
  hastaId: string;
  tipo: string;
  episodioId: string;
}

interface Episodio {
  id: string;
  texto: string;
}

function mockNER(texto: string): Array<{ nombre: string; tipo: string }> {
  const mapa: Record<string, string> = {
    "Ana": "persona",
    "Acme Corp": "organización",
    "Pegasus": "proyecto",
    "Q3": "periodo",
    "Stripe": "servicio",
    "Q4": "periodo",
  };
  return Object.entries(mapa)
    .filter(([nombre]) => texto.toLowerCase().includes(nombre.toLowerCase()))
    .map(([nombre, tipo]) => ({ nombre, tipo }));
}

function mockRelaciones(
  texto: string,
  entidades: Array<{ nombre: string }>
): Array<[string, string, string]> {
  const nombres = new Set(entidades.map((e) => e.nombre));
  const rels: Array<[string, string, string]> = [];
  if (nombres.has("Ana") && nombres.has("Acme Corp")) rels.push(["Ana", "trabaja_en", "Acme Corp"]);
  if (nombres.has("Ana") && nombres.has("Q3") && texto.toLowerCase().includes("aprobó")) {
    rels.push(["Ana", "aprobó", "Q3"]);
  }
  if (nombres.has("Pegasus") && nombres.has("Stripe")) rels.push(["Pegasus", "incluye", "Stripe"]);
  if (nombres.has("Q3") && nombres.has("Pegasus")) rels.push(["Q3", "financia", "Pegasus"]);
  if (nombres.has("Ana") && nombres.has("Q4")) rels.push(["Ana", "aprobó", "Q4"]);
  return rels;
}

class KnowledgeGraph {
  private readonly entidades = new Map<string, Entidad>();
  private readonly relaciones: Relacion[] = [];
  private readonly episodios = new Map<string, Episodio>();
  private readonly episodioEntidades = new Map<string, Set<string>>();
  private idCounter = 0;

  private nextId(): string {
    return `ep_${++this.idCounter}`;
  }

  indexarEpisodio(texto: string): string {
    const epId = this.nextId();
    this.episodios.set(epId, { id: epId, texto });
    this.episodioEntidades.set(epId, new Set());

    const entidades = mockNER(texto);
    for (const { nombre, tipo } of entidades) {
      const entId = nombre.toLowerCase().replace(/\s+/g, "_");
      if (!this.entidades.has(entId)) {
        this.entidades.set(entId, { id: entId, nombre, tipo });
      }
      this.episodioEntidades.get(epId)!.add(entId);
    }

    for (const [s, verbo, o] of mockRelaciones(texto, entidades)) {
      const sId = s.toLowerCase().replace(/\s+/g, "_");
      const oId = o.toLowerCase().replace(/\s+/g, "_");
      if (this.entidades.has(sId) && this.entidades.has(oId)) {
        this.relaciones.push({ desdeId: sId, hastaId: oId, tipo: verbo, episodioId: epId });
      }
    }

    return epId;
  }

  recallPorGrafo(seeds: string[], hops = 2): Episodio[] {
    const seedIds = new Set(
      seeds.map((s) => s.toLowerCase().replace(/\s+/g, "_")).filter((id) => this.entidades.has(id))
    );

    const visitados = new Set(seedIds);
    let frontera = new Set(seedIds);

    for (let h = 0; h < hops && frontera.size > 0; h++) {
      const nuevos = new Set<string>();
      for (const rel of this.relaciones) {
        if (frontera.has(rel.desdeId) && !visitados.has(rel.hastaId)) nuevos.add(rel.hastaId);
        if (frontera.has(rel.hastaId) && !visitados.has(rel.desdeId)) nuevos.add(rel.desdeId);
      }
      nuevos.forEach((id) => visitados.add(id));
      frontera = nuevos;
    }

    const epIds = new Set<string>();
    for (const [epId, entIds] of this.episodioEntidades.entries()) {
      for (const entId of entIds) {
        if (visitados.has(entId)) {
          epIds.add(epId);
          break;
        }
      }
    }

    return [...epIds].map((id) => this.episodios.get(id)!);
  }

  get stats() {
    return {
      entidades: this.entidades.size,
      relaciones: this.relaciones.length,
      episodios: this.episodios.size,
    };
  }
}

// ── Demo ──────────────────────────────────────────────────────────────────

const kg = new KnowledgeGraph();

const episodios = [
  "Ana es la directora de producto de Acme Corp",
  "El proyecto Pegasus tiene deadline en junio",
  "El presupuesto del Q3 fue aprobado por Ana",
  "La integración con Stripe es parte de Pegasus y financia el Q3 del proyecto",
  "Ana aprobó el roadmap del Q4 en la reunión del viernes",
];

console.log("Indexando episodios...");
for (const ep of episodios) {
  const id = kg.indexarEpisodio(ep);
  console.log(`  [${id}] ${ep.slice(0, 60)}`);
}

console.log(`\nGrafo: ${JSON.stringify(kg.stats)}\n`);

console.log("Consulta relacional: 'proyectos con presupuesto'  Seeds: [Q3, Pegasus]");
const resultados = kg.recallPorGrafo(["Q3", "Pegasus"], 2);
console.log(`Episodios recuperados: ${resultados.length}`);
for (const r of resultados) console.log(`  ${r.texto.slice(0, 80)}`);

console.log("\nConsulta: ¿qué sabe el sistema sobre Ana?  Seeds: [Ana]");
const sobreAna = kg.recallPorGrafo(["Ana"], 1);
for (const r of sobreAna) console.log(`  ${r.texto.slice(0, 80)}`);
