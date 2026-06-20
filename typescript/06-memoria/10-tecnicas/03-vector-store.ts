// Vector store: SQL como fuente de verdad, embeddings como ranking semántico.
// Orquestación con degradación: si el índice vectorial falla, cae a búsqueda por texto.
//
// Cómo ejecutar: make ts SCRIPT=typescript/06-memoria/10-tecnicas/03-vector-store.ts

function mockEmbedding(texto: string, dim = 64): number[] {
  let seed = 0;
  for (let i = 0; i < texto.length; i++) seed = (seed * 31 + texto.charCodeAt(i)) >>> 0;

  const vec: number[] = [];
  for (let i = 0; i < dim; i++) {
    seed = (seed * 1664525 + 1013904223) >>> 0;
    vec.push(((seed >>> 0) / 0xffffffff) * 2 - 1);
  }
  const norma = Math.sqrt(vec.reduce((s, x) => s + x * x, 0));
  return vec.map((x) => x / norma);
}

function cosineSim(a: number[], b: number[]): number {
  return a.reduce((sum, ai, i) => sum + ai * b[i], 0);
}

interface Memoria {
  id: string;
  texto: string;
  tipo: string;
  fuente?: string;
  creado: number;
}

interface ResultadoBusqueda extends Memoria {
  score: number | null;
}

class VectorStore {
  private readonly store = new Map<string, Memoria>();
  private readonly indice = new Map<string, number[]>();
  private indiceActivo = true;

  insertar(id: string, texto: string, tipo = "hecho", fuente?: string): void {
    this.store.set(id, { id, texto, tipo, fuente, creado: Date.now() });
    this.indice.set(id, mockEmbedding(texto));
  }

  buscar(query: string, k = 5): ResultadoBusqueda[] {
    if (this.indiceActivo && this.indice.size > 0) {
      try {
        return this.buscarSemantico(query, k);
      } catch (e) {
        console.log(`  [degradación] índice vectorial falló → usando texto`);
        this.indiceActivo = false;
      }
    }
    return this.buscarTexto(query, k);
  }

  private buscarSemantico(query: string, k: number): ResultadoBusqueda[] {
    const qEmb = mockEmbedding(query);
    const scores = [...this.indice.entries()]
      .map(([id, emb]) => ({ id, score: cosineSim(qEmb, emb) }))
      .sort((a, b) => b.score - a.score)
      .slice(0, k);

    return scores.map(({ id, score }) => ({
      ...this.store.get(id)!,
      score,
    }));
  }

  private buscarTexto(query: string, k: number): ResultadoBusqueda[] {
    const tokens = query.toLowerCase().split(/\s+/);
    return [...this.store.values()]
      .filter((m) => tokens.some((t) => m.texto.toLowerCase().includes(t)))
      .slice(0, k)
      .map((m) => ({ ...m, score: null }));
  }

  simularFalloIndice(): void {
    this.indiceActivo = false;
    console.log("  [simulación] índice vectorial desactivado");
  }

  get total(): number {
    return this.store.size;
  }
}

// ── Demo ──────────────────────────────────────────────────────────────────

const store = new VectorStore();

const recuerdos: [string, string, string][] = [
  ["m1", "Ana es la directora de producto de Acme Corp", "hecho"],
  ["m2", "El proyecto Pegasus tiene deadline en junio", "hecho"],
  ["m3", "El presupuesto del Q3 fue aprobado por Ana", "decision"],
  ["m4", "La integración con Stripe es parte de Pegasus", "hecho"],
  ["m5", "El equipo usa Python y TypeScript como lenguajes principales", "hecho"],
  ["m6", "La reunión de lanzamiento fue el 15 de marzo", "evento"],
  ["m7", "El bug en el módulo de auth afecta a usuarios admin", "hallazgo"],
  ["m8", "Ana aprobó el roadmap del Q4 en la reunión del viernes", "decision"],
];

for (const [id, texto, tipo] of recuerdos) store.insertar(id, texto, tipo);
console.log(`Total recuerdos insertados: ${store.total}\n`);

console.log("Búsqueda semántica: 'proyectos con presupuesto aprobado'");
const resultados = store.buscar("proyectos con presupuesto aprobado", 3);
for (const r of resultados) {
  console.log(`  [${r.score?.toFixed(3)}] ${r.texto}`);
}

console.log();
store.simularFalloIndice();
console.log("Búsqueda con degradación: 'Ana proyecto'");
const resultadosFTS = store.buscar("Ana proyecto", 3);
for (const r of resultadosFTS) {
  console.log(`  [texto] ${r.texto}`);
}
