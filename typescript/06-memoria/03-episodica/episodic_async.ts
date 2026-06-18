// Pipeline post-turno con cola asíncrona.
// El turno del agente responde sin bloquear; el aprendizaje episódico
// ocurre en background. La cola desacopla producción (turno) de consumo (worker).

// Cómo ejecutar: make ts SCRIPT=typescript/06-memoria/03-episodica/episodic_async.ts


interface TareaAprendizaje {
  rawText: string;
  sesionId: string | null;
  timestamp: number;
}

type ExtractorFn = (tarea: TareaAprendizaje) => Promise<string[]>;

async function extractorBasico(tarea: TareaAprendizaje): Promise<string[]> {
  await Promise.resolve();
  if (tarea.rawText.length <= 20) return [];
  return [tarea.rawText];
}

class PipelineEpisodico {
  private queue: TareaAprendizaje[] = [];
  private almacen: { append: (c: string, sesionId: string | null) => void };
  private extractor: ExtractorFn;
  private maxsize: number;
  private running = false;
  private workerDone: Promise<void> | null = null;
  private resolveWorker: (() => void) | null = null;
  processed = 0;
  dropped = 0;

  constructor(
    almacen: { append: (c: string, sesionId: string | null) => void },
    extractor: ExtractorFn = extractorBasico,
    maxsize: number = 100,
  ) {
    this.almacen = almacen;
    this.extractor = extractor;
    this.maxsize = maxsize;
  }

  start(): void {
    this.running = true;
    this.workerDone = new Promise((resolve) => {
      this.resolveWorker = resolve;
    });
    this.runWorker();
  }

  private async runWorker(): Promise<void> {
    while (this.running || this.queue.length > 0) {
      if (this.queue.length === 0) {
        await new Promise((r) => setTimeout(r, 1));
        continue;
      }
      const tarea = this.queue.shift()!;
      try {
        const episodios = await this.extractor(tarea);
        for (const ep of episodios) {
          this.almacen.append(ep, tarea.sesionId);
          this.processed++;
        }
      } catch {
        // no propagar errores del worker
      }
    }
    this.resolveWorker?.();
  }

  submit(rawText: string, sesionId: string | null = null): boolean {
    if (this.queue.length >= this.maxsize) {
      this.dropped++;
      return false;
    }
    this.queue.push({ rawText, sesionId, timestamp: Date.now() });
    return true;
  }

  async stop(): Promise<void> {
    this.running = false;
    if (this.workerDone) await this.workerDone;
  }
}

async function turnoAgente(
  pipeline: PipelineEpisodico,
  mensaje: string,
  sesionId: string,
): Promise<string> {
  const respuesta = `Entendido: '${mensaje.slice(0, 40)}'`;
  pipeline.submit(mensaje, sesionId);
  return respuesta;
}

async function main() {
  const almacen = {
    entradas: [] as [string | null, string][],
    append(c: string, sesionId: string | null) {
      this.entradas.push([sesionId, c]);
    },
  };

  const pipeline = new PipelineEpisodico(almacen);
  pipeline.start();

  const sesion = "demo";
  const mensajes = [
    "El usuario usa Python 3.12 en producción",
    "Bug en auth.py línea 247: condición invertida",
    "ok",
    "Decidimos usar PostgreSQL para producción",
    "El módulo de billing tiene deuda técnica",
  ];

  const t0 = Date.now();
  for (const msg of mensajes) {
    const resp = await turnoAgente(pipeline, msg, sesion);
    console.log(`  turno: ${resp}`);
  }

  await pipeline.stop();
  const elapsed = (Date.now() - t0) / 1000;

  console.log(`\nEpisodios guardados: ${pipeline.processed} | descartados: ${pipeline.dropped}`);
  console.log(`Tiempo total del loop: ${elapsed.toFixed(3)}s\n`);
  for (const [sid, contenido] of almacen.entradas) {
    console.log(`  [${sid}] ${contenido.slice(0, 60)}`);
  }
}

main().catch(console.error);
