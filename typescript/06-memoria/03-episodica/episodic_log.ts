// Memoria episódica sin decay — log plano para prototipado.
// Variante mínima: solo append y búsqueda por texto o sesión.

// Cómo ejecutar: make ts SCRIPT=typescript/06-memoria/03-episodica/episodic_log.ts


interface EntradaLog {
  contenido: string;
  sesionId: string | null;
  timestamp: number;
}

class MemoriaLog {
  private _log: EntradaLog[] = [];

  append(contenido: string, sesionId: string | null = null): EntradaLog {
    const entrada: EntradaLog = { contenido, sesionId, timestamp: Date.now() };
    this._log.push(entrada);
    return entrada;
  }

  recallRecent(n: number = 5): EntradaLog[] {
    return this._log.slice(-n);
  }

  recallSearch(query: string, n: number = 5): EntradaLog[] {
    const q = query.toLowerCase();
    const matches = this._log.filter((e) => e.contenido.toLowerCase().includes(q));
    return matches.slice(-n);
  }

  recallSession(sesionId: string): EntradaLog[] {
    return this._log.filter((e) => e.sesionId === sesionId);
  }

  get length(): number {
    return this._log.length;
  }
}

const mem = new MemoriaLog();

mem.append("El usuario usa Python 3.12 en producción", "s1");
mem.append("Bug en auth.py línea 247: condición invertida", "s1");
mem.append("Decidimos usar PostgreSQL en lugar de SQLite", "s2");
mem.append("El módulo de billing tiene deuda técnica", "s2");
mem.append("Prefiere respuestas sin código cuando no es necesario", "s3");

console.log(`Total: ${mem.length} episodios\n`);

console.log("--- últimos 3 ---");
for (const e of mem.recallRecent(3)) {
  console.log(`  [${e.sesionId}] ${e.contenido.slice(0, 60)}`);
}

console.log("\n--- búsqueda: 'usuario' ---");
for (const e of mem.recallSearch("usuario")) {
  console.log(`  [${e.sesionId}] ${e.contenido.slice(0, 60)}`);
}

console.log("\n--- sesión s1 ---");
for (const e of mem.recallSession("s1")) {
  console.log(`  ${e.contenido.slice(0, 60)}`);
}
