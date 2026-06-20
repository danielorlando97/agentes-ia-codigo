// Ciclo de vida de un recuerdo: decay, tombstone, consolidación y olvido auditado.
//
// Los cuatro mecanismos que cierran el ciclo de vida de la memoria:
//   1. Decaimiento exponencial por tipo (media vida en días)
//   2. Corrección con tombstone — versiones anteriores se archivan, no se borran
//   3. Consolidación de clusters similares (union-find sobre mock embeddings)
//   4. Olvido auditado — soft delete con trazabilidad completa
//
// Cómo ejecutar: make ts SCRIPT=typescript/06-memoria/10-tecnicas/06-ciclo-vida.ts

const MEDIO_VIDA_DIAS: Record<string, number> = {
  episodio_sesion: 7,
  preferencia: 30,
  hecho_usuario: 180,
};

const UMBRAL_OLVIDO = 0.05;

type Estado = "activo" | "tombstone" | "olvidado" | "consolidado";

interface Memoria {
  id: string;
  contenido: string;
  tipo: string;
  estado: Estado;
  fuerzaBase: number;
  fuerzaActual: number;
  ultimoUso: number;
  vecesUsado: number;
  medioVidaDias: number;
  reemplazaA?: string;
  reemplazadoPor?: string;
  razonCorreccion?: string;
  creado: number;
  procesadoEn?: number;
}

// ── Funciones de ciclo de vida ─────────────────────────────────────────────

function calcularFuerza(fuerzaBase: number, ultimoUso: number, medioVidaDias: number): number {
  const deltaDias = (Date.now() / 1000 - ultimoUso) / 86400;
  return fuerzaBase * Math.exp(-0.693 * deltaDias / medioVidaDias);
}

function mockEmbedding(texto: string, dim = 32): number[] {
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
  return a.reduce((s, x, i) => s + x * b[i], 0);
}

let counter = 0;
function genId(): string {
  return (++counter).toString(16).padStart(8, "0");
}

// ── GestorCicloVida ────────────────────────────────────────────────────────

class GestorCicloVida {
  private almacen = new Map<string, Memoria>();

  insertar(contenido: string, tipo = "hecho_usuario", fuerzaBase = 1.0): string {
    const id = genId();
    const ahora = Date.now() / 1000;
    this.almacen.set(id, {
      id, contenido, tipo,
      estado: "activo",
      fuerzaBase, fuerzaActual: fuerzaBase,
      ultimoUso: ahora, vecesUsado: 0,
      medioVidaDias: MEDIO_VIDA_DIAS[tipo] ?? 90,
      creado: ahora,
    });
    return id;
  }

  reforzar(id: string): void {
    const m = this.almacen.get(id);
    if (!m || m.estado !== "activo") return;
    m.fuerzaBase = Math.min(1.0, m.fuerzaBase + 0.1);
    m.ultimoUso = Date.now() / 1000;
    m.vecesUsado++;
  }

  actualizarDecaimiento(): number {
    let n = 0;
    for (const m of this.almacen.values()) {
      if (m.estado !== "activo") continue;
      m.fuerzaActual = calcularFuerza(m.fuerzaBase, m.ultimoUso, m.medioVidaDias);
      n++;
    }
    return n;
  }

  corregir(idAnterior: string, nuevoContenido: string, razon = ""): string {
    const anterior = this.almacen.get(idAnterior);
    if (!anterior) throw new Error(`No existe memoria: ${idAnterior}`);

    const nuevoId = genId();
    const ahora = Date.now() / 1000;

    // Insertar sucesor primero (tombstone necesita referencia válida)
    this.almacen.set(nuevoId, {
      id: nuevoId, contenido: nuevoContenido,
      tipo: anterior.tipo, estado: "activo",
      fuerzaBase: anterior.fuerzaBase, fuerzaActual: anterior.fuerzaActual,
      ultimoUso: ahora, vecesUsado: 0,
      medioVidaDias: anterior.medioVidaDias,
      reemplazaA: idAnterior,
      creado: ahora,
    });
    // Tombstone el anterior
    anterior.estado = "tombstone";
    anterior.reemplazadoPor = nuevoId;
    anterior.razonCorreccion = razon;

    return nuevoId;
  }

  consolidarClusters(umbral = 0.92): number {
    const activos = [...this.almacen.values()].filter((m) => m.estado === "activo");
    if (activos.length < 2) return 0;

    const embs = new Map(activos.map((m) => [m.id, mockEmbedding(m.contenido)]));

    // Union-Find
    const parent = new Map(activos.map((m) => [m.id, m.id]));
    const find = (x: string): string => {
      while (parent.get(x) !== x) {
        parent.set(x, parent.get(parent.get(x)!)!);
        x = parent.get(x)!;
      }
      return x;
    };
    const union = (x: string, y: string) => parent.set(find(x), find(y));

    for (let i = 0; i < activos.length; i++) {
      for (let j = i + 1; j < activos.length; j++) {
        const sim = cosineSim(embs.get(activos[i].id)!, embs.get(activos[j].id)!);
        if (sim >= umbral) union(activos[i].id, activos[j].id);
      }
    }

    const clusters = new Map<string, Memoria[]>();
    for (const m of activos) {
      const raiz = find(m.id);
      if (!clusters.has(raiz)) clusters.set(raiz, []);
      clusters.get(raiz)!.push(m);
    }

    let consolidaciones = 0;
    for (const miembros of clusters.values()) {
      if (miembros.length < 2) continue;
      const cid = genId();
      const ahora = Date.now() / 1000;
      const fuerzaMax = Math.max(...miembros.map((m) => m.fuerzaBase));
      const resumen = `[Consolidado de ${miembros.length}] ` + miembros[0].contenido;

      this.almacen.set(cid, {
        id: cid, contenido: resumen, tipo: "hecho_usuario",
        estado: "activo", fuerzaBase: fuerzaMax, fuerzaActual: fuerzaMax,
        ultimoUso: ahora, vecesUsado: 0, medioVidaDias: 90, creado: ahora,
      });
      for (const m of miembros) {
        m.estado = "consolidado";
        m.reemplazadoPor = cid;
        m.razonCorreccion = "consolidación de cluster";
      }
      consolidaciones++;
    }
    return consolidaciones;
  }

