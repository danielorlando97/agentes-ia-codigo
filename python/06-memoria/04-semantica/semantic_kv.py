"""Memoria semántica KV plana sin versionado — para prototipado.

Variante mínima de AlmacenSemantico: última escritura gana, sin tombstones,
sin detección de conflictos, sin historial de versiones.
Útil cuando el caso de uso no requiere auditoría ni resolución de contradicciones.

Cómo ejecutar:
    make py SCRIPT=python/06-memoria/04-semantica/semantic_kv.py

Qué esperar:
    Demo de almacén KV plano: store, retrieve, list. Última escritura gana.
    Sin versionado ni detección de conflictos — versión mínima para prototipado.

Variables de entorno:
    MODEL — modelo a usar (default: claude-sonnet-4-6)
"""
import time
from dataclasses import dataclass, field


@dataclass
class HechoKV:
    clave: str
    valor: str
    fuente: str = "auto"
    timestamp: float = field(default_factory=time.time)


class MemoriaSemanticaKV:
    def __init__(self):
        self._hechos: dict[str, HechoKV] = {}

    def set_fact(self, clave: str, valor: str, fuente: str = "auto") -> HechoKV:
        """Guarda o sobreescribe un hecho. Última escritura gana."""
        hecho = HechoKV(clave=clave, valor=valor, fuente=fuente)
        self._hechos[clave] = hecho
        return hecho

    def get_fact(self, clave: str) -> str | None:
        h = self._hechos.get(clave)
        return h.valor if h else None

    def delete_fact(self, clave: str) -> bool:
        return self._hechos.pop(clave, None) is not None

    def get_all(self) -> list[HechoKV]:
        return list(self._hechos.values())

    def build_context_block(self, max_facts: int = 20) -> str:
        """Bloque de texto para inyectar en el system prompt."""
        hechos = sorted(self._hechos.values(), key=lambda h: h.timestamp, reverse=True)
        lineas = [f"- {h.clave}: {h.valor}" for h in hechos[:max_facts]]
        return "## Perfil del usuario\n" + "\n".join(lineas) if lineas else ""

    def __len__(self) -> int:
        return len(self._hechos)


if __name__ == "__main__":
    mem = MemoriaSemanticaKV()

    mem.set_fact("lenguaje_preferido", "Python", fuente="usuario_directo")
    mem.set_fact("timezone", "Europe/Madrid", fuente="usuario_directo")
    mem.set_fact("estilo_respuesta", "conciso", fuente="auto_extract")
    mem.set_fact("proyecto_actual", "backend de facturación", fuente="auto_extract")

    # Corrección directa — sin conflicto explícito, última escritura gana
    mem.set_fact("lenguaje_preferido", "TypeScript", fuente="usuario_directo")

    print(f"Total hechos: {len(mem)}")
    print(f"lenguaje_preferido: {mem.get_fact('lenguaje_preferido')}")  # TypeScript
    print(f"timezone: {mem.get_fact('timezone')}")
    print(f"no_existe: {mem.get_fact('no_existe')}")

    print(f"\n{mem.build_context_block()}")
