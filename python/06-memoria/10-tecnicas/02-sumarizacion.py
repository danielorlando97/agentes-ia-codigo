"""Sumarización lazy del historial conversacional.

Comprime el intermedio del historial cuando supera el umbral de tokens.
Preserva siempre: cabeza (primeros 2 mensajes) + cola (últimos 6 turnos).
El modelo secundario mock demuestra el mecanismo sin requerir API key.

Cómo ejecutar:
    make py SCRIPT=python/06-memoria/10-tecnicas/02-sumarizacion.py

Qué esperar:
    Historial de 30 mensajes simulados. Muestra cuándo activa la compresión,
    qué mensajes se preservan y la reducción de tokens resultante.
"""

import json
from dataclasses import dataclass

UMBRAL_TOKENS = 2_000
TOKENS_RESUMEN = 400
TURNOS_PRESERVAR = 6
CABEZA_PRESERVAR = 2


def estimar_tokens(mensajes: list[dict]) -> int:
    return sum(len(json.dumps(m, ensure_ascii=False)) // 4 for m in mensajes)


def _resumir_mock(mensajes: list[dict]) -> str:
    """Simulación del modelo secundario. En producción: llamada a LLM barato."""
    herramientas = [m for m in mensajes if m.get("type") == "tool_use"]
    errores = [m for m in mensajes if "error" in str(m.get("content", "")).lower()]
    resumen = f"[{len(mensajes)} turnos comprimidos] "
    if herramientas:
        nombres = {h.get("name", "?") for h in herramientas}
        resumen += f"Herramientas usadas: {', '.join(nombres)}. "
    if errores:
        resumen += f"{len(errores)} errores encontrados. "
    resumen += "El agente continuó investigando y encontró información relevante."
    return resumen


@dataclass
class SumarizadorLazy:
    umbral: int = UMBRAL_TOKENS
    max_tokens_resumen: int = TOKENS_RESUMEN
    turnos_preservar: int = TURNOS_PRESERVAR
    cabeza_preservar: int = CABEZA_PRESERVAR

    def build_context(self, mensajes: list[dict]) -> list[dict]:
        tokens_actuales = estimar_tokens(mensajes)
        if tokens_actuales <= self.umbral:
            return mensajes

        cabeza = mensajes[: self.cabeza_preservar]
        cola = mensajes[-self.turnos_preservar :]
        middle = mensajes[self.cabeza_preservar : -self.turnos_preservar]

        if not middle:
            return mensajes

        middle_limpio = self._sanitizar_pares(middle)
        resumen_texto = _resumir_mock(middle_limpio)

        bloque_resumen = {
            "role": "user",
            "content": (
                f"[HISTORIAL COMPRIMIDO — {len(middle_limpio)} turnos]\n"
                f"{resumen_texto}\n"
                "[FIN COMPRIMIDO]"
            ),
        }

        return cabeza + [bloque_resumen] + cola

    def _sanitizar_pares(self, mensajes: list[dict]) -> list[dict]:
        """Descarta tool_use sin su tool_result correspondiente (evita historial malformado)."""
        result_ids = {
            m.get("tool_use_id") for m in mensajes if m.get("type") == "tool_result"
        }
        salida = []
        for m in mensajes:
            if m.get("type") == "tool_use" and m.get("id") not in result_ids:
                continue
            salida.append(m)
        return salida


def _simular_historial(n: int) -> list[dict]:
    mensajes = []
    for i in range(n):
        if i == 0:
            mensajes.append({"role": "user", "content": "Analiza el repositorio completo y encuentra el bug."})
        elif i % 4 == 1:
            uid = f"tool_{i}"
            mensajes.append({
                "role": "assistant",
                "type": "tool_use",
                "id": uid,
                "name": "read_file",
                "input": {"path": f"src/módulo_{i // 4}.py"},
            })
        elif i % 4 == 2:
            uid = f"tool_{i - 1}"
            mensajes.append({
                "role": "user",
                "type": "tool_result",
                "tool_use_id": uid,
                "content": f"Contenido del archivo {i // 4}: función principal con {i * 10} líneas. " + "código" * 15,
            })
        elif i % 4 == 3:
            mensajes.append({
                "role": "assistant",
                "content": f"Análisis parcial #{i // 4}: el módulo parece correcto. Continuando.",
            })
        else:
            mensajes.append({"role": "user", "content": f"¿Avanzaste? (turno {i})"})
    return mensajes


if __name__ == "__main__":
    sumarizador = SumarizadorLazy()
    historial = _simular_historial(30)

    tokens_original = estimar_tokens(historial)
    print(f"Historial original: {len(historial)} mensajes, ~{tokens_original} tokens")

    contexto = sumarizador.build_context(historial)
    tokens_comprimido = estimar_tokens(contexto)

    print(f"Contexto comprimido: {len(contexto)} mensajes, ~{tokens_comprimido} tokens")
    print(f"Reducción: {100 * (1 - tokens_comprimido / tokens_original):.0f}%")
    print()
    print("Mensajes resultantes:")
    for m in contexto:
        contenido = str(m.get("content", m.get("name", "")))[:80]
        tipo = m.get("type", m.get("role", "?"))
        print(f"  [{tipo}] {contenido}")
