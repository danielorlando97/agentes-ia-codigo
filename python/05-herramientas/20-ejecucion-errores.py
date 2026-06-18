# Ejecución y manejo de errores en tool calling.
#
# El 20-40% de tool calls en producción encuentran algún tipo de error.
# Este ejecutor distingue entre errores transitorios (retry con backoff)
# y errores determinísticos (fail fast), y devuelve errores formativos
# al modelo para que pueda autocorregir su llamada.
#
# El agent loop tiene cinco stop_reason posibles, no dos:
# end_turn, tool_use, max_tokens, pause_turn, refusal.
#
# Cómo ejecutar:
#   make py SCRIPT=python/05-herramientas/20-ejecucion-errores.py
#
# Qué esperar:
#   Demos de errores transitorios (retry) y errores determinísticos (fail fast).
#   El modelo recibe los errores como strings y puede autocorregir sus llamadas.
#
# Variables de entorno:
#   MODEL — modelo a usar (default: claude-sonnet-4-6)

import os
import json
import random
import time
import asyncio
import anthropic

MODEL = os.environ.get("MODEL", "claude-sonnet-4-6")
MAX_ITERATIONS = 20


# --- Tipos de error ---

class ToolNotFoundError(Exception):
    def __init__(self, tool_name: str):
        super().__init__(f"Herramienta '{tool_name}' no registrada")
        self.tool_name = tool_name


class ToolTimeoutError(Exception):
    def __init__(self, tool_name: str, timeout_ms: int):
        super().__init__(f"{tool_name} no completó en {timeout_ms}ms")
        self.tool_name = tool_name
        self.timeout_ms = timeout_ms


class AuthError(Exception):
    def __init__(self, resource: str):
        super().__init__(f"Sin permisos para acceder a {resource}")
        self.resource = resource


class RateLimitError(Exception):
    def __init__(self, retry_after_ms: int):
        super().__init__(f"Rate limit excedido. Reintenta en {retry_after_ms}ms")
        self.retry_after_ms = retry_after_ms


# --- Herramientas mock con distintos comportamientos de error ---

async def tool_fetch_data(args: dict) -> str:
    source = args.get("source", "")
    if source == "restricted":
        raise AuthError(source)
    if source == "slow":
        await asyncio.sleep(5)  # simulará timeout desde el ejecutor
        return "datos muy tardíos"
    return json.dumps({"data": f"datos de {source}", "rows": 42})


async def tool_calculate(args: dict) -> str:
    expression = args.get("expression", "")
    # Solo evaluar expresiones numéricas básicas
    import re
    sanitized = re.sub(r"[^0-9+\-*/().\s]", "", expression)
    result = eval(sanitized)  # noqa: S307 — sanitized input
    return str(result)


async def tool_save_file(args: dict) -> str:
    path = args.get("path")
    content = args.get("content")
    if not path or not content:
        raise ValueError("path y content son requeridos")
    return f"Archivo guardado: {path} ({len(content)} bytes)"


TOOL_REGISTRY = {
    "fetch_data": tool_fetch_data,
    "calculate": tool_calculate,
    "save_file": tool_save_file,
}

TOOLS = [
    {
        "name": "fetch_data",
        "description": "Obtiene datos de una fuente. source puede ser: 'database', 'api', 'cache', 'restricted' (sin permisos), 'slow' (timeout).",
        "input_schema": {
            "type": "object",
            "properties": {
                "source": {"type": "string", "description": "Nombre de la fuente de datos"},
            },
            "required": ["source"],
        },
    },
    {
        "name": "calculate",
        "description": "Evalúa una expresión matemática.",
        "input_schema": {
            "type": "object",
            "properties": {
                "expression": {"type": "string"},
            },
            "required": ["expression"],
        },
    },
    {
        "name": "save_file",
        "description": "Guarda contenido en un archivo.",
        "input_schema": {
            "type": "object",
            "properties": {
                "path": {"type": "string"},
                "content": {"type": "string"},
            },
            "required": ["path", "content"],
        },
    },
]


# --- Retry con backoff exponencial ---

async def con_backoff(fn, max_retries: int, base_delay_ms: int = 100):
    for intento in range(max_retries):
        try:
            return await fn()
        except (AuthError, ToolNotFoundError):
            raise  # errores determinísticos: no reintentar
        except Exception as e:
            if intento == max_retries - 1:
                raise
            delay = base_delay_ms * (2 ** intento)
            jitter = delay * 0.1 * (random.random() * 2 - 1)
            wait = round(delay + jitter)
            print(f"    [backoff] intento {intento + 1}/{max_retries} falló, esperando {wait}ms")
            await asyncio.sleep(wait / 1000)


