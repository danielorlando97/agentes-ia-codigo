# Formato ReAct (Reasoning + Acting) para modelos sin function calling nativo.
#
# ReAct intercala Thought/Action/Observation en texto libre.
# El cliente parsea la Action con regex, ejecuta la herramienta,
# e inyecta la Observation antes de que el modelo continúe.
#
# Stop sequence "Observation:" interrumpe la generación para
# que el cliente inyecte el resultado real de la herramienta.
#
# Cómo ejecutar:
#   make py FILE=python/05-herramientas/10-formatos/react-text.py

import os
import re
import anthropic

MODEL = os.environ.get("MODEL", "claude-sonnet-4-6")

# --- System prompt ReAct ---

REACT_SYSTEM = """Responde usando EXACTAMENTE el siguiente formato:

Thought: [tu razonamiento sobre qué hacer a continuación]
Action: ToolName[argumento]
Observation: [resultado de la herramienta — lo inyecta el sistema]

Repite Thought/Action/Observation hasta tener la respuesta final, luego:
Thought: Tengo la información necesaria para responder.
Action: Finish[respuesta completa aquí]

Herramientas disponibles:
- Search[query]: Busca información. Ejemplo: Search[capital of France]
- Calculate[expresion]: Evalúa expresión matemática. Ejemplo: Calculate[15 * 8 + 3]
- Finish[respuesta]: Termina y devuelve la respuesta final.

IMPORTANTE: usa exactamente el formato ToolName[argumento] con corchetes."""

REACT_FEW_SHOT = """
Ejemplo:
Pregunta: ¿Cuánto es el doble de la población de Madrid?
Thought: Necesito buscar la población de Madrid y luego multiplicarla por 2.
Action: Search[population of Madrid]
Observation: La población de Madrid es aproximadamente 3.3 millones de personas.
Thought: Ahora calculo el doble: 3.3 * 2.
Action: Calculate[3.3 * 2]
Observation: 6.6
Thought: Tengo la respuesta final.
Action: Finish[El doble de la población de Madrid es 6.6 millones de personas]

---"""


# --- Herramientas mock ---

def mock_search(query: str) -> str:
    db = {
        "population of madrid": "La población de Madrid es aproximadamente 3.3 millones.",
        "population of tokyo": "La población de Tokio es aproximadamente 13.96 millones.",
        "capital of france": "La capital de Francia es París.",
        "capital of japan": "La capital de Japón es Tokio.",
        "height of eiffel tower": "La Torre Eiffel mide 330 metros.",
        "distance madrid barcelona": "La distancia Madrid-Barcelona es ~621 km.",
    }
    lower = query.lower()
    for key, val in db.items():
        if lower in key or key in lower:
            return val
    return f'No se encontró información específica sobre "{query}". Intenta con términos más simples.'


def mock_calculate(expression: str) -> str:
    try:
        sanitized = re.sub(r"[^0-9+\-*/().\s]", "", expression)
        if not sanitized.strip():
            raise ValueError("expresión vacía")
        result = eval(sanitized, {"__builtins__": {}})  # noqa: S307
        return str(round(float(result), 4))
    except Exception as e:
        return f'Error: no se pudo evaluar "{expression}": {e}'


# --- Parser de ReAct ---

def parse_react_output(text: str) -> tuple[str, str, str] | None:
    thought_match = re.search(r"Thought:\s*(.+?)(?=Action:|$)", text, re.S)
    thought = thought_match.group(1).strip() if thought_match else ""

    action_match = re.search(r"Action:\s*(\w+)\[([^\]]*)\]", text)
    if not action_match:
        return None

    return thought, action_match.group(1), action_match.group(2).strip()


# --- Loop ReAct ---

def react_loop(pregunta: str, max_pasos: int = 10) -> str:
    client = anthropic.Anthropic()
    contexto = REACT_FEW_SHOT + f"\nPregunta: {pregunta}\n"

    print(f"Pregunta: {pregunta}\n")

    for paso in range(max_pasos):
        response = client.messages.create(
            model=MODEL,
            max_tokens=512,
            system=REACT_SYSTEM,
            messages=[{"role": "user", "content": contexto}],
            stop_sequences=["Observation:"],
        )

        generado = "".join(
            b.text for b in response.content if b.type == "text"
        )

        print(f"[Paso {paso + 1}]")
        print(generado.strip())

        parsed = parse_react_output(generado)
        if not parsed:
            print("  [warn] no se encontró Action en el output — terminando")
            break

        thought, tool_name, argument = parsed

        if tool_name == "Finish":
            print(f"\n[Finish] {argument}")
            return argument

        if tool_name == "Search":
            observacion = mock_search(argument)
        elif tool_name == "Calculate":
            observacion = mock_calculate(argument)
        else:
            observacion = f"Error: herramienta '{tool_name}' no existe"

        print(f"Observation: {observacion}\n")
        contexto += generado + f"Observation: {observacion}\n"

    return "Max pasos alcanzados sin respuesta final"


def main() -> None:
    print("=== Formato ReAct (Thought/Action/Observation) ===\n")

    resultado = react_loop(
        "¿Cuántos metros cuadrados tiene una habitación de 4.5m × 3.2m?"
    )

    print(f"\n=== Respuesta final ===\n{resultado}")


if __name__ == "__main__":
    main()
