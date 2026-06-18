// Memoria semántica KV plana sin versionado — para prototipado.
// Última escritura gana, sin tombstones, sin historial de versiones.

// Cómo ejecutar: make ts SCRIPT=typescript/06-memoria/04-semantica/semantic_kv.ts


interface HechoKV {
  clave: string;
  valor: string;
  fuente: string;
  timestamp: number;
}

class MemoriaSemanticaKV {
  private _hechos: Map<string, HechoKV> = new Map();

  setFact(clave: string, valor: string, fuente: string = "auto"): HechoKV {
    const hecho: HechoKV = { clave, valor, fuente, timestamp: Date.now() };
    this._hechos.set(clave, hecho);
    return hecho;
  }

  getFact(clave: string): string | null {
    return this._hechos.get(clave)?.valor ?? null;
  }

  deleteFact(clave: string): boolean {
    return this._hechos.delete(clave);
  }

  getAll(): HechoKV[] {
    return Array.from(this._hechos.values());
  }

  buildContextBlock(maxFacts: number = 20): string {
    const hechos = Array.from(this._hechos.values())
      .sort((a, b) => b.timestamp - a.timestamp)
      .slice(0, maxFacts);
    if (!hechos.length) return "";
    const lineas = hechos.map((h) => `- ${h.clave}: ${h.valor}`);
    return "## Perfil del usuario\n" + lineas.join("\n");
  }

  get size(): number {
    return this._hechos.size;
  }
}

const mem = new MemoriaSemanticaKV();

mem.setFact("lenguaje_preferido", "Python", "usuario_directo");
mem.setFact("timezone", "Europe/Madrid", "usuario_directo");
mem.setFact("estilo_respuesta", "conciso", "auto_extract");
mem.setFact("proyecto_actual", "backend de facturación", "auto_extract");

mem.setFact("lenguaje_preferido", "TypeScript", "usuario_directo");

console.log(`Total hechos: ${mem.size}`);
console.log(`lenguaje_preferido: ${mem.getFact("lenguaje_preferido")}`);
console.log(`timezone: ${mem.getFact("timezone")}`);
console.log(`no_existe: ${mem.getFact("no_existe")}`);
console.log(`\n${mem.buildContextBlock()}`);
