"""Reflexion — Shinn et al. 2023 (arXiv:2303.11366).

ReflexionAgent: loop actor → evaluador → reflector hasta MAX_INTENTOS.
EvaluatorProtocol: interfaz común; tres implementaciones:
  - UnitTestEvaluator: test determinista, la opción más confiable
  - HeuristicEvaluator: heurísticas sin modelo (pasos repetidos, respuesta vacía)
  - LLMJudgeEvaluator: LLM-as-judge para tareas sin criterio computable
ReflectorAgent: genera la reflexión verbal sobre el fallo.
sliding_window_memory: mantiene solo las últimas N reflexiones en el contexto.

Requiere: pip install anthropic

Cómo ejecutar:
    make py SCRIPT=python/08-bucle/reflexion.py

Qué esperar:
    Actor genera solución, evaluador la juzga, reflector escribe crítica memorizable.
    Hasta MAX_INTENTOS reintentos. Muestra la traza completa de mejora iterativa.

Variables de entorno:
    MODEL — modelo actor/evaluador/reflector (default: claude-sonnet-4-6)
"""
import os
import re
from abc import ABC, abstractmethod
from dataclasses import dataclass, field
from typing import Callable
import anthropic

MODEL = os.environ.get("MODEL", "claude-sonnet-4-6")
MAX_INTENTOS   = 3
MAX_REFLEXIONES = 3

PROMPT_REFLEXION = """\
Tarea: {tarea}
Intento #{intento} — resultado: FALLIDO
Trayectoria:
{trayectoria}
Feedback del evaluador: {feedback}

Reflexiona sobre qué salió mal y qué harías diferente.
Sé específico (máximo 80 palabras)."""

PROMPT_ACTOR = """\
{reflexiones}Completa esta tarea:

{tarea}"""


@dataclass
class Trayectoria:
    pasos:           list[str] = field(default_factory=list)
    resultado_final: str       = ""

    def log(self, paso: str) -> None:
        self.pasos.append(paso)

    def to_text(self) -> str:
        return "\n".join(self.pasos)


class EvaluatorProtocol(ABC):
    @abstractmethod
    def evaluar(self, trayectoria: Trayectoria, tarea: str) -> tuple[bool, str]:
        """Devuelve (éxito, feedback). feedback explica el fallo cuando éxito=False."""
        ...


class UnitTestEvaluator(EvaluatorProtocol):
    """Test determinista. La opción más confiable cuando el criterio es computable."""
    def __init__(self, test_fn: Callable[[str], bool]):
        self.test_fn = test_fn

    def evaluar(self, trayectoria: Trayectoria, tarea: str) -> tuple[bool, str]:
        try:
            ok = self.test_fn(trayectoria.resultado_final)
            return ok, ("Test superado." if ok else "El resultado no cumple el criterio del test.")
        except Exception as e:
            return False, f"Excepción en el test: {e}"


class HeuristicEvaluator(EvaluatorProtocol):
    """Heurísticas sin llamada a modelo. Señal ruidosa pero instantánea."""
    def evaluar(self, trayectoria: Trayectoria, tarea: str) -> tuple[bool, str]:
        if not trayectoria.resultado_final.strip():
            return False, "El agente no produjo respuesta."
        if len(trayectoria.pasos) >= 2 and trayectoria.pasos[-1] == trayectoria.pasos[-2]:
            return False, "El agente repitió el mismo paso dos veces consecutivas."
        return True, "Heurísticas superadas."


class LLMJudgeEvaluator(EvaluatorProtocol):
    """LLM-as-judge para tareas sin criterio computable. Introduce bias del juez."""
    def __init__(self, client: anthropic.Anthropic, model: str = MODEL):
        self.client = client
        self.model  = model

    def evaluar(self, trayectoria: Trayectoria, tarea: str) -> tuple[bool, str]:
        resp = self.client.messages.create(
            model=self.model,
            max_tokens=150,
            messages=[{
                "role": "user",
                "content": (
                    f"Tarea: {tarea}\n"
                    f"Respuesta: {trayectoria.resultado_final}\n\n"
                    "¿La respuesta completa la tarea? Responde: ÉXITO o FALLO y una frase de feedback."
                ),
            }],
        )
        texto = resp.content[0].text
        return "ÉXITO" in texto.upper(), texto


