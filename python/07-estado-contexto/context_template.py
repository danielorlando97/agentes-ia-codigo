"""Ensamblador de contexto con slots tipados y presupuesto explícito por región.

ContextTemplate define los slots y sus presupuestos.
ContextAssembler ensambla validando que cada slot no exceda su budget.
ContextValidator verifica el contexto ensamblado.
SlotBudgetError se lanza si un slot tiene más contenido del que su budget permite (modo strict).
progressive_tool_loading: selecciona tools en orden de prioridad hasta agotar el budget.

Cómo ejecutar:
    make py SCRIPT=python/07-estado-contexto/context_template.py

Qué esperar:
    Demo del ensamblador con 5 regiones de presupuesto. Muestra el error
    SlotBudgetError cuando un slot excede su budget en modo strict.

Variables de entorno:
    MODEL — modelo a usar (default: claude-sonnet-4-6)
"""
import json
from dataclasses import dataclass, field
from enum import Enum
from typing import Optional


class TrustLevel(Enum):
    HIGH   = "high"    # system prompt, constraints — instrucciones del desarrollador
    MEDIUM = "medium"  # retrieved context — puede contener contenido de terceros
    LOW    = "low"     # tool results, historial — puede contener contenido no controlado


class SlotBudgetError(Exception):
    """El contenido de un slot excede su presupuesto de tokens."""
    def __init__(self, slot: str, actual: int, budget: int):
        self.slot   = slot
        self.actual = actual
        self.budget = budget
        super().__init__(f"Slot '{slot}': {actual}t > budget {budget}t")


@dataclass
class SlotDef:
    name:     str
    budget:   int
    trust:    TrustLevel = TrustLevel.HIGH
    required: bool = True


def _tokens_text(text: str) -> int:
    return len(text.encode("utf-8")) // 4


def _tokens_list(obj: list) -> int:
    return len(json.dumps(obj, ensure_ascii=False)) // 4


@dataclass
class ContextTemplate:
    """Define los slots del contexto y sus presupuestos de tokens."""
    total: int = 128_000
    slots: list[SlotDef] = field(default_factory=lambda: [
        SlotDef("system",      4_000, TrustLevel.HIGH,   required=True),
        SlotDef("constraints", 2_000, TrustLevel.HIGH,   required=False),
        SlotDef("retrieved",   3_000, TrustLevel.MEDIUM, required=False),
        SlotDef("tools",       2_000, TrustLevel.HIGH,   required=False),
        SlotDef("response",    8_000, TrustLevel.HIGH,   required=False),
        # "response" es reserva de presupuesto, no contenido ensamblado
        # "history" ocupa el resto: total - suma de los anteriores
    ])

    def get_slot(self, name: str) -> Optional[SlotDef]:
        return next((s for s in self.slots if s.name == name), None)

    @property
    def history_budget(self) -> int:
        return self.total - sum(s.budget for s in self.slots)


class ContextValidator:
    def __init__(self, template: ContextTemplate):
        self.template = template

    def validate(self, assembled: dict) -> list[str]:
        """Devuelve lista de errores. Vacía = válido.

        Solo valida slots presentes en `assembled` — el slot "response" es
        una reserva de presupuesto, no contenido que se ensambla.
        """
        errors = []
        for slot in self.template.slots:
            content = assembled.get(slot.name)
            if slot.name not in assembled:
                if slot.required:
                    errors.append(f"Slot requerido '{slot.name}' está vacío")
                continue
            if content and isinstance(content, str):
                actual = _tokens_text(content)
                if actual > slot.budget:
                    errors.append(f"Slot '{slot.name}': {actual}t > budget {slot.budget}t")
        return errors


