# Pipeline completo del agente de revisión de código con tracing OpenTelemetry.
#
# Cómo ejecutar:
#   make py SCRIPT=python/16-proyecto/agente_completo.py
#
# Qué esperar:
#   Pipeline completo con tools, memoria, HITL y tracing OpenTelemetry.
#   El agente revisa el codigo de ejemplo del libro e imprime un informe.
#
# Variables de entorno:
#   MODEL — modelo a usar (default: claude-sonnet-4-6)

import anthropic
import json
import sqlite3
import hashlib
import subprocess
import tempfile
import os
import sys
from dataclasses import dataclass
try:
    from opentelemetry import trace
    from opentelemetry.sdk.trace import TracerProvider
    from opentelemetry.sdk.trace.export import BatchSpanProcessor
    from opentelemetry.exporter.otlp.proto.grpc.trace_exporter import OTLPSpanExporter
    _OTEL_OK = True
except (ImportError, Exception):
    _OTEL_OK = False
    class _DummySpan:
        def __enter__(self): return self
        def __exit__(self, *a): pass
        def set_attribute(self, *a): pass
    class _DummyTracer:
        def start_as_current_span(self, *a, **kw): return _DummySpan()
    class trace:
        @staticmethod
        def get_tracer(*a): return _DummyTracer()

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
        "description": "Ejecuta un fragmento de código Python en sandbox y devuelve stdout/stderr",
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
        "description": "Busca en la documentación técnica del equipo",
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
        "description": "Escribe el informe final de revisión en disco",
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


def configurar_tracing():
    if not _OTEL_OK:
        return trace.get_tracer("agente-revision")
    endpoint = os.getenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://localhost:4317")
    provider = TracerProvider()
    exporter = OTLPSpanExporter(endpoint=endpoint, insecure=True)
    provider.add_span_processor(BatchSpanProcessor(exporter))
    trace.set_tracer_provider(provider)
    return trace.get_tracer("agente-revision")


def ejecutar_herramienta(nombre: str, params: dict, proyecto_dir: str, tracer) -> str:
    with tracer.start_as_current_span(f"tool.{nombre}") as span:
        span.set_attribute("gen_ai.tool.name", nombre)

        if nombre == "read_file":
            ruta = params["path"]
            ruta_abs = os.path.realpath(os.path.join(proyecto_dir, ruta))
            if not ruta_abs.startswith(os.path.realpath(proyecto_dir)):
                span.set_attribute("tool.error", "path_traversal")
                return "Error: ruta fuera del directorio del proyecto"
            try:
                contenido = open(ruta_abs).read()
                span.set_attribute("tool.result_length", len(contenido))
                return contenido
            except FileNotFoundError:
                return f"Error: archivo '{ruta}' no encontrado"

        elif nombre == "run_code":
            codigo = params["code"]
            timeout = params.get("timeout", 10)
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
                    span.set_attribute("tool.exit_code", resultado.returncode)
                    salida = {"stdout": resultado.stdout or "(vacío)", "stderr": resultado.stderr or "(vacío)"}
                    return json.dumps(salida)
                except subprocess.TimeoutExpired:
                    span.set_attribute("tool.error", "timeout")
                    return "Error: timeout de ejecución"

        elif nombre == "search_docs":
            query = params["query"]
            span.set_attribute("tool.query", query)
            return f"[Documentación para '{query}': ver /docs/ del proyecto]"

        elif nombre == "write_report":
            reports_dir = os.path.join(proyecto_dir, "reports")
            os.makedirs(reports_dir, exist_ok=True)
            ruta = os.path.join(reports_dir, params["filename"])
            open(ruta, "w").write(params["content"])
            span.set_attribute("tool.report_path", ruta)
            return f"Informe escrito en {ruta}"

        return f"Error: herramienta '{nombre}' desconocida"


def extraer_json(texto: str) -> dict:
    decoder = json.JSONDecoder()
    for i, c in enumerate(texto):
        if c == "{":
            try:
                obj, _ = decoder.raw_decode(texto, i)
                if isinstance(obj, dict) and "hallazgos" in obj:
                    return obj
            except json.JSONDecodeError:
                pass
    # fallback: el primer objeto JSON válido
    for i, c in enumerate(texto):
        if c == "{":
            try:
                obj, _ = decoder.raw_decode(texto, i)
                if isinstance(obj, dict):
                    return obj
            except json.JSONDecodeError:
                pass
    raise ValueError(f"No se encontró JSON en output: {texto[:300]}")