  olvidarDebiles(umbral = UMBRAL_OLVIDO): number {
    let n = 0;
    const ahora = Date.now() / 1000;
    for (const m of this.almacen.values()) {
      if (m.estado === "activo" && m.fuerzaActual < umbral) {
        m.estado = "olvidado";
        m.procesadoEn = ahora;
        n++;
      }
    }
    return n;
  }

  buscar(k = 5): Memoria[] {
    return [...this.almacen.values()]
      .filter((m) => m.estado === "activo")
      .sort((a, b) => b.fuerzaActual - a.fuerzaActual)
      .slice(0, k);
  }

  historial(id: string): Memoria[] {
    const cadena: Memoria[] = [];
    let current = this.almacen.get(id);
    while (current) {
      cadena.unshift(current);
      current = current.reemplazaA ? this.almacen.get(current.reemplazaA) : undefined;
    }
    return cadena;
  }

  stats(): Record<string, number> {
    const s: Record<string, number> = {};
    for (const m of this.almacen.values()) s[m.estado] = (s[m.estado] ?? 0) + 1;
    return s;
  }
}

// ── Demo ──────────────────────────────────────────────────────────────────

const g = new GestorCicloVida();

console.log("=== Inserción inicial ===");
const datos: [string, string, number][] = [
  ["El usuario trabaja en Empresa A", "hecho_usuario", 0.9],
  ["El usuario prefiere Python sobre Java", "preferencia", 0.8],
  ["El usuario mencionó dolor de cabeza hoy", "episodio_sesion", 0.7],
  ["El usuario habla español como idioma nativo", "hecho_usuario", 1.0],
  ["El usuario prefiere Python como lenguaje principal", "preferencia", 0.75],
  ["El usuario usa Python en todos sus proyectos", "preferencia", 0.7],
  ["La reunión de ayer fue sobre el roadmap del Q3", "episodio_sesion", 0.6],
  ["El usuario es vegetariano", "hecho_usuario", 0.85],
];

const ids: Record<string, string> = {};
for (const [contenido, tipo, fuerza] of datos) {
  const id = g.insertar(contenido, tipo, fuerza);
  ids[contenido.slice(0, 30)] = id;
  console.log(`  [${id}] ${contenido.slice(0, 50)}`);
}

console.log(`\nEstado inicial: ${JSON.stringify(g.stats())}`);

// Simular 14 días de paso de tiempo en episodios de sesión
console.log("\n=== Simulando paso del tiempo (episodio_sesion → 14 días) ===");
const hace14Dias = Date.now() / 1000 - 14 * 86400;
for (const m of (g as any).almacen.values() as IterableIterator<Memoria>) {
  if (m.tipo === "episodio_sesion") m.ultimoUso = hace14Dias;
}
const actualizados = g.actualizarDecaimiento();
console.log(`Fuerzas recalculadas para ${actualizados} recuerdos`);

// Corrección con tombstone
console.log("\n=== Corrección con tombstone ===");
const idEmpresaA = [...(g as any).almacen.values() as IterableIterator<Memoria>]
  .find((m) => m.contenido.includes("Empresa A"))?.id!;
const nuevoId = g.corregir(idEmpresaA, "El usuario trabaja en Empresa B desde enero 2026",
  "el usuario lo comunicó explícitamente");
console.log(`  Hecho corregido: ${idEmpresaA} → tombstone`);
console.log(`  Nuevo hecho: ${nuevoId}`);
for (const h of g.historial(nuevoId)) {
  console.log(`    [${h.estado}] ${h.contenido.slice(0, 50)}`);
}

// Consolidación
console.log("\n=== Consolidación de clusters ===");
const nConsolidaciones = g.consolidarClusters(0.6);
console.log(`  Clusters consolidados: ${nConsolidaciones}`);
console.log(`  Estado: ${JSON.stringify(g.stats())}`);

// Olvido auditado
console.log("\n=== Olvido auditado ===");
const nOlvidados = g.olvidarDebiles(0.4);
console.log(`  Recuerdos olvidados: ${nOlvidados}`);
console.log(`  Estado final: ${JSON.stringify(g.stats())}`);

console.log("\n=== Recuerdos activos (por fuerza) ===");
for (const r of g.buscar(10)) {
  console.log(`  [${r.fuerzaActual.toFixed(3)}] ${r.contenido.slice(0, 60)}`);
}
