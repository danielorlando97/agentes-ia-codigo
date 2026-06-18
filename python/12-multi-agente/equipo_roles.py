# Patrón Equipo de Roles (MetaGPT style): PM → Architect → ProjectManager → Engineer → QA
# Coordinados por un Message Pool con campo cause_by. Cada rol se activa cuando el pool
# contiene mensajes con cause_by en su lista de observes. Engineer tiene 3 reintentos ante bugs.
#
# Cómo ejecutar:
#   make py SCRIPT=python/12-multi-agente/equipo_roles.py
#
# Qué esperar:
#   Pipeline PM → Architect → Engineer → QA para una tarea de software.
#   Cada rol produce un artefacto que el siguiente consume via Message Pool.
#
# Variables de entorno:
#   MODEL — modelo a usar (default: claude-sonnet-4-6)

import os
from dataclasses import dataclass, field
from anthropic import Anthropic

client = Anthropic()
MODEL = os.environ.get("MODEL", "claude-haiku-4-5-20251001")

MAX_ENGINEER_RETRIES = 3


# ---------------------------------------------------------------------------
# Message Pool
# ---------------------------------------------------------------------------

@dataclass
class Message:
    content: str
    type: str      # "PRD", "SystemDesign", "Plan", "Code", "BugFix", "TestReport"
    cause_by: str  # id del rol que lo generó


class MessagePool:
    def __init__(self):
        self._messages: list[Message] = []

    def publish(self, content: str, type: str, cause_by: str) -> None:
        self._messages.append(Message(content=content, type=type, cause_by=cause_by))

    def filter(self, cause_by_list: list[str]) -> list[Message]:
        """Retorna todos los mensajes cuyo cause_by esté en la lista."""
        return [m for m in self._messages if m.cause_by in cause_by_list]

    def latest(self, type: str) -> Message | None:
        """Retorna el mensaje más reciente de ese tipo (evita que QA evalúe versiones antiguas)."""
        for msg in reversed(self._messages):
            if msg.type == type:
                return msg
        return None

    def has(self, type: str) -> bool:
        return any(m.type == type for m in self._messages)

    def summary(self) -> str:
        lines = [f"  [{m.cause_by}] {m.type}: {m.content[:60]}..." for m in self._messages]
        return "\n".join(lines)


# ---------------------------------------------------------------------------
# Roles
# ---------------------------------------------------------------------------

@dataclass
class Role:
    id: str
    produce: str          # tipo de artefacto que produce
    observes: list[str]   # cause_by values que activan este rol
    system_prompt: str


ROLES = {
    "pm": Role(
        id="pm",
        produce="PRD",
        observes=[],  # el PM se activa con el requisito del usuario, no desde el pool
        system_prompt=(
            "Eres un Product Manager senior. A partir del requisito del usuario, "
            "escribe un PRD (Product Requirements Document) claro y estructurado. "
            "Incluye: objetivo del producto, funcionalidades requeridas, "
            "criterios de aceptación y restricciones técnicas. "
            "Sé específico — el arquitecto necesita información concreta para diseñar."
        ),
    ),
    "architect": Role(
        id="architect",
        produce="SystemDesign",
        observes=["pm"],
        system_prompt=(
            "Eres un Arquitecto de Software. A partir del PRD, diseña la arquitectura del sistema. "
            "Incluye: componentes principales, interfaces entre ellos, "
            "decisiones de diseño clave (con justificación), y lista de archivos/módulos a crear. "
            "Sé concreto — el Project Manager necesita un plan ejecutable."
        ),
    ),
    "pm_mgr": Role(
        id="pm_mgr",
        produce="Plan",
        observes=["architect"],
        system_prompt=(
            "Eres un Project Manager técnico. A partir del System Design, "
            "crea un plan de implementación serializable. "
            "Lista las tareas de implementación en orden, con dependencias explícitas. "
            "El ingeniero ejecutará este plan directamente."
        ),
    ),
    "engineer": Role(
        id="engineer",
        produce="Code",
        observes=["pm_mgr"],
        system_prompt=(
            "Eres un Ingeniero de Software senior. A partir del plan de implementación, "
            "escribe el código funcional completo. "
            "Incluye todos los archivos necesarios, con sintaxis correcta. "
            "El QA ejecutará tests sobre este código — asegúrate de que sea ejecutable."
        ),
    ),
    "qa": Role(
        id="qa",
        produce="TestReport",
        observes=["engineer"],
        system_prompt=(
            "Eres un QA Engineer. Revisa el código del ingeniero contra el PRD y el System Design. "
            "Busca: bugs de lógica, casos borde no cubiertos, violaciones de los requisitos. "
            "Responde con JSON: "
            '{"tiene_bugs": true/false, "bugs": ["descripción bug 1", ...], '
            '"veredicto": "PASA" o "FALLA"}. '
            "Sé preciso — el ingeniero necesita descripciones concretas para corregir."
        ),
    ),
}


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

def llamar_llm(system: str, user: str, temperature: float = 0.0) -> str:
    resp = client.messages.create(
        model=MODEL,
        max_tokens=1200,
        system=system,
        messages=[{"role": "user", "content": user}],
        temperature=temperature,
    )
    return resp.content[0].text.strip()


def parsear_test_report(raw: str) -> dict:
    """Extrae JSON del TestReport aunque venga con texto adicional."""
    import json
    inicio = raw.find("{")
    fin = raw.rfind("}") + 1
    if inicio == -1 or fin == 0:
        return {"tiene_bugs": False, "bugs": [], "veredicto": "PASA"}
    try:
        return json.loads(raw[inicio:fin])
    except Exception:
        return {"tiene_bugs": False, "bugs": [], "veredicto": "PASA"}


