# Devolver el resultado al modelo — formatos de tool_result.
#
# Muestra el formato correcto de tool_result en cinco escenarios:
#   1. Texto simple
#   2. JSON estructurado
#   3. Imagen (content array con type: "image")
#   4. Error formativo (is_error: True)
#   5. Loop completo: request → tool_use → execute → tool_result → segunda response
#
# El contenido del campo 'content' cuando is_error=True determina
# si el modelo puede autocorregir — un error genérico produce retry
# idéntico; un error formativo produce recovery inteligente.
#
# Cómo ejecutar:
#   make py SCRIPT=python/05-herramientas/21-feedback-modelo.py
#
# Qué esperar:
#   5 formatos de tool_result: texto, JSON, imagen, error, y respuesta vacía.
#   Cada formato afecta cómo el modelo interpreta y procesa el resultado.
#
# Variables de entorno:
#   MODEL — modelo a usar (default: claude-sonnet-4-6)

import os
import json
import base64
import anthropic

MODEL = os.environ.get("MODEL", "claude-sonnet-4-6")

client = anthropic.Anthropic()


# --- 1. Tool result con texto simple ---

def tool_result_texto(tool_use_id: str) -> dict:
    return {
        "type": "tool_result",
        "tool_use_id": tool_use_id,
        "content": "La temperatura en Madrid es 24°C, condición: soleado.",
    }


# --- 2. Tool result con JSON estructurado ---

def tool_result_json(tool_use_id: str) -> dict:
    datos = {
        "city": "Madrid",
        "temperature": {"value": 24, "unit": "celsius"},
        "condition": "sunny",
        "humidity": 45,
        "wind": {"speed": 12, "direction": "NW"},
        "forecast": [
            {"day": "mañana", "high": 26, "low": 18},
            {"day": "pasado", "high": 23, "low": 16},
        ],
    }
    return {
        "type": "tool_result",
        "tool_use_id": tool_use_id,
        "content": json.dumps(datos),
    }


# --- 3. Tool result con imagen ---

def tool_result_imagen(tool_use_id: str) -> dict:
    # Imagen PNG 1x1 roja (base64) — en producción sería el PNG real del gráfico
    png_base64 = (
        "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAA"
        "DUlEQVR42mP8z8BQDwADhQGAWjR9awAAAABJRU5ErkJggg=="
    )
    return {
        "type": "tool_result",
        "tool_use_id": tool_use_id,
        "content": [
            {
                "type": "text",
                "text": "Gráfico de temperaturas de Madrid — últimas 24 horas:",
            },
            {
                "type": "image",
                "source": {
                    "type": "base64",
                    "media_type": "image/png",
                    "data": png_base64,
                },
            },
        ],
    }


# --- 4. Tool result con error formativo ---

def tool_result_error_formativo(tool_use_id: str, tipo: str) -> dict:
    mensajes = {
        "not_found": (
            "Archivo no encontrado: /tmp/report.md\n"
            "Archivos disponibles en /tmp/: budget.md, analysis.md, notes.txt\n"
            "Sugerencia: usa read_file con el path de uno de los archivos disponibles."
        ),
        "timeout": (
            "Timeout tras 10s buscando 'todos los documentos de 2024'.\n"
            "Intenta filtrar por rango de fecha más pequeño, e.g. '2024-Q1' o 'enero 2024'."
        ),
        "permission": (
            "Sin permisos para acceder a /etc/passwords.\n"
            "No reintentes — usa un directorio dentro de /home/usuario/."
        ),
        "generic": (
            "RateLimitError: demasiadas requests a la API externa.\n"
            "Reintenta después de 60 segundos."
        ),
    }
    return {
        "type": "tool_result",
        "tool_use_id": tool_use_id,
        "content": mensajes[tipo],
        "is_error": True,
    }


# --- Mostrar los formatos ---

