# Fase 3: añade memoria episódica SQLite a la Fase 2.
# Antes de revisar, consulta si ya existe una revisión cacheada del mismo código.
#
# Cómo ejecutar:
#   make py SCRIPT=python/16-proyecto/fase3_memoria.py
#
# Qué esperar:
#   El agente consulta memoria SQLite antes de revisar — si el mismo codigo
#   fue revisado antes, reutiliza el resultado (cache semantico).
#
# Variables de entorno:
#   MODEL — modelo a usar (default: claude-sonnet-4-6)

import sqlite3
import hashlib
import json
import os

from fase2_herramientas import agente_revision

DB_PATH = "revisiones.db"


def inicializar_db(db_path: str = DB_PATH) -> sqlite3.Connection:
    conn = sqlite3.connect(db_path)
    conn.execute("""
        CREATE TABLE IF NOT EXISTS revisiones (
            hash_archivo TEXT PRIMARY KEY,
            ruta TEXT,
            fecha TEXT,
            hallazgos_json TEXT,
            resumen TEXT
        )
    """)
    conn.commit()
    return conn


def buscar_revision_previa(conn: sqlite3.Connection, codigo: str) -> dict | None:
    hash_codigo = hashlib.sha256(codigo.encode()).hexdigest()
    fila = conn.execute(
        "SELECT hallazgos_json, resumen, fecha FROM revisiones WHERE hash_archivo = ?",
        (hash_codigo,)
    ).fetchone()
    if fila:
        return {
            "hallazgos": json.loads(fila[0]),
            "resumen": fila[1],
            "fecha": fila[2],
            "cached": True
        }
    return None


def guardar_revision(conn: sqlite3.Connection, codigo: str, ruta: str, revision: dict):
    hash_codigo = hashlib.sha256(codigo.encode()).hexdigest()
    conn.execute(
        """INSERT OR REPLACE INTO revisiones
           (hash_archivo, ruta, fecha, hallazgos_json, resumen)
           VALUES (?, ?, datetime('now'), ?, ?)""",
        (hash_codigo, ruta, json.dumps(revision["hallazgos"]), revision["resumen"])
    )
    conn.commit()


def agente_revision_con_memoria(codigo: str, ruta: str, proyecto_dir: str,
                                 db_path: str = DB_PATH) -> dict:
    conn = inicializar_db(db_path)

    revision_previa = buscar_revision_previa(conn, codigo)
    if revision_previa:
        revision_previa["nota"] = f"Revisión previa del {revision_previa['fecha']}"
        return revision_previa

    revision = agente_revision(codigo, proyecto_dir)
    guardar_revision(conn, codigo, ruta, revision)
    return revision


if __name__ == "__main__":
    import sys
    codigo = open(sys.argv[1]).read() if len(sys.argv) > 1 else """
def procesar(items):
    return [item.value for item in items]  # AttributeError si item no tiene .value
"""
    ruta = sys.argv[1] if len(sys.argv) > 1 else "test.py"
    proyecto = sys.argv[2] if len(sys.argv) > 2 else os.getcwd()

    resultado = agente_revision_con_memoria(codigo, ruta, proyecto)
    cached = resultado.get("cached", False)
    print(f"[{'CACHED' if cached else 'NUEVO'}]")
    print(json.dumps(resultado, indent=2, ensure_ascii=False))
