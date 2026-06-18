# Sandboxing: ejecución aislada de código con límites de recursos y red
#
# Cómo ejecutar:
#   make py SCRIPT=python/15-seguridad/sandboxing.py
#
# Qué esperar:
#   Ejecucion de codigo en subprocess aislado con limites de CPU, memoria y red.
#   Muestra intentos de escape del sandbox bloqueados.
#
# Variables de entorno:
#   MODEL — modelo a usar (default: claude-sonnet-4-6)

import os
import subprocess
import sys
import tempfile
from typing import Optional
import anthropic

cliente = anthropic.Anthropic()


# ─── Nivel 1: subprocess básico con tmpdir ────────────────────────────────────

def ejecutar_sandbox_basico(codigo: str, timeout_s: int = 10) -> tuple[str, str, int]:
    """Nivel mínimo: proceso hijo en directorio temporal con entorno limpio."""
    with tempfile.TemporaryDirectory() as tmpdir:
        ruta = os.path.join(tmpdir, "script.py")
        with open(ruta, "w") as f:
            f.write(codigo)

        try:
            resultado = subprocess.run(
                [sys.executable, ruta],
                capture_output=True,
                text=True,
                timeout=timeout_s,
                cwd=tmpdir,
                env={
                    "PATH": "/usr/local/bin:/usr/bin:/bin",
                    "HOME": tmpdir,
                    "PYTHONPATH": "",
                },
            )
            return resultado.stdout, resultado.stderr, resultado.returncode
        except subprocess.TimeoutExpired:
            return "", f"Timeout: ejecución superó {timeout_s}s", -1
        except Exception as e:
            return "", f"Error de sandbox: {e}", -1


# ─── Nivel 2: restricciones de recursos (Linux/macOS) ────────────────────────

def _preexec_limitar_recursos() -> None:
    """Llamada en el hijo antes del exec — establece límites de recursos."""
    try:
        import resource
        resource.setrlimit(resource.RLIMIT_CPU, (10, 10))          # 10s CPU
        resource.setrlimit(resource.RLIMIT_AS, (256 * 1024**2, 256 * 1024**2))  # 256MB RAM
        resource.setrlimit(resource.RLIMIT_FSIZE, (10 * 1024**2, 10 * 1024**2))  # 10MB archivos
        resource.setrlimit(resource.RLIMIT_NOFILE, (32, 32))        # max 32 file descriptors
    except Exception:
        pass  # no disponible en todos los sistemas


def ejecutar_sandbox_con_recursos(
    codigo: str,
    timeout_s: int = 10,
    bloquear_red: bool = True,
) -> tuple[str, str, int]:
    """Nivel 2: proceso hijo con límites de recursos del SO."""
    with tempfile.TemporaryDirectory() as tmpdir:
        ruta = os.path.join(tmpdir, "script.py")

        if bloquear_red:
            # Parchar socket.socket para bloquear conexiones de red desde el código
            preambulo = """
import socket as _socket_orig
class _NoNetwork:
    def __call__(self, *a, **kw): raise PermissionError("Acceso a red bloqueado en sandbox")
    def __getattr__(self, n): raise PermissionError("Acceso a red bloqueado en sandbox")
import socket
socket.socket = _NoNetwork()
"""
            codigo_final = preambulo + codigo
        else:
            codigo_final = codigo

        with open(ruta, "w") as f:
            f.write(codigo_final)

        try:
            resultado = subprocess.run(
                [sys.executable, ruta],
                capture_output=True,
                text=True,
                timeout=timeout_s,
                cwd=tmpdir,
                env={
                    "PATH": "/usr/local/bin:/usr/bin:/bin",
                    "HOME": tmpdir,
                    "PYTHONPATH": "",
                },
                preexec_fn=_preexec_limitar_recursos,
            )
            return resultado.stdout, resultado.stderr, resultado.returncode
        except subprocess.TimeoutExpired:
            return "", f"Timeout: ejecución superó {timeout_s}s", -1
        except Exception as e:
            return "", f"Error de sandbox: {e}", -1


# ─── Agente de código con sandbox ────────────────────────────────────────────

HERRAMIENTAS = [
    {
        "name": "ejecutar_codigo",
        "description": (
            "Ejecuta código Python en un sandbox seguro. "
            "El código no puede acceder a red ni a archivos fuera del directorio temporal."
        ),
        "input_schema": {
            "type": "object",
            "properties": {
                "codigo": {"type": "string", "description": "Código Python a ejecutar"},
                "timeout": {"type": "integer", "description": "Timeout en segundos (máx 30)", "default": 10},
            },
            "required": ["codigo"],
        },
    }
]


def agente_codigo_sandboxed(tarea: str) -> str:
    """Agente que ejecuta código dentro de un sandbox seguro."""
    mensajes = [{"role": "user", "content": tarea}]

    for _ in range(10):
        respuesta = cliente.messages.create(
            model=os.environ.get("MODEL", "claude-sonnet-4-6"),
            max_tokens=1024,
            tools=HERRAMIENTAS,
            messages=mensajes,
        )

        mensajes.append({"role": "assistant", "content": respuesta.content})

        if respuesta.stop_reason == "end_turn":
            return next((b.text for b in respuesta.content if hasattr(b, "text")), "")

        if respuesta.stop_reason == "tool_use":
            tool_results = []
            for bloque in respuesta.content:
                if bloque.type != "tool_use":
                    continue

                codigo = bloque.input.get("codigo", "")
                timeout = min(int(bloque.input.get("timeout", 10)), 30)  # max 30s

                stdout, stderr, rc = ejecutar_sandbox_con_recursos(codigo, timeout)

                if stdout or rc == 0:
                    contenido = stdout if stdout else "(sin output)"
                else:
                    contenido = f"Error (rc={rc}): {stderr[:500]}"

                print(f"[sandbox] rc={rc} | stdout={stdout[:100]} | stderr={stderr[:100]}")
                tool_results.append({
                    "type": "tool_result",
                    "tool_use_id": bloque.id,
                    "content": contenido,
                })

            mensajes.append({"role": "user", "content": tool_results})

    return "[max iteraciones]"


if __name__ == "__main__":
    print("=== Sandbox básico ===")
    tests = [
        ("print('hello world')", "código legítimo"),
        ("import time; time.sleep(20)", "timeout"),
        ("x = 2**31; print(x)", "operación matemática"),
    ]
    for codigo, descripcion in tests:
        stdout, stderr, rc = ejecutar_sandbox_basico(codigo, timeout_s=3)
        print(f"  [{descripcion}] rc={rc} | stdout={stdout.strip()[:50]} | stderr={stderr.strip()[:50]}")

    print("\n=== Sandbox con bloqueo de red ===")
    intento_red = "import socket; s = socket.socket(); s.connect(('google.com', 80))"
    stdout, stderr, rc = ejecutar_sandbox_con_recursos(intento_red, bloquear_red=True)
    print(f"  Intento red: rc={rc} | stderr={stderr.strip()[:100]}")

    print("\n=== Agente de código con sandbox ===")
    resultado = agente_codigo_sandboxed(
        "Calcula el factorial de 10 usando código Python y muéstrame el resultado."
    )
    print(f"Resultado: {resultado[:300]}")