# ---------------------------------------------------------------------------
# Pipeline principal
# ---------------------------------------------------------------------------

def equipo_roles(requisito_usuario: str) -> str:
    """
    Pipeline SOP: PM → Architect → PM_Manager → Engineer → QA.
    El Engineer tiene hasta MAX_ENGINEER_RETRIES intentos para corregir bugs.

    Args:
        requisito_usuario: Descripción de la funcionalidad a implementar.

    Returns:
        Código final validado por QA (o la mejor versión disponible).
    """
    pool = MessagePool()

    # --- PM: genera PRD (no observa pool, se activa con el requisito) ---
    print("[PM] Generando PRD...")
    prd = llamar_llm(
        system=ROLES["pm"].system_prompt,
        user=f"Requisito del usuario: {requisito_usuario}",
    )
    pool.publish(prd, type="PRD", cause_by="pm")
    print(f"  PRD: {prd[:80]}...")

    # --- Architect: observa PM → genera SystemDesign ---
    print("\n[Architect] Diseñando sistema...")
    prd_msg = pool.latest("PRD")
    system_design = llamar_llm(
        system=ROLES["architect"].system_prompt,
        user=f"PRD:\n{prd_msg.content}",
    )
    pool.publish(system_design, type="SystemDesign", cause_by="architect")
    print(f"  SystemDesign: {system_design[:80]}...")

    # --- PM_Manager: observa Architect → genera Plan ---
    print("\n[ProjectManager] Creando plan de implementación...")
    arch_msg = pool.latest("SystemDesign")
    plan = llamar_llm(
        system=ROLES["pm_mgr"].system_prompt,
        user=f"PRD:\n{prd_msg.content}\n\nSystem Design:\n{arch_msg.content}",
    )
    pool.publish(plan, type="Plan", cause_by="pm_mgr")
    print(f"  Plan: {plan[:80]}...")

    # --- Engineer: observa PM_Manager → genera Code ---
    print("\n[Engineer] Escribiendo código...")
    plan_msg = pool.latest("Plan")
    contexto_eng = (
        f"PRD:\n{prd_msg.content}\n\n"
        f"System Design:\n{arch_msg.content}\n\n"
        f"Plan de implementación:\n{plan_msg.content}"
    )
    codigo = llamar_llm(
        system=ROLES["engineer"].system_prompt,
        user=contexto_eng,
        temperature=0.2,
    )
    pool.publish(codigo, type="Code", cause_by="engineer")
    print(f"  Code: {codigo[:80]}...")

    # --- QA: observa Engineer → genera TestReport ---
    print("\n[QA] Revisando código...")
    code_msg = pool.latest("Code")
    qa_contexto = (
        f"PRD (requisitos):\n{prd_msg.content}\n\n"
        f"System Design:\n{arch_msg.content}\n\n"
        f"Código a revisar:\n{code_msg.content}"
    )
    test_report_raw = llamar_llm(
        system=ROLES["qa"].system_prompt,
        user=qa_contexto,
    )
    pool.publish(test_report_raw, type="TestReport", cause_by="qa")
    reporte = parsear_test_report(test_report_raw)
    print(f"  Veredicto inicial: {reporte['veredicto']}")

    # --- Bucle Engineer ↔ QA: hasta MAX_ENGINEER_RETRIES reintentos ---
    intento = 0
    while reporte.get("tiene_bugs") and intento < MAX_ENGINEER_RETRIES:
        intento += 1
        bugs_texto = "\n".join(f"- {b}" for b in reporte.get("bugs", []))
        print(f"\n[Engineer] Intento {intento}/{MAX_ENGINEER_RETRIES} — corrigiendo bugs:")
        print(f"  {bugs_texto[:120]}")

        codigo_actual = pool.latest("Code")
        fix = llamar_llm(
            system=ROLES["engineer"].system_prompt,
            user=(
                f"El QA encontró los siguientes bugs en tu código:\n{bugs_texto}\n\n"
                f"Código actual:\n{codigo_actual.content}\n\n"
                f"PRD original:\n{prd_msg.content}\n\n"
                "Corrige todos los bugs manteniendo la funcionalidad completa."
            ),
            temperature=0.2,
        )
        pool.publish(fix, type="BugFix", cause_by="engineer")

        print(f"\n[QA] Re-revisando (intento {intento})...")
        test_report_raw = llamar_llm(
            system=ROLES["qa"].system_prompt,
            user=(
                f"PRD (requisitos):\n{prd_msg.content}\n\n"
                f"System Design:\n{arch_msg.content}\n\n"
                f"Código corregido:\n{fix}"
            ),
        )
        pool.publish(test_report_raw, type="TestReport", cause_by="qa")
        reporte = parsear_test_report(test_report_raw)
        print(f"  Veredicto intento {intento}: {reporte['veredicto']}")

    # Devolver la versión más reciente del código (Code o BugFix)
    codigo_final = pool.latest("BugFix") or pool.latest("Code")
    estado = "VALIDADO" if not reporte.get("tiene_bugs") else f"MEJOR_INTENTO (bugs restantes)"
    print(f"\n[Pipeline] Estado final: {estado}")
    print(f"\n--- Pool de mensajes ---\n{pool.summary()}")

    return codigo_final.content if codigo_final else "[Sin código generado]"


if __name__ == "__main__":
    requisito = (
        "Implementa una función Python que calcule el número de Fibonacci de forma eficiente "
        "usando memoización. Debe manejar n=0, n=1, y números grandes. "
        "Incluye una función main() con ejemplos de uso."
    )
    print(f"Requisito: {requisito}\n{'=' * 60}\n")
    resultado = equipo_roles(requisito)
    print(f"\n{'=' * 60}\nCódigo final:\n{resultado}")
