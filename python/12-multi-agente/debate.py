# Patrón Debate: N agentes generan respuestas independientes, luego leen las de los demás
# y actualizan las suyas por R rondas. Agregación por mayoría o árbitro LLM.
#
# Cómo ejecutar:
#   make py SCRIPT=python/12-multi-agente/debate.py
#
# Qué esperar:
#   N agentes generan respuestas independientes, luego se leen entre si
#   por R rondas. Agregacion por mayoria o arbitro LLM al final.
#
# Variables de entorno:
#   MODEL — modelo a usar (default: claude-sonnet-4-6)

import os
from anthropic import Anthropic
from collections import Counter

client = Anthropic()
MODEL_AGENTS = "claude-haiku-4-5-20251001"   # barato para N agentes × R rondas
MODEL_ARBITER = "claude-sonnet-4-6"           # árbitro más capaz para síntesis final


def llamar_llm(system: str, messages: list, model: str = MODEL_AGENTS, temperature: float = 0.7) -> str:
    resp = client.messages.create(
        model=model,
        max_tokens=600,
        system=system,
        messages=messages,
        temperature=temperature,
    )
    return resp.content[0].text.strip()


def majority_vote(respuestas: list[str]) -> str:
    """Devuelve la respuesta más frecuente. Si no hay mayoría clara, devuelve la primera."""
    # Normalizar para comparación: minúsculas, sin espacios sobrantes
    normalizadas = [r.lower().strip() for r in respuestas]
    conteo = Counter(normalizadas)
    modal, _ = conteo.most_common(1)[0]
    # Devolver el original correspondiente a la versión normalizada modal
    for r in respuestas:
        if r.lower().strip() == modal:
            return r
    return respuestas[0]


def llm_arbiter(pregunta: str, respuestas: list[str]) -> str:
    """Un LLM separado (más fuerte) sintetiza las respuestas del pool."""
    pool_texto = "\n\n".join(
        f"Agente {i + 1}:\n{r}" for i, r in enumerate(respuestas)
    )
    system = (
        "Eres un árbitro experto. Lee las siguientes respuestas de distintos agentes "
        "a la misma pregunta. Identifica cuál razonamiento es más sólido y sintetiza "
        "la mejor respuesta posible. Si hay contradicción, explica cuál es correcta y por qué."
    )
    messages = [
        {
            "role": "user",
            "content": (
                f"Pregunta original: {pregunta}\n\n"
                f"Respuestas de los agentes:\n{pool_texto}\n\n"
                "Proporciona la respuesta final más precisa."
            ),
        }
    ]
    return llamar_llm(system, messages, model=MODEL_ARBITER, temperature=0.0)


def debate(
    pregunta: str,
    n_agents: int = 3,
    n_rounds: int = 2,
    use_arbiter: bool = False,
) -> str:
    """
    Debate multi-agente.

    Args:
        pregunta:    La pregunta o problema a resolver.
        n_agents:    Número de agentes debatientes (recomendado: 3).
        n_rounds:    Rondas de actualización tras la ronda 0 (máximo útil: 2).
        use_arbiter: Si True, un árbitro LLM sintetiza; si False, mayoría simple.

    Returns:
        Respuesta final agregada.
    """
    agent_system = (
        "Eres un agente analítico. Responde con precisión y razonamiento claro. "
        "Cuando veas las respuestas de otros agentes, actualiza la tuya si tienen razón; "
        "mantén tu posición y justifícala si no la tienen."
    )

    # Ronda 0: respuestas independientes con temperatura alta para diversidad
    # Temperatura 0.7 intencional — si todos generan lo mismo no hay debate útil
    respuestas: list[str] = []
    print(f"[Ronda 0] Generando {n_agents} respuestas independientes...")
    for i in range(n_agents):
        r = llamar_llm(
            system=agent_system,
            messages=[{"role": "user", "content": pregunta}],
            temperature=0.7,
        )
        respuestas.append(r)
        print(f"  Agente {i + 1}: {r[:80]}...")

    # Rondas de debate: cada agente lee a los demás y actualiza
    for ronda in range(n_rounds):
        print(f"\n[Ronda {ronda + 1}] Actualizando respuestas...")
        nuevas_respuestas: list[str] = []
        for i in range(n_agents):
            otros = [
                f"Agente {j + 1}: {respuestas[j]}"
                for j in range(n_agents)
                if j != i
            ]
            otros_texto = "\n\n".join(otros)
            actualizada = llamar_llm(
                system=agent_system,
                messages=[
                    {"role": "user", "content": pregunta},
                    {"role": "assistant", "content": respuestas[i]},
                    {
                        "role": "user",
                        "content": (
                            f"Otros agentes respondieron:\n{otros_texto}\n\n"
                            "Usa sus argumentos para mejorar tu respuesta. "
                            "Si tienen razón en algo, actualiza. "
                            "Si no, mantén tu posición y justifica por qué."
                        ),
                    },
                ],
                temperature=0.3,  # menor temperatura en rondas de actualización
            )
            nuevas_respuestas.append(actualizada)
            print(f"  Agente {i + 1} (actualizado): {actualizada[:80]}...")
        respuestas = nuevas_respuestas

    # Agregación
    print("\n[Agregación]")
    if use_arbiter:
        print("  Usando árbitro LLM...")
        return llm_arbiter(pregunta, respuestas)
    else:
        print("  Usando majority_vote...")
        return majority_vote(respuestas)


if __name__ == "__main__":
    pregunta = (
        "Un tren parte de la ciudad A a 60 km/h. Otro tren parte simultáneamente "
        "de la ciudad B a 90 km/h en dirección contraria. Las ciudades están a 300 km. "
        "¿En cuántos minutos se cruzan los trenes?"
    )
    print(f"Pregunta: {pregunta}\n")

    # Debate con majority_vote (más barato)
    resultado = debate(pregunta, n_agents=3, n_rounds=2, use_arbiter=False)
    print(f"\nRespuesta final (majority_vote):\n{resultado}")

    print("\n" + "=" * 60 + "\n")

    # Debate con árbitro (resuelve oracle gap)
    resultado_arbitro = debate(pregunta, n_agents=3, n_rounds=2, use_arbiter=True)
    print(f"\nRespuesta final (árbitro LLM):\n{resultado_arbitro}")
