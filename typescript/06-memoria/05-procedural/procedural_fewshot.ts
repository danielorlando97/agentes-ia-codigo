// Memoria procedural como few-shot dinámico con scoring Jaccard.

// Cómo ejecutar: make ts SCRIPT=typescript/06-memoria/05-procedural/procedural_fewshot.ts


interface Ejemplo {
  contexto: string;
  salida: string;
  score: number;
}

function jaccard(a: string, b: string): number {
  const sa = new Set(a.toLowerCase().split(/\s+/));
  const sb = new Set(b.toLowerCase().split(/\s+/));
  if (sa.size === 0 || sb.size === 0) return 0;
  const interseccion = [...sa].filter((w) => sb.has(w)).length;
  const union = new Set([...sa, ...sb]).size;
  return interseccion / union;
}

class BufferFewShot {
  private ejemplos: Ejemplo[] = [];
  private maxEjemplos: number;

  constructor(maxEjemplos: number = 50) {
    this.maxEjemplos = maxEjemplos;
  }

  add(contexto: string, salida: string): void {
    if (this.ejemplos.length >= this.maxEjemplos) {
      this.ejemplos.sort((a, b) => a.score - b.score);
      this.ejemplos.shift();
    }
    this.ejemplos.push({ contexto, salida, score: 1.0 });
  }

  reinforce(contexto: string, delta: number = 0.1): void {
    for (const e of this.recuperarSimilares(contexto, 3)) {
      e.score = Math.min(2.0, e.score + delta);
    }
  }

  penalize(contexto: string, delta: number = 0.15): void {
    const similares = this.recuperarSimilares(contexto, 3);
    for (const e of similares) {
      e.score = Math.max(0.0, e.score - delta);
    }
    this.ejemplos = this.ejemplos.filter((e) => e.score >= 0.1);
  }

  private recuperarSimilares(query: string, topK: number): Ejemplo[] {
    const scored = this.ejemplos.map((e) => ({
      e,
      s: jaccard(query, e.contexto) * e.score,
    }));
    scored.sort((a, b) => b.s - a.s);
    return scored.slice(0, topK).map((x) => x.e);
  }

  buildFewShotBlock(query: string, topK: number = 3): string {
    const ejemplos = this.recuperarSimilares(query, topK);
    if (ejemplos.length === 0) return "";
    const partes = ["## Ejemplos de comportamiento esperado\n"];
    ejemplos.forEach((e, i) => {
      partes.push(`Ejemplo ${i + 1}:\nEntrada: ${e.contexto}\nSalida: ${e.salida}\n`);
    });
    return partes.join("\n");
  }

  get length(): number {
    return this.ejemplos.length;
  }
}

if (require.main === module) {
  const buf = new BufferFewShot(20);

  buf.add(
    "Explica qué es un decorador en Python",
    "Un decorador es una función que envuelve a otra función para modificar su comportamiento sin cambiar su código."
  );
  buf.add(
    "Escribe un ejemplo de función recursiva",
    "function factorial(n: number): number {\n  return n <= 1 ? 1 : n * factorial(n - 1);\n}"
  );
  buf.add(
    "Corrige el manejo de errores en este código",
    "Añade try/catch específico, loggea el error con stack trace, y re-lanza si no puedes manejar."
  );
  buf.add("Resume este texto en 3 puntos", "• Punto 1: ...\n• Punto 2: ...\n• Punto 3: ...");

  const query = "Dame un ejemplo de recursión en TypeScript";
  console.log(`Buffer: ${buf.length} ejemplos\n`);
  console.log(buf.buildFewShotBlock(query, 2));

  buf.reinforce("función recursiva");
  console.log("Score del ejemplo de recursión tras refuerzo actualizado.");
}
