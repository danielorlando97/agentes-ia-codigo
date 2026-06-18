"""Plan-and-Execute: separa planificación de ejecución.

El planificador (LLM caro) genera una lista de pasos con una sola llamada.
El executor (LLM barato) implementa cada paso con tool_use nativo.
Dynamic replanning: si un paso falla, el planificador se invoca de nuevo
con el estado actual para regenerar el plan restante.

Requiere: pip install anthropic

Cómo ejecutar:
    make py SCRIPT=python/08-bucle/plan_execute.py

Qué esperar:
    Planificador genera plan JSON de pasos, executor implementa cada uno con tools.
    Si un paso falla, el planificador se re-invoca para regenerar los pasos restantes.

Variables de entorno:
    MODEL          — modelo planificador (default: claude-sonnet-4-6)
    SMALL_MODEL    — modelo executor (default: claude-haiku-4-5-20251001)
"""
import os
import re
from dataclasses import dataclass
from typing import Callable
import anthropic

PLANNER_MODEL  = os.environ.get("PLANNER_MODEL", "claude-sonnet-4-6")
EXECUTOR_MODEL = os.environ.get("EXECUTOR_MODEL", "claude-haiku-4-5-20251001")

PLANNER_PROMPT = """\
Genera una lista numerada de pasos atómicos para completar esta tarea.
Cada paso debe comenzar con un verbo de acción y ser ejecutable de forma independiente.

Tarea: {tarea}
Estado actual: {estado}

Responde solo con la lista numerada, sin explicaciones adicionales."""


def parsear_lista_numerada(texto: str) -> list[str]:
    """Extrae pasos de una lista numerada '1. paso' o '1) paso'."""
    pasos = re.findall(r"^\d+[.)]\s+(.+)$", texto, re.MULTILINE)
    return [p.strip() for p in pasos if p.strip()]


@dataclass
class PlanAndExecuteAgent:
    client: anthropic.Anthropic
    tools: list[dict]                # schemas para la API
    tool_fns: dict[str, Callable]    # implementaciones reales
    planner_model:  str = PLANNER_MODEL
    executor_model: str = EXECUTOR_MODEL
    max_replan:     int = 2

    def _planificar(self, tarea: str, estado: str = "Sin ejecución previa") -> list[str]:
        resp = self.client.messages.create(
            model=self.planner_model,
            max_tokens=600,
            messages=[{"role": "user", "content": PLANNER_PROMPT.format(tarea=tarea, estado=estado)}],
        )
        return parsear_lista_numerada(resp.content[0].text)

    def _ejecutar_paso(self, paso: str, contexto: str) -> tuple[str, bool]:
        """Ejecuta un paso con tool_use. Devuelve (resultado, éxito)."""
        messages: list[dict] = [{
            "role": "user",
            "content": f"Contexto previo:\n{contexto}\n\nEjecuta este paso: {paso}",
        }]
        while True:
            resp = self.client.messages.create(
                model=self.executor_model,
                max_tokens=512,
                tools=self.tools,
                messages=messages,
            )
            if resp.stop_reason == "end_turn":
                return "".join(b.text for b in resp.content if b.type == "text"), True
            if resp.stop_reason == "tool_use":
                results = []
                for b in resp.content:
                    if b.type == "tool_use":
                        fn = self.tool_fns.get(b.name)
                        r = fn(**b.input) if fn else f"[tool '{b.name}' no encontrada]"
                        results.append({"type": "tool_result", "tool_use_id": b.id, "content": str(r)})
                messages.append({"role": "assistant", "content": resp.content})
                messages.append({"role": "user", "content": results})
            else:
                return "[paso no completado]", False

    def run(self, tarea: str) -> str:
        plan = self._planificar(tarea)
        print(f"Plan ({len(plan)} pasos):")
        for j, p in enumerate(plan, 1):
            print(f"  {j}. {p}")

        resultados: list[str] = []
        i = 0
        replans = 0

        while i < len(plan):
            contexto = "\n".join(f"Paso {j+1}: {r}" for j, r in enumerate(resultados))
            resultado, exito = self._ejecutar_paso(plan[i], contexto)

            print(f"\n[paso {i+1}/{len(plan)}] {'✓' if exito else '✗'} {plan[i][:60]}")

            if not exito and replans < self.max_replan:
                estado = f"Completados: {resultados}\nFalló: {plan[i]}"
                nuevo = self._planificar(tarea, estado)
                if nuevo:
                    plan = plan[:i] + nuevo
                    replans += 1
                    print(f"  → replan #{replans}: {len(nuevo)} pasos nuevos")
                    continue

            resultados.append(resultado)
            i += 1

        sintesis = self.client.messages.create(
            model=self.planner_model,
            max_tokens=400,
            messages=[{
                "role": "user",
                "content": (
                    f"Tarea: {tarea}\n\n"
                    f"Resultados de los pasos:\n" + "\n".join(resultados) +
                    "\n\nResume qué se logró en 2-3 frases."
                ),
            }],
        )
        return sintesis.content[0].text


if __name__ == "__main__":
    client = anthropic.Anthropic()

    TOOLS = [{
        "name": "calcular",
        "description": "Evalúa una expresión matemática simple.",
        "input_schema": {
            "type": "object",
            "properties": {"expresion": {"type": "string", "description": "ej: '15 * 8'"}},
            "required": ["expresion"],
        },
    }]

    def calcular(expresion: str) -> str:
        try:
            return str(eval(expresion, {"__builtins__": {}}, {}))
        except Exception as e:
            return f"Error: {e}"

    agent = PlanAndExecuteAgent(client=client, tools=TOOLS, tool_fns={"calcular": calcular})
    resultado = agent.run(
        "Calcula el área de un rectángulo de 15 por 8 metros. "
        "Luego calcula cuántas baldosas de 0.25 m² se necesitan para cubrirlo."
    )
    print(f"\n=== Resultado final ===\n{resultado}")
