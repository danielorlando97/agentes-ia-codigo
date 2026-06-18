# Patrón Mercado/Subasta: el orquestador descompone la tarea, consulta a los workers
# por sus bids (confianza + tokens estimados), asigna según utilidad = confianza/tokens,
# ejecuta, y sintetiza los resultados.
#
# Cómo ejecutar:
#   make py SCRIPT=python/12-multi-agente/mercado.py
#
# Qué esperar:
#   Orquestador descompone la tarea y consulta bids a los workers.
#   Asigna subtareas según utilidad (confianza/tokens estimados).
#
# Variables de entorno:
#   MODEL — modelo a usar (default: claude-sonnet-4-6)

import os
import json
from dataclasses import dataclass, field
from anthropic import Anthropic

client = Anthropic()
MODEL = os.environ.get("MODEL", "claude-haiku-4-5-20251001")


@dataclass
class Worker:
    id: str
    capabilities: list[str]
    current_load: int = 0
    system_prompt: str = ""

    def __post_init__(self):
        if not self.system_prompt:
            caps = ", ".join(self.capabilities)
            self.system_prompt = (
                f"Eres el worker {self.id}. "
                f"Tus capacidades son: {caps}. "
                "Ejecuta las tareas que te asignen con precisión."
            )


@dataclass
class Bid:
    worker_id: str
    confidence: float      # 0.0 = no puede; 1.0 = máxima confianza
    estimated_tokens: int  # tokens estimados para ejecutar la subtarea

    @property
    def utility(self) -> float:
        """Función de utilidad: confianza por token gastado."""
        if self.estimated_tokens <= 0:
            return 0.0
        return self.confidence / self.estimated_tokens


def llamar_llm(system: str, user: str, temperature: float = 0.0) -> str:
    resp = client.messages.create(
        model=MODEL,
        max_tokens=800,
        system=system,
        messages=[{"role": "user", "content": user}],
        temperature=temperature,
    )
    return resp.content[0].text.strip()


def orquestador_descomponer(tarea: str) -> list[dict]:
    """El orquestador descompone la tarea en subtareas con la capacidad requerida."""
    system = (
        "Eres un orquestador. Descompone la tarea en subtareas concretas. "
        "Para cada subtarea especifica la capacidad necesaria. "
        "Responde SOLO con JSON válido: "
        '[{"id": "s1", "descripcion": "...", "capacidad_requerida": "..."}, ...]'
    )
    raw = llamar_llm(system, f"Tarea: {tarea}")
    inicio = raw.find("[")
    fin = raw.rfind("]") + 1
    return json.loads(raw[inicio:fin])


def solicitar_bid(worker: Worker, subtarea: dict) -> Bid:
    """Pregunta al worker si puede ejecutar la subtarea y con qué confianza."""
    system = (
        f"Eres el worker {worker.id}. "
        f"Tus capacidades: {', '.join(worker.capabilities)}. "
        f"Carga actual: {worker.current_load} tareas en curso. "
        "Evalúa si puedes ejecutar la subtarea solicitada. "
        "Responde SOLO con JSON: "
        '{"confidence": <0.0-1.0>, "estimated_tokens": <int>}. '
        "Si no puedes hacerla, confidence debe ser 0.0."
    )
    descripcion = subtarea["descripcion"]
    capacidad = subtarea.get("capacidad_requerida", "general")
    user = (
        f"Subtarea: {descripcion}\n"
        f"Capacidad requerida: {capacidad}\n"
        "¿Puedes ejecutarla? Indica confianza y tokens estimados."
    )
    raw = llamar_llm(system, user)
    inicio = raw.find("{")
    fin = raw.rfind("}") + 1
    data = json.loads(raw[inicio:fin])
    return Bid(
        worker_id=worker.id,
        confidence=float(data.get("confidence", 0.0)),
        estimated_tokens=int(data.get("estimated_tokens", 500)),
    )


