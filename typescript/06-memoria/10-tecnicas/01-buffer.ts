// Buffer conversacional con token counting y paridad tool_use/tool_result.
//
// Demuestra las cuatro propiedades críticas de un buffer de producción:
//   1. Cap por tokens (no por número de mensajes)
//   2. Paridad tool_use/tool_result — evicción nunca rompe pares
//   3. Mensajes pinned que nunca se eviccionan
//   4. Acceso seguro en JavaScript async (single-thread, no mutex necesario)
//
// Cómo ejecutar: make ts SCRIPT=typescript/06-memoria/10-tecnicas/01-buffer.ts

interface Mensaje {
  role: string;
  type?: string;
  id?: string;
  tool_use_id?: string;
  name?: string;
  content?: string;
  input?: unknown;
  _pinned?: boolean;
}

function estimarTokens(m: Mensaje): number {
  return Math.floor(JSON.stringify(m).length / 4);
}

class BufferConversacional {
  private mensajes: Mensaje[] = [];
  private readonly budget: number;

  constructor(maxTokens: number, reservaRespuesta = 2000) {
    this.budget = maxTokens - reservaRespuesta;
  }

  agregar(mensaje: Mensaje, pinned = false): void {
    this.mensajes.push({ ...mensaje, _pinned: pinned || undefined });
    this.evictar();
  }

  snapshot(): Mensaje[] {
    return this.mensajes.map(({ _pinned, ...m }) => m);
  }

  tokensActuales(): number {
    return this.mensajes.reduce((sum, m) => sum + estimarTokens(m), 0);
  }

  private primerEviccionable(): number {
    const toolUseIds = new Set(
      this.mensajes.filter((m) => m.type === "tool_use").map((m) => m.id!)
    );
    const toolResultIds = new Set(
      this.mensajes
        .filter((m) => m.type === "tool_result")
        .map((m) => m.tool_use_id!)
    );
    const paresActivos = new Set(
      [...toolUseIds].filter((id) => toolResultIds.has(id))
    );

    for (let i = 0; i < this.mensajes.length; i++) {
      const m = this.mensajes[i];
      if (m._pinned) continue;
      if (m.type === "tool_use" && paresActivos.has(m.id!)) continue;
      if (m.type === "tool_result" && paresActivos.has(m.tool_use_id!)) continue;
      return i;
    }
    return -1;
  }

  private evictar(): void {
    while (this.tokensActuales() > this.budget) {
      const idx = this.primerEviccionable();
      if (idx === -1) break;
      this.mensajes.splice(idx, 1);
    }
  }

  get length(): number {
    return this.mensajes.length;
  }
}

// ── Demo ──────────────────────────────────────────────────────────────────

const buf = new BufferConversacional(600, 200);

// Ancla pinned — nunca se eviccionará
buf.agregar({ role: "user", content: "Analiza el módulo de pagos" }, true);
buf.agregar({ role: "assistant", content: "Voy a revisar los archivos." });

// Cuatro pares tool_use / tool_result
for (let i = 0; i < 4; i++) {
  const useId = `tu_${i}`;
  buf.agregar({
    role: "assistant",
    type: "tool_use",
    id: useId,
    name: "read_file",
    input: { path: `src/pagos/modulo_${i}.py` },
  });
  buf.agregar({
    role: "user",
    type: "tool_result",
    tool_use_id: useId,
    content: `Contenido del módulo ${i}: ` + "x".repeat(80),
  });
}

buf.agregar({ role: "assistant", content: "Análisis completo. El módulo 2 tiene el problema." });
buf.agregar({ role: "user", content: "¿Qué tipo de problema?" });

const snap = buf.snapshot();
console.log(`Mensajes en buffer: ${snap.length}`);
console.log(`Tokens estimados: ${buf.tokensActuales()}`);
console.log(`Budget: ${(buf as any).budget} tokens`);
console.log();

for (const m of snap) {
  const tipo = m.type ?? m.role;
  const contenido = String(m.content ?? m.name ?? "").slice(0, 60);
  console.log(`  ${tipo}: ${contenido}`);
}