class ContextAssembler:
    def __init__(self, template: Optional[ContextTemplate] = None):
        self.template  = template or ContextTemplate()
        self.validator = ContextValidator(self.template)

    def _clip(self, text: str, budget: int) -> str:
        max_chars = budget * 4
        return text[:max_chars] if len(text) > max_chars else text

    def _wrap_untrusted(self, content: str, label: str) -> str:
        """Encapsula contenido de baja confianza para prevenir prompt injection."""
        return f"[{label.upper()}]\n{content}\n[/{label.upper()}]"

    def assemble(
        self,
        system:      str,
        history:     list[dict],
        retrieved:   str = "",
        constraints: str = "",
        tools:       list | None = None,
        strict:      bool = True,
    ) -> dict:
        """Ensambla el contexto aplicando presupuestos por slot.

        strict=True: lanza SlotBudgetError si un slot excede su presupuesto.
        strict=False: hace clip silencioso.
        """
        tools  = tools or []
        result: dict = {}

        sys_slot = self.template.get_slot("system")
        sys_tokens = _tokens_text(system)
        if strict and sys_tokens > sys_slot.budget:
            raise SlotBudgetError("system", sys_tokens, sys_slot.budget)
        result["system"] = self._clip(system, sys_slot.budget)

        if constraints:
            c_slot = self.template.get_slot("constraints")
            result["constraints"] = self._clip(constraints, c_slot.budget)

        if retrieved:
            r_slot = self.template.get_slot("retrieved")
            # Contenido no confiable: encapsular con marcadores
            wrapped = self._wrap_untrusted(retrieved, "retrieved")
            result["retrieved"] = self._clip(wrapped, r_slot.budget)

        result["tools"]    = tools
        result["messages"] = history
        return result

    def validate(self, assembled: dict) -> list[str]:
        return self.validator.validate(assembled)


def progressive_tool_loading(
    all_tools:      list[dict],
    budget:         int,
    priority_field: str = "priority",
) -> list[dict]:
    """Selecciona tools en orden de prioridad hasta agotar el budget de tokens.

    Las tools con menor número de `priority` se incluyen primero.
    Tools sin campo priority reciben prioridad 99 (últimas).
    """
    sorted_tools = sorted(all_tools, key=lambda t: t.get(priority_field, 99))
    selected: list[dict] = []
    used = 0
    for tool in sorted_tools:
        cost = _tokens_list([tool])
        if used + cost > budget:
            break
        selected.append(tool)
        used += cost
    return selected


if __name__ == "__main__":
    # El total debe ser mayor que la suma de los slots fijos (19k) + historial
    template = ContextTemplate(total=32_000)
    assembler = ContextAssembler(template)

    tools = [
        {"name": "read_file",  "description": "Lee un archivo", "priority": 1},
        {"name": "write_file", "description": "Escribe un archivo", "priority": 2},
        {"name": "run_tests",  "description": "Ejecuta los tests", "priority": 3},
        {"name": "deploy",     "description": "Despliega el servicio", "priority": 4},
    ]

    selected_tools = progressive_tool_loading(tools, budget=500)
    print(f"Tools seleccionadas con budget=500t: {[t['name'] for t in selected_tools]}")

    ctx = assembler.assemble(
        system="Eres un asistente de código experto en Python.",
        history=[{"role": "user", "content": "Analiza este repositorio."}],
        retrieved="Sesión anterior: el usuario trabaja en un proyecto de facturación.",
        constraints="No modificar archivos de test. Penalización máxima 15%.",
        tools=selected_tools,
        strict=False,
    )

    errors = assembler.validate(ctx)
    print(f"Errores de validación: {errors or 'ninguno'}")
    print(f"Slots ensamblados: {list(ctx.keys())}")
    print(f"Budget de historial disponible: {template.history_budget}t")

    # Demostrar SlotBudgetError en modo strict
    try:
        assembler.assemble(
            system="X" * 20_000,  # supera el budget de system (4_000t)
            history=[],
            strict=True,
        )
    except SlotBudgetError as e:
        print(f"\nSlotBudgetError capturado: {e}")
