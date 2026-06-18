# costos — Control de costos: presupuesto por tarea, routing de modelos, alertas de gasto.
#
# Cómo ejecutar:
#     make py SCRIPT=python/17-produccion/costos.py
#
# Qué esperar:
#     Simula tareas de distintos tipos, routea cada una al modelo económico correcto,
#     y muestra el gasto acumulado con alertas si se supera el presupuesto.
#
# Variables de entorno:
#     MODEL — modelo a usar (default: claude-sonnet-4-6)
from dataclasses import dataclass, field
import anthropic

cliente = anthropic.Anthropic()

# Precios en USD por millón de tokens (mayo 2025)
PRECIOS = {
    "claude-haiku-4-5-20251001":   {"input": 0.80,  "output": 4.00},
    "claude-sonnet-4-6-20250219":  {"input": 3.00,  "output": 15.00},
    "claude-opus-4-7-20250219":    {"input": 15.00, "output": 75.00},
}

# Routing: qué modelo usar para cada tipo de subtarea
MODELO_POR_TAREA = {
    "clasificar":        "claude-haiku-4-5-20251001",
    "extraer_campo":     "claude-haiku-4-5-20251001",
    "verificar_bool":    "claude-haiku-4-5-20251001",
    "resumir_breve":     "claude-haiku-4-5-20251001",
    "revisar_codigo":    "claude-sonnet-4-6-20250219",
    "analizar_doc":      "claude-sonnet-4-6-20250219",
    "generar_codigo":    "claude-sonnet-4-6-20250219",
    "arquitectura":      "claude-opus-4-7-20250219",
    "analisis_profundo": "claude-opus-4-7-20250219",
}


def seleccionar_modelo(tipo_tarea: str) -> str:
    return MODELO_POR_TAREA.get(tipo_tarea, "claude-sonnet-4-6-20250219")


def coste_llamada(modelo: str, tokens_input: int, tokens_output: int) -> float:
    precios = PRECIOS.get(modelo, {"input": 3.00, "output": 15.00})
    return (tokens_input * precios["input"] + tokens_output * precios["output"]) / 1_000_000


@dataclass
class PresupuestoTarea:
    max_pasos: int = 15
    max_tokens_input: int = 50_000
    max_tokens_output: int = 10_000
    max_coste_usd: float = 0.50

    tokens_input: int = field(default=0, init=False)
    tokens_output: int = field(default=0, init=False)
    pasos: int = field(default=0, init=False)
    coste: float = field(default=0.0, init=False)

    def registrar(self, modelo: str, tokens_input: int, tokens_output: int) -> None:
        self.tokens_input += tokens_input
        self.tokens_output += tokens_output
        self.pasos += 1
        self.coste += coste_llamada(modelo, tokens_input, tokens_output)

    def verificar(self) -> tuple[bool, str]:
        if self.pasos >= self.max_pasos:
            return False, f"pasos={self.pasos} >= max={self.max_pasos}"
        if self.tokens_input >= self.max_tokens_input:
            return False, f"tokens_input={self.tokens_input} >= max={self.max_tokens_input}"
        if self.coste >= self.max_coste_usd:
            return False, f"coste=${self.coste:.4f} >= max=${self.max_coste_usd}"
        return True, ""

    def resumen(self) -> dict:
        return {
            "pasos": self.pasos,
            "tokens_input": self.tokens_input,
            "tokens_output": self.tokens_output,
            "coste_usd": round(self.coste, 6),
        }


def loop_con_presupuesto(pregunta: str, tipo_tarea: str = "analizar_doc") -> dict:
    presupuesto = PresupuestoTarea()
    modelo = seleccionar_modelo(tipo_tarea)
    mensajes = [{"role": "user", "content": pregunta}]

    while True:
        ok, motivo = presupuesto.verificar()
        if not ok:
            print(f"[WARN] Presupuesto agotado: {motivo}")
            return {"error": motivo, "parcial": True, "uso": presupuesto.resumen()}

        respuesta = cliente.messages.create(
            model=modelo,
            max_tokens=512,
            messages=mensajes,
        )

        presupuesto.registrar(
            modelo,
            respuesta.usage.input_tokens,
            respuesta.usage.output_tokens,
        )

        if respuesta.stop_reason == "end_turn":
            print(f"[INFO] Tarea completada: {presupuesto.resumen()}")
            return {
                "resultado": respuesta.content[0].text,
                "uso": presupuesto.resumen(),
            }

        mensajes.append({"role": "assistant", "content": respuesta.content})


def demostrar_routing() -> None:
    """Muestra el coste estimado de cada tipo de tarea con el modelo correcto."""
    tareas = [
        ("clasificar", "¿Este texto es spam? 'Gana dinero fácil'"),
        ("revisar_codigo", "def fib(n): return fib(n-1)+fib(n-2)"),
        ("analisis_profundo", "Propón una arquitectura de microservicios para pagos"),
    ]

    for tipo, contenido in tareas:
        modelo = seleccionar_modelo(tipo)
        coste_estimado = coste_llamada(modelo, 500, 200)
        print(f"[routing] tipo={tipo} → modelo={modelo.split('-')[1]} | "
              f"coste_estimado=${coste_estimado:.6f}")


if __name__ == "__main__":
    print("=== Routing de modelos ===")
    demostrar_routing()

    print("\n=== Loop con presupuesto ===")
    resultado = loop_con_presupuesto(
        "Analiza brevemente los tradeoffs de usar Redis vs SQLite para caché.",
        tipo_tarea="analizar_doc",
    )
    print(f"Resultado: {resultado.get('resultado', resultado.get('error', ''))[:200]}")
    print(f"Uso: {resultado['uso']}")
