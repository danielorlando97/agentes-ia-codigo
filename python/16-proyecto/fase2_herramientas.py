# Fase 2: añade las 4 herramientas al loop ReAct de la Fase 1.
# El modelo decide cuándo usarlas; el código ejecuta y devuelve resultados.
#
# Cómo ejecutar:
#   make py SCRIPT=python/16-proyecto/fase2_herramientas.py
#
# Qué esperar:
#   Agente de revision de codigo con 4 herramientas: run_tests, check_style,
#   analyze_complexity, search_docs. Muestra el ciclo completo de una revision.
#
# Variables de entorno:
#   MODEL — modelo a usar (default: claude-sonnet-4-6)

import anthropic
import subprocess
import tempfile
import json
import os

SYSTEM_PROMPT = """Eres un agente de revisión de código Python.
Analiza el código con cuidado, usa las herramientas disponibles para verificar comportamiento
cuando sea útil, y produce una revisión técnica estructurada.

Cuando tengas suficiente información, emite el resultado final como JSON con este schema exacto:
{
  "hallazgos": [
    {
      "linea": <número o null>,
      "severidad": "<critical|high|medium|low>",
      "tipo": "<bug|estilo|rendimiento|seguridad>",
      "descripcion": "<descripción concisa del hallazgo>",
      "sugerencia": "<cómo corregirlo>"
    }
  ],
  "resumen": "<párrafo de resumen>"
}"""

HERRAMIENTAS = [
    {
        "name": "read_file",
        "description": "Lee el contenido de un archivo del proyecto",
        "input_schema": {
            "type": "object",
            "properties": {
                "path": {"type": "string", "description": "Ruta relativa al directorio del proyecto"}
            },
            "required": ["path"]
        }
    },
    {
        "name": "run_code",
        "description": "Ejecuta un fragmento de código Python y devuelve stdout/stderr",
        "input_schema": {
            "type": "object",
            "properties": {
                "code": {"type": "string"},
                "timeout": {"type": "integer", "default": 10}
            },
            "required": ["code"]
        }
    },
    {
        "name": "search_docs",
        "description": "Busca en la documentación técnica interna del equipo",
        "input_schema": {
            "type": "object",
            "properties": {
                "query": {"type": "string"}
            },
            "required": ["query"]
        }
    },
    {
        "name": "write_report",
        "description": "Escribe el informe final de revisión",
        "input_schema": {
            "type": "object",
            "properties": {
                "content": {"type": "string"},
                "filename": {"type": "string"}
            },
            "required": ["content", "filename"]
        }
    }
]


def ejecutar_herramienta(nombre: str, params: dict, proyecto_dir: str) -> str:
    if nombre == "read_file":
        ruta = params["path"]
        ruta_absoluta = os.path.realpath(os.path.join(proyecto_dir, ruta))
        if not ruta_absoluta.startswith(os.path.realpath(proyecto_dir)):
            return "Error: ruta fuera del directorio del proyecto"
        try:
            return open(ruta_absoluta).read()
        except FileNotFoundError:
            return f"Error: archivo '{ruta}' no encontrado"

    elif nombre == "run_code":
        codigo = params["code"]
        timeout = float(params.get("timeout", 10))
        with tempfile.TemporaryDirectory() as tmpdir:
            script = os.path.join(tmpdir, "script.py")
            open(script, "w").write(codigo)
            try:
                resultado = subprocess.run(
                    ["python3", script],
                    capture_output=True, text=True,
                    timeout=timeout, cwd=tmpdir,
                    env={"PATH": "/usr/local/bin:/usr/bin:/bin", "HOME": tmpdir}
                )
                output = resultado.stdout
                if resultado.stderr:
                    output += f"\nSTDERR: {resultado.stderr}"
                return output or "(sin output)"
            except subprocess.TimeoutExpired:
                return "Error: timeout de ejecución"

    elif nombre == "search_docs":
        query = params["query"]
        return f"[Documentación para '{query}': ver estándares del equipo en /docs/]"

    elif nombre == "write_report":
        reports_dir = os.path.join(proyecto_dir, "reports")
        os.makedirs(reports_dir, exist_ok=True)
        ruta_report = os.path.join(reports_dir, params["filename"])
        open(ruta_report, "w").write(params["content"])
        return f"Informe escrito en {ruta_report}"

    return f"Error: herramienta '{nombre}' desconocida"


def agente_revision(codigo: str, proyecto_dir: str) -> dict:
    cliente = anthropic.Anthropic()
    mensajes = [
        {"role": "user", "content": f"Revisa este código:\n\n```python\n{codigo}\n```"}
    ]

    while True:
        respuesta = cliente.messages.create(
            model=os.environ.get("MODEL", "claude-sonnet-4-6"),
            max_tokens=4096,
            system=SYSTEM_PROMPT,
            tools=HERRAMIENTAS,
            messages=mensajes
        )

        if respuesta.stop_reason == "end_turn":
            texto = next((b.text for b in respuesta.content if hasattr(b, "text") and b.text), "")
            # Intentar extraer el primer objeto JSON del texto (con o sin markdown)
            _decoder = json.JSONDecoder()
            _parsed = None
            for _i, _c in enumerate(texto):
                if _c == "{":
                    try:
                        _parsed, _ = _decoder.raw_decode(texto, _i)
                        break
                    except json.JSONDecodeError:
                        pass
            if _parsed is not None:
                return _parsed
            # Modelo no devolvió JSON — pedir al modelo que lo haga explícito
            mensajes.append({"role": "assistant", "content": texto or "[sin respuesta]"})
            mensajes.append({"role": "user", "content": "Devuelve SOLO el JSON del resultado, sin texto adicional."})
            continue

        if respuesta.stop_reason == "tool_use":
            mensajes.append({"role": "assistant", "content": respuesta.content})
            resultados = []

            for bloque in respuesta.content:
                if bloque.type == "tool_use":
                    resultado = ejecutar_herramienta(bloque.name, bloque.input, proyecto_dir)
                    resultados.append({
                        "type": "tool_result",
                        "tool_use_id": bloque.id,
                        "content": str(resultado)
                    })

            mensajes.append({"role": "user", "content": resultados})


if __name__ == "__main__":
    import sys
    codigo = open(sys.argv[1]).read() if len(sys.argv) > 1 else """
def divide(a, b):
    return a / b  # ZeroDivisionError no manejado
"""
    proyecto = sys.argv[2] if len(sys.argv) > 2 else os.getcwd()
    resultado = agente_revision(codigo, proyecto)
    print(json.dumps(resultado, indent=2, ensure_ascii=False))