class ReflectorAgent:
    """Genera la reflexión verbal sobre el fallo del intento anterior."""
    def __init__(self, client: anthropic.Anthropic, model: str = MODEL):
        self.client = client
        self.model  = model

    def reflexionar(self, tarea: str, intento: int, trayectoria: Trayectoria, feedback: str) -> str:
        resp = self.client.messages.create(
            model=self.model,
            max_tokens=200,
            messages=[{
                "role": "user",
                "content": PROMPT_REFLEXION.format(
                    tarea=tarea,
                    intento=intento,
                    trayectoria=trayectoria.to_text()[:1500],
                    feedback=feedback,
                ),
            }],
        )
        return resp.content[0].text.strip()


def sliding_window_memory(reflexiones: list[str], max_n: int = MAX_REFLEXIONES) -> list[str]:
    return reflexiones[-max_n:]


class ReflexionAgent:
    """Loop actor → evaluador → reflector. Cada intento parte de las reflexiones anteriores."""

    def __init__(
        self,
        client:       anthropic.Anthropic,
        evaluator:    EvaluatorProtocol,
        actor_model:  str = MODEL,
        reflector:    ReflectorAgent | None = None,
        max_intentos: int = MAX_INTENTOS,
    ):
        self.client       = client
        self.evaluator    = evaluator
        self.reflector    = reflector or ReflectorAgent(client)
        self.actor_model  = actor_model
        self.max_intentos = max_intentos

    def _ejecutar_actor(self, tarea: str, reflexiones: list[str]) -> Trayectoria:
        bloque = ""
        if reflexiones:
            items = "\n".join(f"- {r}" for r in reflexiones)
            bloque = f"Reflexiones de intentos previos:\n{items}\n\n"

        resp = self.client.messages.create(
            model=self.actor_model,
            max_tokens=500,
            messages=[{"role": "user", "content": PROMPT_ACTOR.format(tarea=tarea, reflexiones=bloque)}],
        )
        tray = Trayectoria()
        tray.log(f"Actor ejecutado con {len(reflexiones)} reflexiones previas.")
        tray.resultado_final = resp.content[0].text.strip()
        return tray

    def run(self, tarea: str) -> str:
        memoria: list[str] = []

        for intento in range(1, self.max_intentos + 1):
            tray = self._ejecutar_actor(tarea, sliding_window_memory(memoria))
            exito, feedback = self.evaluator.evaluar(tray, tarea)

            print(f"[intento {intento}/{self.max_intentos}] {'✓' if exito else '✗'} {feedback[:70]}")

            if exito:
                return tray.resultado_final

            if intento < self.max_intentos:
                reflexion = self.reflector.reflexionar(tarea, intento, tray, feedback)
                memoria.append(reflexion)
                print(f"  Reflexión: {reflexion[:90]}")

        return tray.resultado_final  # mejor esfuerzo tras agotar intentos


if __name__ == "__main__":
    client = anthropic.Anthropic()

    # Evaluador determinista: la respuesta debe contener un número entre 10 y 50
    def test_rango(resultado: str) -> bool:
        numeros = [int(n) for n in re.findall(r"\b\d+\b", resultado)]
        return any(10 <= n <= 50 for n in numeros)

    evaluator = UnitTestEvaluator(test_rango)
    agent = ReflexionAgent(client=client, evaluator=evaluator)

    resultado = agent.run(
        "Escribe exactamente un número entero entre 10 y 50, "
        "seguido de por qué elegiste ese número."
    )
    print(f"\nResultado final: {resultado[:200]}")