def ejecutar_subtarea(worker: Worker, subtarea: dict, contexto_previo: str) -> str:
    """El worker ganador ejecuta la subtarea."""
    user = subtarea["descripcion"]
    if contexto_previo:
        user = f"Contexto de subtareas anteriores:\n{contexto_previo}\n\nTu subtarea: {user}"
    return llamar_llm(worker.system_prompt, user, temperature=0.3)


def sintetizar(tarea: str, resultados: dict[str, str]) -> str:
    """El orquestador sintetiza todos los resultados en una respuesta final."""
    resultados_texto = "\n\n".join(
        f"Subtarea {sid}:\n{res}" for sid, res in resultados.items()
    )
    system = (
        "Eres un orquestador. Sintetiza los resultados de las subtareas "
        "en una respuesta cohesiva y completa para la tarea original."
    )
    return llamar_llm(
        system,
        f"Tarea original: {tarea}\n\nResultados de subtareas:\n{resultados_texto}",
        temperature=0.0,
    )


def mercado(tarea: str, workers: list[Worker]) -> str:
    """
    Patrón mercado/subasta.

    Args:
        tarea:   Tarea de alto nivel a resolver.
        workers: Lista de workers disponibles con sus capacidades.

    Returns:
        Respuesta final sintetizada.
    """
    print(f"[Orquestador] Descomponiendo tarea...")
    subtareas = orquestador_descomponer(tarea)
    print(f"  {len(subtareas)} subtareas identificadas.")

    resultados: dict[str, str] = {}

    for subtarea in subtareas:
        sid = subtarea["id"]
        desc = subtarea["descripcion"]
        cap = subtarea.get("capacidad_requerida", "general")
        print(f"\n[Licitación] Subtarea {sid}: {desc[:60]}... (req: {cap})")

        # Fase licitación: consultar a todos los workers
        bids: list[tuple[Worker, Bid]] = []
        for worker in workers:
            if worker.current_load >= 3:
                print(f"  {worker.id}: saturado (carga={worker.current_load}), omitido")
                continue
            bid = solicitar_bid(worker, subtarea)
            print(
                f"  {worker.id}: confidence={bid.confidence:.2f}, "
                f"tokens={bid.estimated_tokens}, utility={bid.utility:.4f}"
            )
            if bid.confidence > 0.0:
                bids.append((worker, bid))

        if not bids:
            print(f"  Sin workers disponibles para subtarea {sid}. Saltando.")
            resultados[sid] = f"[Sin resultado para: {desc}]"
            continue

        # Fase asignación: maximizar utilidad = confianza / tokens_estimados
        worker_ganador, bid_ganador = max(bids, key=lambda x: x[1].utility)
        print(f"  Ganador: {worker_ganador.id} (utility={bid_ganador.utility:.4f})")

        # Fase ejecución
        worker_ganador.current_load += 1
        contexto = "\n".join(
            f"Subtarea {k}: {v[:200]}" for k, v in resultados.items()
        )
        resultado = ejecutar_subtarea(worker_ganador, subtarea, contexto)
        worker_ganador.current_load -= 1
        resultados[sid] = resultado
        print(f"  Resultado: {resultado[:80]}...")

    # Síntesis final
    print("\n[Orquestador] Sintetizando resultados...")
    return sintetizar(tarea, resultados)


if __name__ == "__main__":
    # Definir workers con capacidades explícitas
    # (sin descripción explícita, la asignación sería cuasi-aleatoria)
    workers = [
        Worker(
            id="W1",
            capabilities=["búsqueda y síntesis de información", "resumen ejecutivo"],
            current_load=0,
        ),
        Worker(
            id="W2",
            capabilities=["análisis técnico", "evaluación de tecnologías", "comparativas"],
            current_load=1,
        ),
        Worker(
            id="W3",
            capabilities=["redacción estructurada", "elaboración de reportes", "síntesis"],
            current_load=0,
        ),
    ]

    tarea = (
        "Prepara un informe breve sobre las principales diferencias entre "
        "los patrones de memoria en agentes IA: memoria en contexto, "
        "memoria externa (vector store) y memoria paramétrica."
    )
    print(f"Tarea: {tarea}\n")
    resultado = mercado(tarea, workers)
    print(f"\nResultado final:\n{resultado}")
