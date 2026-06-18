"""Pipeline post-turno con asyncio.Queue.

El turno del agente responde sin bloquear; el aprendizaje episódico
ocurre en background. asyncio.Queue desacopla producción (turno) de consumo (worker).

Patrón clave: submit() es O(1) y no bloquea el turno. El worker
procesa con la latencia que necesite (llamada a LLM de extracción, etc.)
sin que el usuario perciba esa latencia.

Cómo ejecutar:
    make py SCRIPT=python/06-memoria/03-episodica/episodic_async.py

Qué esperar:
    El agente responde sin bloquear; el procesamiento episódico ocurre en background.
    asyncio.Queue desacopla el turno (producción) del worker (consumo).

Variables de entorno:
    MODEL — modelo a usar (default: claude-sonnet-4-6)
"""
import asyncio
import time
from dataclasses import dataclass, field
from typing import Any, Callable, Awaitable


@dataclass
class TareaAprendizaje:
    raw_text: str
    sesion_id: str | None = None
    timestamp: float = field(default_factory=time.time)


ExtractorFn = Callable[[TareaAprendizaje], Awaitable[list[str]]]


async def extractor_basico(tarea: TareaAprendizaje) -> list[str]:
    """Extractor trivial: descarta mensajes cortos, guarda el resto.

    En producción: reemplazar por llamada a un LLM que extrae
    hechos estructurados del turno (Letta-style observation extraction).
    """
    await asyncio.sleep(0)  # cede el control; simula I/O asíncrono
    if len(tarea.raw_text) <= 20:
        return []
    return [tarea.raw_text]


class PipelineEpisodico:
    def __init__(
        self,
        almacen: Any,
        extractor: ExtractorFn = extractor_basico,
        maxsize: int = 100,
    ):
        self._queue: asyncio.Queue[TareaAprendizaje | None] = asyncio.Queue(maxsize=maxsize)
        self._almacen = almacen
        self._extractor = extractor
        self._task: asyncio.Task | None = None
        self.processed = 0
        self.dropped = 0

    async def _worker(self) -> None:
        """Consume la queue hasta recibir el sentinel (None)."""
        while True:
            tarea = await self._queue.get()
            if tarea is None:
                self._queue.task_done()
                break
            try:
                episodios = await self._extractor(tarea)
                for ep in episodios:
                    self._almacen.append(ep, sesion_id=tarea.sesion_id)
                    self.processed += 1
            except Exception:
                pass  # no propagar: el worker nunca debe morir por un error de extracción
            finally:
                self._queue.task_done()

    def start(self) -> None:
        self._task = asyncio.get_event_loop().create_task(self._worker())

    def submit(self, raw_text: str, sesion_id: str | None = None) -> bool:
        """Encola para procesamiento asíncrono. No bloquea.

        Retorna False (backpressure) si la queue está llena — el llamador
        decide si descartar o loggear.
        """
        try:
            self._queue.put_nowait(TareaAprendizaje(raw_text=raw_text, sesion_id=sesion_id))
            return True
        except asyncio.QueueFull:
            self.dropped += 1
            return False

    async def stop(self) -> None:
        await self._queue.put(None)  # sentinel para terminar el worker limpiamente
        if self._task:
            await self._task


# ---------------------------------------------------------------------------
# Demo: loop de agente con aprendizaje post-turno desacoplado
# ---------------------------------------------------------------------------

async def turno_agente(pipeline: PipelineEpisodico, mensaje: str, sesion_id: str) -> str:
    """Turno del agente. submit() es O(1) — el aprendizaje no bloquea la respuesta."""
    respuesta = f"Entendido: '{mensaje[:40]}'"
    pipeline.submit(mensaje, sesion_id=sesion_id)
    return respuesta


async def main() -> None:
    class LogSimple:
        def __init__(self): self.entradas: list[tuple] = []
        def append(self, c, sesion_id=None): self.entradas.append((sesion_id, c))

    almacen = LogSimple()
    pipeline = PipelineEpisodico(almacen=almacen)
    pipeline.start()

    sesion = "demo"
    mensajes = [
        "El usuario usa Python 3.12 en producción",
        "Bug en auth.py línea 247: condición invertida",
        "ok",                                          # corto → extractor descarta
        "Decidimos usar PostgreSQL para producción",
        "El módulo de billing tiene deuda técnica",
    ]

    t0 = time.perf_counter()
    for msg in mensajes:
        resp = await turno_agente(pipeline, msg, sesion_id=sesion)
        print(f"  turno: {resp}")

    await pipeline.stop()
    elapsed = time.perf_counter() - t0

    print(f"\nEpisodios guardados: {pipeline.processed} | descartados: {pipeline.dropped}")
    print(f"Tiempo total del loop: {elapsed:.3f}s\n")
    for sid, contenido in almacen.entradas:
        print(f"  [{sid}] {contenido[:60]}")


if __name__ == "__main__":
    asyncio.run(main())
