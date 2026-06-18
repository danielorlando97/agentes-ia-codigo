"""Ralph Loop — patrón de Claude Code (arXiv:2604.14228).

Sin límite explícito de iteraciones; la condición de salida es semántica
(el modelo responde sin tool_use). Características propias del patrón:
  - compactar_cascada: 4 capas de reducción de contexto ordenadas por coste
  - ejecutar_con_permisos: 5 niveles de autorización para tool use
  - diminishing_returns_check: detiene el loop si varias iteraciones no producen output útil

Requiere: pip install anthropic

Cómo ejecutar:
    make py SCRIPT=python/08-bucle/ralph_loop.py

Qué esperar:
    Loop sin límite de iteraciones con 5 niveles de permisos para tools.
    Cuando el contexto crece, activa compactacion en cascada (4 capas).
    Termina cuando el modelo responde sin tool_use (condicion semántica).

Variables de entorno:
    MODEL          — modelo principal (default: claude-sonnet-4-6)
    COMPACT_MODEL  — modelo de compactación (default: claude-haiku-4-5-20251001)
"""
import os
import json
from dataclasses import dataclass, field
from enum import IntEnum
from typing import Callable
import anthropic

MODEL = os.environ.get("MODEL", "claude-sonnet-4-6")
DIMINISHING_RETURNS_TOKENS = 100   # tokens mínimos por iter para ser "productiva"
DIMINISHING_RETURNS_ITERS  = 3     # iters consecutivas bajo el umbral → detener
COMPACTION_THRESHOLD       = 0.80
HISTORY_BUDGET             = 80_000


class PermissionLevel(IntEnum):
    READ_ONLY       = 1  # búsquedas, cálculos sin side effects
    WORKSPACE_READ  = 2  # leer archivos del workspace
    WORKSPACE_WRITE = 3  # leer + escribir archivos
    NETWORK_ACCESS  = 4  # llamadas a APIs externas
    DANGER_FULL     = 5  # operaciones destructivas


@dataclass
class ToolSpec:
    name:         str
    fn:           Callable
    permission:   PermissionLevel
    description:  str
    input_schema: dict


def _to_serializable(obj):
    """Convierte objetos Pydantic del SDK a dicts para json.dumps."""
    if hasattr(obj, "model_dump"):
        return obj.model_dump()
    if isinstance(obj, list):
        return [_to_serializable(i) for i in obj]
    if isinstance(obj, dict):
        return {k: _to_serializable(v) for k, v in obj.items()}
    return obj


def _estimate_tokens(messages: list[dict]) -> int:
    return sum(len(json.dumps(_to_serializable(m), ensure_ascii=False)) for m in messages) // 4


def compactar_cascada(messages: list[dict], client: anthropic.Anthropic) -> list[dict]:
    """4 capas de reducción ordenadas por coste.

    1. Limpiar tool_results antiguos (sin llamada al modelo)
    2. FIFO si aún excede
    3. Sumarización LLM del segmento intermedio
    4. Truncación agresiva head+tail como último recurso
    """
    # Capa 1: limpiar tool_results con más de 6 ciclos de antigüedad
    tr_count = 0
    result = []
    for msg in reversed(messages):
        content = msg.get("content")
        if isinstance(content, list):
            new_blocks = []
            for b in content:
                if isinstance(b, dict) and b.get("type") == "tool_result":
                    tr_count += 1
                    if tr_count > 6:
                        b = {**b, "content": [{"type": "text", "text": "[cleared]"}]}
                new_blocks.append(b)
            result.insert(0, {**msg, "content": new_blocks})
        else:
            result.insert(0, msg)
    messages = result

    if _estimate_tokens(messages) <= HISTORY_BUDGET:
        return messages

    # Capa 2: FIFO
    while _estimate_tokens(messages) > HISTORY_BUDGET and len(messages) > 2:
        messages = messages[1:]

    if _estimate_tokens(messages) <= HISTORY_BUDGET:
        return messages

    # Capa 3: sumarización LLM del segmento intermedio
    if len(messages) > 8:
        head, tail = messages[:2], messages[-4:]
        middle = messages[2:-4]
        resp = client.messages.create(
            model=os.environ.get("COMPACT_MODEL", "claude-haiku-4-5-20251001"),
            max_tokens=800,
            messages=[{
                "role": "user",
                "content": "Resume este historial preservando decisiones y resultados clave:\n"
                           + json.dumps(_to_serializable(middle), ensure_ascii=False)[:8000],
            }],
        )
        compressed = {"role": "user", "content": f"[HISTORIAL COMPRIMIDO]\n{resp.content[0].text}"}
        messages = head + [compressed] + tail

    # Capa 4: truncación agresiva head+tail si aún excede
    if _estimate_tokens(messages) > HISTORY_BUDGET:
        messages = messages[:2] + messages[-4:]

    return messages