def mostrar_formatos():
    print("=== Formatos de tool_result ===\n")

    fake_id = "toolu_fake_id_001"

    print("1. Texto simple:")
    print(json.dumps(tool_result_texto(fake_id), indent=2))

    print("\n2. JSON estructurado:")
    print(json.dumps(tool_result_json(fake_id), indent=2))

    print("\n3. Imagen (content array):")
    img = tool_result_imagen(fake_id)
    # Truncar base64 para la visualización
    img_display = {
        **img,
        "content": [
            {**c, "source": {**c["source"], "data": "[base64 truncado]"}}
            if c.get("type") == "image"
            else c
            for c in img["content"]
        ],
    }
    print(json.dumps(img_display, indent=2))

    print("\n4. Errores formativos:")
    for tipo in ["not_found", "timeout", "permission"]:
        print(f"\n  [{tipo}]")
        err = tool_result_error_formativo(fake_id, tipo)
        print(f"  is_error: {err['is_error']}")
        print(f"  content: {err['content']}")


# --- 5. Loop completo: el modelo solicita una tool, recibe error formativo, autocorrige ---

def loop_completo():
    print("\n\n=== Loop completo con autocorrección por error formativo ===\n")

    tools = [
        {
            "name": "read_file",
            "description": "Lee el contenido de un archivo.",
            "input_schema": {
                "type": "object",
                "properties": {
                    "path": {"type": "string", "description": "Path absoluto del archivo"},
                },
                "required": ["path"],
            },
        },
        {
            "name": "list_files",
            "description": "Lista los archivos en un directorio.",
            "input_schema": {
                "type": "object",
                "properties": {
                    "directory": {"type": "string"},
                },
                "required": ["directory"],
            },
        },
    ]

    # Mock: read_file falla en primer intento con error formativo
    read_file_intentos = {"count": 0}

    def ejecutar_tool(tool_use_id: str, name: str, input_args: dict) -> dict:
        if name == "read_file":
            read_file_intentos["count"] += 1
            if read_file_intentos["count"] == 1 and input_args.get("path") == "/tmp/report.md":
                return tool_result_error_formativo(tool_use_id, "not_found")
            return {
                "type": "tool_result",
                "tool_use_id": tool_use_id,
                "content": f"Contenido de {input_args['path']}:\n# Presupuesto 2024\nTotal: $1,234,567",
            }
        if name == "list_files":
            return {
                "type": "tool_result",
                "tool_use_id": tool_use_id,
                "content": json.dumps({
                    "directory": input_args.get("directory"),
                    "files": ["budget.md", "analysis.md", "notes.txt"],
                }),
            }
        return {
            "type": "tool_result",
            "tool_use_id": tool_use_id,
            "content": f"Herramienta '{name}' no existe",
            "is_error": True,
        }

    messages = [
        {
            "role": "user",
            "content": "Lee el archivo /tmp/report.md y dime el presupuesto total.",
        }
    ]

    for iter_num in range(5):
        response = client.messages.create(
            model=MODEL,
            max_tokens=1024,
            tools=tools,
            messages=messages,
        )

        print(f"[iter={iter_num + 1}] stop_reason={response.stop_reason}")

        if response.stop_reason == "end_turn":
            texto = "".join(b.text for b in response.content if b.type == "text")
            print(f"\nRespuesta final:\n{texto}")
            break

        if response.stop_reason == "tool_use":
            tool_blocks = [b for b in response.content if b.type == "tool_use"]
            tool_results = []

            for block in tool_blocks:
                print(f"  → {block.name}({json.dumps(block.input)})")
                result = ejecutar_tool(block.id, block.name, block.input)
                is_err = result.get("is_error", False)
                print(f"  ← [{'ERROR' if is_err else 'OK'}] {str(result['content'])[:100]}")
                tool_results.append(result)

            messages.append({"role": "assistant", "content": response.content})
            messages.append({"role": "user", "content": tool_results})


if __name__ == "__main__":
    mostrar_formatos()
    loop_completo()