# --- Ejecutar con timeout ---

async def con_timeout(coro, timeout_ms: int, tool_name: str):
    try:
        return await asyncio.wait_for(coro, timeout=timeout_ms / 1000)
    except asyncio.TimeoutError:
        raise ToolTimeoutError(tool_name, timeout_ms)


# --- Construir mensaje de error formativo ---

def construir_error_formativo(tool_name: str, error: Exception, input_args: dict) -> str:
    if isinstance(error, ToolNotFoundError):
        disponibles = ", ".join(TOOL_REGISTRY.keys())
        return (
            f"Herramienta '{tool_name}' no existe. "
            f"Herramientas disponibles: {disponibles}."
        )
    if isinstance(error, ToolTimeoutError):
        return (
            f"{tool_name} no completó en {error.timeout_ms}ms "
            f"con input {json.dumps(input_args)}. "
            f"Intenta con un scope más pequeño o una fuente diferente."
        )
    if isinstance(error, AuthError):
        return (
            f"Sin permisos para acceder a '{error.resource}'. "
            f"No reintentes — usa una fuente diferente."
        )
    if isinstance(error, RateLimitError):
        return f"Rate limit excedido. Reintenta en {error.retry_after_ms}ms."
    return f"{type(error).__name__} en {tool_name}: {error}"


# --- Dispatcher: ejecutar una tool con manejo completo de errores ---

async def despachar_tool(tool_name: str, input_args: dict) -> dict:
    fn = TOOL_REGISTRY.get(tool_name)
    is_error = False

    if fn is None:
        content = construir_error_formativo(tool_name, ToolNotFoundError(tool_name), input_args)
        is_error = True
    else:
        try:
            content = await con_timeout(
                con_backoff(lambda: fn(input_args), max_retries=2),
                timeout_ms=500,
                tool_name=tool_name,
            )
        except Exception as e:
            content = construir_error_formativo(tool_name, e, input_args)
            is_error = True

    result = {"type": "tool_result", "tool_use_id": "", "content": content}
    if is_error:
        result["is_error"] = True
    return result


# --- Agent loop con manejo completo de stop_reason ---

async def agent_loop(tarea: str) -> str:
    client = anthropic.Anthropic()
    messages = [{"role": "user", "content": tarea}]

    for iter_num in range(MAX_ITERATIONS):
        response = client.messages.create(
            model=MODEL,
            max_tokens=4096,
            tools=TOOLS,
            messages=messages,
        )

        print(f"\n[iter={iter_num + 1}] stop_reason={response.stop_reason}")

        if response.stop_reason == "end_turn":
            return "".join(
                block.text for block in response.content if block.type == "text"
            )

        if response.stop_reason == "tool_use":
            tool_blocks = [b for b in response.content if b.type == "tool_use"]
            tool_results = []

            for block in tool_blocks:
                print(f"  → {block.name}({json.dumps(block.input)})")
                result = await despachar_tool(block.name, block.input)
                result["tool_use_id"] = block.id
                is_err = result.get("is_error", False)
                print(f"  ← [{'ERROR' if is_err else 'OK'}] {str(result['content'])[:100]}")
                tool_results.append(result)

            messages.append({"role": "assistant", "content": response.content})
            messages.append({"role": "user", "content": tool_results})
            continue

        if response.stop_reason == "max_tokens":
            last = response.content[-1] if response.content else None
            if last and last.type == "tool_use":
                print("  [warn] tool_use block truncado por max_tokens — necesita más tokens")
            return "[respuesta truncada — max_tokens alcanzado]"

        if response.stop_reason == "pause_turn":
            # El servidor excedió su límite de iteraciones internas.
            print("  [pause_turn] continuando...")
            messages.append({"role": "assistant", "content": response.content})
            continue

        print(f"  [warn] stop_reason desconocido: {response.stop_reason}")
        return "[stop_reason inesperado]"

    return "[max iteraciones alcanzadas]"


async def main():
    print("=== Ejecución y manejo de errores en tool calling ===\n")

    tarea = (
        "Necesito: 1) obtener datos de 'database', "
        "2) intentar obtener datos de 'restricted' (esto fallará), "
        "3) calcular 15 * 8 + 3, "
        "4) guardar el resultado en /tmp/resultado.txt. "
        "Si algo falla, descríbelo en tu respuesta final."
    )

    print(f"Tarea: {tarea}")

    resultado = await agent_loop(tarea)
    print(f"\n=== Respuesta final ===\n{resultado}")


if __name__ == "__main__":
    asyncio.run(main())
