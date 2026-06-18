"""Memoria episódica sin decay — log plano para prototipado.

Variante mínima: solo append y búsqueda por texto o sesión.
Sin fuerza, sin half-life, sin scoring, sin consolidación.
Útil cuando el criterio de relevancia es recencia, no importancia.

Cómo ejecutar:
    make py SCRIPT=python/06-memoria/03-episodica/episodic_log.py

Qué esperar:
    Demo de append y búsqueda por texto en un log plano de episodios.
    Sin scoring de relevancia — útil para casos donde la recencia es suficiente.

Variables de entorno:
    MODEL — modelo a usar (default: claude-sonnet-4-6)
"""
import time
from dataclasses import dataclass, field


@dataclass
class EntradaLog:
    contenido: str
    sesion_id: str | None = None
    timestamp: float = field(default_factory=time.time)


class MemoriaLog:
    def __init__(self):
        self._log: list[EntradaLog] = []

    def append(self, contenido: str, sesion_id: str | None = None) -> EntradaLog:
        entrada = EntradaLog(contenido=contenido, sesion_id=sesion_id)
        self._log.append(entrada)
        return entrada

    def recall_recent(self, n: int = 5) -> list[EntradaLog]:
        """Últimas n entradas en orden cronológico."""
        return self._log[-n:]

    def recall_search(self, query: str, n: int = 5) -> list[EntradaLog]:
        """Búsqueda por substring case-insensitive. Sin scoring semántico."""
        q = query.lower()
        matches = [e for e in self._log if q in e.contenido.lower()]
        return matches[-n:]

    def recall_session(self, sesion_id: str) -> list[EntradaLog]:
        return [e for e in self._log if e.sesion_id == sesion_id]

    def __len__(self) -> int:
        return len(self._log)


if __name__ == "__main__":
    mem = MemoriaLog()

    mem.append("El usuario usa Python 3.12 en producción", sesion_id="s1")
    mem.append("Bug en auth.py línea 247: condición invertida", sesion_id="s1")
    mem.append("Decidimos usar PostgreSQL en lugar de SQLite", sesion_id="s2")
    mem.append("El módulo de billing tiene deuda técnica", sesion_id="s2")
    mem.append("Prefiere respuestas sin código cuando no es necesario", sesion_id="s3")

    print(f"Total: {len(mem)} episodios\n")

    print("--- últimos 3 ---")
    for e in mem.recall_recent(3):
        print(f"  [{e.sesion_id}] {e.contenido[:60]}")

    print("\n--- búsqueda: 'usuario' ---")
    for e in mem.recall_search("usuario"):
        print(f"  [{e.sesion_id}] {e.contenido[:60]}")

    print("\n--- sesión s1 ---")
    for e in mem.recall_session("s1"):
        print(f"  {e.contenido[:60]}")