def ejecutar_con_permisos(
    spec:          ToolSpec,
    args:          dict,
    level:         PermissionLevel,
) -> tuple[str, bool]:
    """Ejecuta la tool si el nivel actual lo permite. Devuelve (resultado, autorizado)."""
    if level < spec.permission:
        return (
            f"[Denegado: requiere {spec.permission.name}, actual {level.name}]",
            False,
        )
    try:
        return str(spec.fn(**args)), True
    except Exception as e:
        return f"[Error en {spec.name}]: {e}", False


@dataclass
class DiminishingReturnsChecker:
    min_tokens:      int = DIMINISHING_RETURNS_TOKENS
    max_consecutive: int = DIMINISHING_RETURNS_ITERS
    _below:          int = field(default=0, init=False)

    def check(self, tokens_output: int) -> bool:
        """Devuelve True si se debe detener el loop."""
        if tokens_output < self.min_tokens:
            self._below += 1
        else:
            self._below = 0
        return self._below >= self.max_consecutive


def ralph_loop(
    user_request:    str,
    tool_specs:      list[ToolSpec],
    client:          anthropic.Anthropic,
    system_prompt:   str             = "Eres un asistente útil.",
    permission_level: PermissionLevel = PermissionLevel.WORKSPACE_READ,
) -> str:
    """Loop principal. Sin límite de iteraciones — para cuando el modelo no llame tools."""
    messages: list[dict] = [{"role": "user", "content": user_request}]
    tools_api = [
        {"name": ts.name, "description": ts.description, "input_schema": ts.input_schema}
        for ts in tool_specs
    ]
    tool_map = {ts.name: ts for ts in tool_specs}
    dr = DiminishingReturnsChecker()
    iteration = 0

    while True:
        iteration += 1

        if _estimate_tokens(messages) > HISTORY_BUDGET * COMPACTION_THRESHOLD:
            messages = compactar_cascada(messages, client)
            print(f"  [compactado → ~{_estimate_tokens(messages)}t]")

        resp = client.messages.create(
            model=MODEL,
            max_tokens=2048,
            system=system_prompt,
            tools=tools_api,
            messages=messages,
        )

        tokens_out = resp.usage.output_tokens
        print(f"[iter {iteration}] stop={resp.stop_reason} | output={tokens_out}t")

        if resp.stop_reason == "end_turn":
            return "".join(b.text for b in resp.content if b.type == "text")

        if dr.check(tokens_out):
            print(f"  [ralph] {DIMINISHING_RETURNS_ITERS} iters no productivas → stop")
            return "".join(b.text for b in resp.content if b.type == "text") or "[loop detenido]"

        if resp.stop_reason == "tool_use":
            results = []
            for b in resp.content:
                if b.type == "tool_use":
                    spec = tool_map.get(b.name)
                    if spec:
                        r, ok = ejecutar_con_permisos(spec, b.input, permission_level)
                    else:
                        r, ok = f"[tool '{b.name}' no registrada]", False
                    print(f"  {b.name}({b.input}) → {r[:60]}")
                    results.append({"type": "tool_result", "tool_use_id": b.id, "content": r})
            messages.append({"role": "assistant", "content": resp.content})
            messages.append({"role": "user", "content": results})


if __name__ == "__main__":
    import datetime
    client = anthropic.Anthropic()

    tools = [
        ToolSpec(
            name="calcular",
            fn=lambda expresion: str(eval(expresion, {"__builtins__": {}}, {})),
            permission=PermissionLevel.READ_ONLY,
            description="Evalúa una expresión matemática.",
            input_schema={
                "type": "object",
                "properties": {"expresion": {"type": "string"}},
                "required": ["expresion"],
            },
        ),
        ToolSpec(
            name="obtener_fecha",
            fn=lambda: datetime.date.today().isoformat(),
            permission=PermissionLevel.READ_ONLY,
            description="Devuelve la fecha actual en formato ISO.",
            input_schema={"type": "object", "properties": {}},
        ),
    ]

    resultado = ralph_loop(
        user_request="¿Cuántos días han pasado desde el 1 de enero de 2025 hasta hoy?",
        tool_specs=tools,
        client=client,
        permission_level=PermissionLevel.READ_ONLY,
    )
    print(f"\nRespuesta: {resultado}")
