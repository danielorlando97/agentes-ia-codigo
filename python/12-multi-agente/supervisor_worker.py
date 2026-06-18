# Patrón supervisor/worker: un LLM descompone la tarea y despacha a workers especializados.
#
# Cómo ejecutar:
#   make py SCRIPT=python/12-multi-agente/supervisor_worker.py
#
# Qué esperar:
#   Supervisor descompone la tarea en subtareas independientes.
#   Workers especializados las ejecutan en paralelo y el supervisor sintetiza.
#
# Variables de entorno:
#   MODEL — modelo a usar (default: claude-sonnet-4-6)

import os
import json
from anthropic import Anthropic

client = Anthropic()
MODEL = os.environ.get("MODEL", "claude-haiku-4-5-20251001")

WORKERS = {
    "investigador": "Eres un investigador. Busca y sintetiza información factual. Devuelve hechos concretos.",
    "redactor":     "Eres un redactor. Redacta contenido claro y bien estructurado basado en el contexto dado.",
    "revisor":      "Eres un revisor. Identifica problemas concretos y devuelve el texto corregido.",
}

SUPERVISOR_SYSTEM = """Eres un supervisor que descompone tareas y las despacha a workers.
Workers disponibles: investigador, redactor, revisor.
Planifica los pasos necesarios. Responde SIEMPRE con JSON válido.

Para planificar: {"accion": "planificar", "pasos": [{"worker": "<nombre>", "instruccion": "<qué hacer>"}]}
Para terminar:   {"accion": "terminar", "respuesta": "<respuesta final>"}
Para redirigir:  {"accion": "redirigir", "worker": "<nombre>", "correccion": "<qué corregir>"}"""


def llamar_worker(worker: str, instruccion: str) -> str:
    resp = client.messages.create(
        model=MODEL,
        max_tokens=800,
        system=WORKERS[worker],
        messages=[{"role": "user", "content": instruccion}],
    )
    return resp.content[0].text


def llamar_supervisor(mensajes: list) -> dict:
    resp = client.messages.create(
        model=MODEL,
        max_tokens=600,
        system=SUPERVISOR_SYSTEM,
        messages=mensajes,
    )
    texto = resp.content[0].text
    # Extraer JSON de la respuesta aunque venga con texto extra
    inicio = texto.find("{")
    fin = texto.rfind("}") + 1
    return json.loads(texto[inicio:fin])


def supervisor_worker(tarea: str, max_rondas: int = 3) -> str:
    mensajes = [{"role": "user", "content": f"Tarea: {tarea}"}]
    resultados: dict[str, str] = {}

    # Fase 1: planificación
    decision = llamar_supervisor(mensajes)
    mensajes.append({"role": "assistant", "content": json.dumps(decision)})

    if decision["accion"] != "planificar":
        return decision.get("respuesta", "Error: supervisor no pudo planificar.")

    # Fase 2: ejecución del plan
    for paso in decision["pasos"]:
        worker = paso["worker"]
        instruccion = paso["instruccion"]

        # Sustituir referencias a outputs anteriores ($worker → resultado previo)
        for nombre, resultado in resultados.items():
            instruccion = instruccion.replace(f"${nombre}", resultado[:500])

        resultado = llamar_worker(worker, instruccion)
        resultados[worker] = resultado
        mensajes.append({
            "role": "user",
            "content": f"Resultado de {worker}:\n{resultado}"
        })

    # Fase 3: evaluación del supervisor
    for ronda in range(max_rondas):
        mensajes.append({
            "role": "user",
            "content": "¿La tarea está completa? Responde con JSON: terminar o redirigir."
        })
        decision = llamar_supervisor(mensajes)
        mensajes.append({"role": "assistant", "content": json.dumps(decision)})

        if decision["accion"] == "terminar":
            return decision["respuesta"]

        if decision["accion"] == "redirigir":
            worker = decision["worker"]
            correccion = decision["correccion"]
            resultado_corregido = llamar_worker(worker, correccion)
            resultados[worker] = resultado_corregido
            mensajes.append({
                "role": "user",
                "content": f"Resultado corregido de {worker}:\n{resultado_corregido}"
            })

    # Fallback: devolver el mejor resultado disponible
    return resultados.get("revisor", resultados.get("redactor", "Sin resultado."))


if __name__ == "__main__":
    tarea = "Escribe un párrafo explicando qué es el patrón supervisor/worker en sistemas multi-agente."
    print(f"Tarea: {tarea}\n")
    resultado = supervisor_worker(tarea)
    print(f"Resultado:\n{resultado}")
