"""ReAct (Reason + Act) — Yao et al. 2022 (arXiv:2210.03629).

Implementación text-based fiel al paper: el modelo genera Thought + Action en texto
libre; el ejecutor parsea la acción, llama la herramienta e inyecta la Observation.
El ciclo continúa hasta Action: Finish[respuesta] o MAX_ITERATIONS.

Requiere: pip install anthropic
"""
import os
import re
from dataclasses import dataclass
from typing import Callable
import anthropic

MODEL = os.environ.get("MODEL", "claude-sonnet-4-6")
MAX_ITERATIONS = 10

# Few-shot: 2 trayectorias completas calibran el formato Thought/Action/Observation
#
# Cómo ejecutar:
#   make py SCRIPT=python/08-bucle/react_agent.py
#
# Qué esperar:
#   El agente resuelve una pregunta multi-paso con el patron ReAct fiel al paper.
#   Muestra Thought/Action/Observation en cada iteración hasta Action: Finish[].
#
# Variables de entorno:
#   MODEL — modelo a usar (default: claude-sonnet-4-6)

FEW_SHOT = """\
Thought: Necesito buscar la capital de Australia.
Action: Search[capital Australia]
Observation: La capital de Australia es Canberra.
Thought: Tengo la respuesta.
Action: Finish[Canberra]

---

Thought: Necesito saber quién fue el padre de Zeus.
Action: Search[padre Zeus mitología]
Observation: Crono es el padre de Zeus. Era un Titán que gobernó el cosmos.
Thought: La respuesta es Crono.
Action: Finish[Crono]

---

"""

SYSTEM = (
    "Responde siguiendo el formato Thought/Action/Observation del ejemplo. "
    "Las acciones disponibles son: Search[query] y Finish[respuesta]."
)


@dataclass
class ReActAgent:
    client: anthropic.Anthropic
    tools: dict[str, Callable[[str], str]]
    model: str = MODEL
    max_iterations: int = MAX_ITERATIONS

    def _parse_action(self, text: str) -> tuple[str, str] | None:
        """Extrae (tool_name, tool_args) del texto. Formato: Action: Name[args]"""
        m = re.search(r"Action:\s*(\w+)\[(.+?)\]", text, re.DOTALL)
        return (m.group(1), m.group(2).strip()) if m else None

    def run(self, task: str) -> str:
        prompt = FEW_SHOT + f"Task: {task}\n"

        for i in range(self.max_iterations):
            # stop_sequences corta antes de "Observation:" — el ejecutor inyecta esa parte
            response = self.client.messages.create(
                model=self.model,
                max_tokens=300,
                system=SYSTEM,
                messages=[{"role": "user", "content": prompt}],
                stop_sequences=["Observation:"],
            )
            generated = response.content[0].text
            prompt += generated

            print(f"[iter {i+1}] {generated.strip()[:100]}")

            action = self._parse_action(generated)
            if not action:
                break

            tool_name, tool_args = action

            if tool_name == "Finish":
                return tool_args

            fn = self.tools.get(tool_name)
            observation = fn(tool_args) if fn else f"[Error: herramienta '{tool_name}' no encontrada]"
            prompt += f"Observation: {observation}\n"
            print(f"  Observation: {observation[:80]}")

        return "[MAX_ITERATIONS sin respuesta]"


if __name__ == "__main__":
    KB = {
        "capital españa":    "Madrid es la capital de España.",
        "capital francia":   "París es la capital de Francia.",
        "capital alemania":  "Berlín es la capital de Alemania.",
        "padre zeus":        "Crono es el padre de Zeus en la mitología griega.",
        "capital australia": "La capital de Australia es Canberra.",
    }

    def search(query: str) -> str:
        q = query.lower()
        for key, val in KB.items():
            if all(w in q for w in key.split()):
                return val
        return "No encontré información sobre esa consulta."

    client = anthropic.Anthropic()
    agent = ReActAgent(client=client, tools={"Search": search})

    result = agent.run("¿Cuáles son las capitales de España y Francia?")
    print(f"\nRespuesta final: {result}")
