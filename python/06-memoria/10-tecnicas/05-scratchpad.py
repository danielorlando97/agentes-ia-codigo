"""Scratchpad: memoria legible por humanos, editable, versionada en archivo de texto.

Tres modos:
  1. Instrucciones fijas (CLAUDE.md): solo lectura para el agente, escritura por el equipo.
  2. Notas persistentes del agente: el agente escribe, el usuario puede editar.
  3. Razonamiento intermedio: scratchpad efímero dentro del turno, no persiste.

Cómo ejecutar:
    make py SCRIPT=python/06-memoria/10-tecnicas/05-scratchpad.py

Qué esperar:
    Crea un scratchpad en /tmp/agente-scratchpad.md, escribe entradas tipadas,
    lee el contexto al inicio de sesión, y muestra el archivo resultante.
"""

import os
import time
from dataclasses import dataclass
from datetime import datetime
from pathlib import Path
from typing import Optional


RUTA_DEFAULT = Path("/tmp/agente-scratchpad.md")

ESTRUCTURA_INICIAL = """\
# Notas del agente — proyecto

## Convenciones del proyecto
<!-- El agente añade aquí lo que aprende sobre el proyecto -->

## Decisiones de arquitectura
<!-- Formato: fecha: decisión + razonamiento -->

## Deuda técnica conocida
<!-- Formato: archivo:línea descripción del problema -->

## Notas de sesiones recientes
<!-- Generadas automáticamente por el agente -->
"""


@dataclass
class Scratchpad:
    ruta: Path = RUTA_DEFAULT

    def inicializar(self) -> None:
        if not self.ruta.exists():
            self.ruta.write_text(ESTRUCTURA_INICIAL, encoding="utf-8")

    def leer_contexto(self) -> Optional[str]:
        if not self.ruta.exists():
            return None
        contenido = self.ruta.read_text(encoding="utf-8").strip()
        if not contenido:
            return None
        return contenido

    def build_system_prompt(self, base: str) -> str:
        contexto = self.leer_contexto()
        if not contexto:
            return base
        return base + f"\n\n## Notas de sesiones anteriores\n{contexto}"

    def escribir_nota(self, seccion: str, contenido: str) -> None:
        texto_actual = self.ruta.read_text(encoding="utf-8") if self.ruta.exists() else ESTRUCTURA_INICIAL
        marca_seccion = f"## {seccion}"
        if marca_seccion not in texto_actual:
            texto_actual += f"\n{marca_seccion}\n"

        entrada = f"- {contenido}"
        lineas = texto_actual.split("\n")
        nueva_lineas = []
        dentro_seccion = False
        insertado = False

        for linea in lineas:
            if linea.strip() == marca_seccion:
                dentro_seccion = True
            elif linea.startswith("## ") and dentro_seccion and not insertado:
                nueva_lineas.append(entrada)
                nueva_lineas.append("")
                insertado = True
                dentro_seccion = False
            nueva_lineas.append(linea)

        if not insertado:
            nueva_lineas.append(entrada)

        self.ruta.write_text("\n".join(nueva_lineas), encoding="utf-8")

    def escribir_nota_sesion(self, texto: str) -> None:
        ts = datetime.now().strftime("%Y-%m-%d %H:%M")
        nota = f"\n### {ts}\n{texto}\n"
        with open(self.ruta, "a", encoding="utf-8") as f:
            f.write(nota)

    def tamaño_tokens(self) -> int:
        if not self.ruta.exists():
            return 0
        return len(self.ruta.read_text(encoding="utf-8")) // 4


if __name__ == "__main__":
    ruta_demo = Path("/tmp/agente-scratchpad-demo.md")
    if ruta_demo.exists():
        ruta_demo.unlink()

    sp = Scratchpad(ruta=ruta_demo)
    sp.inicializar()

    print("=== Inicio de sesión ===")
    contexto = sp.leer_contexto()
    system = sp.build_system_prompt("Eres un asistente de desarrollo.")
    print(f"System prompt incluye {sp.tamaño_tokens()} tokens de contexto del scratchpad")

    print("\n=== El agente aprende cosas durante la sesión ===")
    sp.escribir_nota(
        "Convenciones del proyecto",
        "Usar snake_case para variables, PascalCase para clases"
    )
    sp.escribir_nota(
        "Decisiones de arquitectura",
        f"{datetime.now().strftime('%Y-%m-%d')}: Elegimos SQLite sobre PostgreSQL por simplicidad inicial"
    )
    sp.escribir_nota(
        "Deuda técnica conocida",
        "src/auth/login.py:247 — condición de guarda incorrecta para sesiones admin"
    )
    sp.escribir_nota_sesion(
        "Investigué el bug de auth. El problema está en la verificación de expiración "
        "cuando user.role == 'admin'. Pendiente: escribir test de regresión."
    )

    print("Notas guardadas en el scratchpad.\n")

    print("=== Contenido del scratchpad ===")
    print(ruta_demo.read_text(encoding="utf-8"))

    print(f"=== Tamaño final: {sp.tamaño_tokens()} tokens ===")

    ruta_demo.unlink()
