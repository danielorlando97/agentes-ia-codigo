# Fase 1: loop ReAct mínimo sin herramientas. Verifica que el system prompt
# produce el schema JSON correcto antes de añadir complejidad.
#
# Cómo ejecutar:
#   make py SCRIPT=python/16-proyecto/fase1_loop.py
#
# Qué esperar:
#   Loop ReAct minimo verificando que el system prompt produce schema JSON correcto.
#   Fase de validacion antes de añadir herramientas reales.
#
# Variables de entorno:
#   MODEL — modelo a usar (default: claude-sonnet-4-6)

import anthropic
import json
import os

SYSTEM_PROMPT = """Eres un agente de revisión de código.
Recibes código Python y produces una revisión técnica estructurada.

Tu revisión debe identificar:
- Bugs (severidad: critical, high, medium, low)
- Problemas de estilo o mantenibilidad
- Sugerencias de mejora

Responde SIEMPRE en JSON con este schema:
{
  "hallazgos": [
    {
      "linea": <número o null>,
      "severidad": "<critical|high|medium|low>",
      "tipo": "<bug|estilo|rendimiento|seguridad>",
      "descripcion": "<descripción del hallazgo>",
      "sugerencia": "<cómo corregirlo>"
    }
  ],
  "resumen": "<párrafo de resumen de la revisión>"
}"""


def revisar_codigo(codigo: str) -> dict:
    cliente = anthropic.Anthropic()

    respuesta = cliente.messages.create(
        model=os.environ.get("MODEL", "claude-sonnet-4-6"),
        max_tokens=2048,
        system=SYSTEM_PROMPT,
        messages=[
            {"role": "user", "content": f"Revisa este código:\n\n```python\n{codigo}\n```"}
        ]
    )

    return json.loads(respuesta.content[0].text)


if __name__ == "__main__":
    codigo_ejemplo = """
def calcular_promedio(numeros):
    total = 0
    for n in numeros:
        total += n
    return total / len(numeros)  # bug: ZeroDivisionError si numeros está vacío

usuarios = {}
def get_usuario(id):
    return usuarios[id]  # bug: KeyError si id no existe
"""
    resultado = revisar_codigo(codigo_ejemplo)
    print(json.dumps(resultado, indent=2, ensure_ascii=False))