def loop_react(codigo: str, proyecto_dir: str, tracer) -> dict:
    cliente = anthropic.Anthropic()
    mensajes = [
        {"role": "user", "content": f"Revisa este código:\n\n```python\n{codigo}\n```"}
    ]
    MAX_PASOS = 15
    tokens_totales = {"input": 0, "output": 0}

    for paso in range(MAX_PASOS):
        with tracer.start_as_current_span(f"llm.paso_{paso}") as span:
            respuesta = cliente.messages.create(
                model=os.environ.get("MODEL", "claude-sonnet-4-6"),
                max_tokens=4096,
                system=SYSTEM_PROMPT,
                tools=HERRAMIENTAS,
                messages=mensajes
            )
            span.set_attribute("gen_ai.usage.input_tokens", respuesta.usage.input_tokens)
            span.set_attribute("gen_ai.usage.output_tokens", respuesta.usage.output_tokens)
            span.set_attribute("gen_ai.response.finish_reason", respuesta.stop_reason)
            tokens_totales["input"] += respuesta.usage.input_tokens
            tokens_totales["output"] += respuesta.usage.output_tokens

        if respuesta.stop_reason == "end_turn":
            texto = next(b.text for b in respuesta.content if hasattr(b, "text"))
            revision = extraer_json(texto)
            revision["_meta"] = {"pasos": paso + 1, "tokens": tokens_totales}
            return revision

        if respuesta.stop_reason == "tool_use":
            mensajes.append({"role": "assistant", "content": respuesta.content})
            resultados = []
            for bloque in respuesta.content:
                if bloque.type == "tool_use":
                    resultado = ejecutar_herramienta(bloque.name, bloque.input, proyecto_dir, tracer)
                    resultados.append({
                        "type": "tool_result",
                        "tool_use_id": bloque.id,
                        "content": str(resultado)
                    })
            mensajes.append({"role": "user", "content": resultados})

    raise RuntimeError(f"El agente no terminó en {MAX_PASOS} pasos")


def memoria_episodica(db_path: str):
    conn = sqlite3.connect(db_path)
    conn.execute("""
        CREATE TABLE IF NOT EXISTS revisiones (
            hash_codigo TEXT PRIMARY KEY,
            ruta TEXT,
            fecha TEXT,
            revision_json TEXT
        )
    """)
    conn.commit()
    return conn


def hitl_checkpoint(revision: dict) -> dict:
    criticos = [h for h in revision.get("hallazgos", []) if h.get("severidad") == "critical"]
    if not criticos:
        return revision

    modo = os.getenv("HITL_MODE", "interactive")
    if modo == "auto-approve":
        return revision
    if modo == "auto-reject":
        raise RuntimeError("HITL: hallazgo crítico rechazado automáticamente")

    print(f"\n=== CHECKPOINT: {len(criticos)} hallazgo(s) crítico(s) ===")
    for i, h in enumerate(criticos, 1):
        print(f"{i}. Línea {h.get('linea', '?')}: {h['descripcion']}")

    opción = input("\n[a]probar / [d]escartar críticos: ").strip().lower()
    if opción == "d":
        justificacion = input("Justificación: ")
        revision["hallazgos"] = [h for h in revision.get("hallazgos", []) if h.get("severidad") != "critical"]
        revision["hitl_descarte"] = justificacion

    return revision


def revisar(ruta_archivo: str, proyecto_dir: str) -> dict:
    tracer = configurar_tracing()

    with tracer.start_as_current_span("revision.sesion") as span:
        span.set_attribute("revision.ruta_archivo", ruta_archivo)

        codigo = open(os.path.join(proyecto_dir, ruta_archivo)).read()
        hash_codigo = hashlib.sha256((ruta_archivo + "::" + codigo).encode()).hexdigest()

        db = memoria_episodica(os.path.join(proyecto_dir, "revisiones.db"))
        fila = db.execute(
            "SELECT revision_json, fecha FROM revisiones WHERE hash_codigo = ?",
            (hash_codigo,)
        ).fetchone()

        if fila:
            revision = json.loads(fila[0])
            revision["_cached"] = True
            revision["_cached_fecha"] = fila[1]
            print(f"[INFO] Revisión cacheada del {fila[1]}")
            return revision

        revision = loop_react(codigo, proyecto_dir, tracer)
        revision = hitl_checkpoint(revision)

        db.execute(
            "INSERT OR REPLACE INTO revisiones (hash_codigo, ruta, fecha, revision_json) VALUES (?, ?, datetime('now'), ?)",
            (hash_codigo, ruta_archivo, json.dumps(revision))
        )
        db.commit()

        span.set_attribute("revision.hallazgos_total", len(revision.get("hallazgos", [])))
        span.set_attribute("revision.hallazgos_criticos",
                           len([h for h in revision.get("hallazgos", []) if h.get("severidad") == "critical"]))

        return revision


if __name__ == "__main__":
    if len(sys.argv) < 2:
        print("Uso: python agente_completo.py <ruta_archivo_relativa> [directorio_proyecto]")
        sys.exit(1)

    ruta = sys.argv[1]
    directorio = sys.argv[2] if len(sys.argv) > 2 else os.getcwd()

    resultado = revisar(ruta, directorio)
    print(json.dumps(resultado, indent=2, ensure_ascii=False))
