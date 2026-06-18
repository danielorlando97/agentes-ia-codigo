// Control de costos: presupuesto por tarea, routing de modelos, alertas de gasto

// Cómo ejecutar: make ts SCRIPT=typescript/17-produccion/costos.ts

import Anthropic from "@anthropic-ai/sdk";

const cliente = new Anthropic();

// Precios en USD por millón de tokens (mayo 2025)
const PRECIOS: Record<string, { input: number; output: number }> = {
  "claude-haiku-4-5-20251001":  { input: 0.80,  output: 4.00 },
  "claude-sonnet-4-6-20250219": { input: 3.00,  output: 15.00 },
  "claude-opus-4-7-20250219":   { input: 15.00, output: 75.00 },
};

const MODELO_POR_TAREA: Record<string, string> = {
  clasificar:        "claude-haiku-4-5-20251001",
  extraer_campo:     "claude-haiku-4-5-20251001",
  verificar_bool:    "claude-haiku-4-5-20251001",
  resumir_breve:     "claude-haiku-4-5-20251001",
  revisar_codigo:    "claude-sonnet-4-6-20250219",
  analizar_doc:      "claude-sonnet-4-6-20250219",
  generar_codigo:    "claude-sonnet-4-6-20250219",
  arquitectura:      "claude-opus-4-7-20250219",
  analisis_profundo: "claude-opus-4-7-20250219",
};

function seleccionarModelo(tipoTarea: string): string {
  return MODELO_POR_TAREA[tipoTarea] ?? "claude-sonnet-4-6-20250219";
}

function costeLlamada(modelo: string, tokensInput: number, tokensOutput: number): number {
  const precios = PRECIOS[modelo] ?? { input: 3.00, output: 15.00 };
  return (tokensInput * precios.input + tokensOutput * precios.output) / 1_000_000;
}

class PresupuestoTarea {
  maxPasos: number;
  maxTokensInput: number;
  maxTokensOutput: number;
  maxCosteUsd: number;
  tokensInput: number = 0;
  tokensOutput: number = 0;
  pasos: number = 0;
  coste: number = 0;

  constructor(
    maxPasos = 15,
    maxTokensInput = 50_000,
    maxTokensOutput = 10_000,
    maxCosteUsd = 0.5
  ) {
    this.maxPasos = maxPasos;
    this.maxTokensInput = maxTokensInput;
    this.maxTokensOutput = maxTokensOutput;
    this.maxCosteUsd = maxCosteUsd;
  }

  registrar(modelo: string, tokensInput: number, tokensOutput: number): void {
    this.tokensInput += tokensInput;
    this.tokensOutput += tokensOutput;
    this.pasos += 1;
    this.coste += costeLlamada(modelo, tokensInput, tokensOutput);
  }

  verificar(): [boolean, string] {
    if (this.pasos >= this.maxPasos) return [false, `pasos=${this.pasos} >= max=${this.maxPasos}`];
    if (this.tokensInput >= this.maxTokensInput) return [false, `tokens_input=${this.tokensInput} >= max=${this.maxTokensInput}`];
    if (this.coste >= this.maxCosteUsd) return [false, `coste=$${this.coste.toFixed(4)} >= max=$${this.maxCosteUsd}`];
    return [true, ""];
  }

  resumen(): Record<string, number> {
    return {
      pasos: this.pasos,
      tokens_input: this.tokensInput,
      tokens_output: this.tokensOutput,
      coste_usd: Math.round(this.coste * 1_000_000) / 1_000_000,
    };
  }
}

async function loopConPresupuesto(
  pregunta: string,
  tipoTarea: string = "analizar_doc"
): Promise<Record<string, unknown>> {
  const presupuesto = new PresupuestoTarea();
  const modelo = seleccionarModelo(tipoTarea);
  const mensajes: Anthropic.MessageParam[] = [{ role: "user", content: pregunta }];

  while (true) {
    const [ok, motivo] = presupuesto.verificar();
    if (!ok) {
      console.log(`[WARN] Presupuesto agotado: ${motivo}`);
      return { error: motivo, parcial: true, uso: presupuesto.resumen() };
    }

    const respuesta = await cliente.messages.create({
      model: modelo,
      max_tokens: 512,
      messages: mensajes,
    });

    presupuesto.registrar(modelo, respuesta.usage.input_tokens, respuesta.usage.output_tokens);

    if (respuesta.stop_reason === "end_turn") {
      console.log(`[INFO] Tarea completada: ${JSON.stringify(presupuesto.resumen())}`);
      return {
        resultado: (respuesta.content[0] as Anthropic.TextBlock).text,
        uso: presupuesto.resumen(),
      };
    }

    mensajes.push({ role: "assistant", content: respuesta.content });
  }
}

function demostrarRouting(): void {
  const tareas: Array<[string, string]> = [
    ["clasificar", "¿Este texto es spam? 'Gana dinero fácil'"],
    ["revisar_codigo", "def fib(n): return fib(n-1)+fib(n-2)"],
    ["analisis_profundo", "Propón una arquitectura de microservicios para pagos"],
  ];

  for (const [tipo, _contenido] of tareas) {
    const modelo = seleccionarModelo(tipo);
    const costeEstimado = costeLlamada(modelo, 500, 200);
    console.log(
      `[routing] tipo=${tipo} → modelo=${modelo.split("-")[1]} | coste_estimado=$${costeEstimado.toFixed(6)}`
    );
  }
}

async function main(): Promise<void> {
  console.log("=== Routing de modelos ===");
  demostrarRouting();

  console.log("\n=== Loop con presupuesto ===");
  const resultado = await loopConPresupuesto(
    "Analiza brevemente los tradeoffs de usar Redis vs SQLite para caché.",
    "analizar_doc"
  );
  const texto = (resultado.resultado as string | undefined) ?? (resultado.error as string) ?? "";
  console.log(`Resultado: ${texto.slice(0, 200)}`);
  console.log(`Uso: ${JSON.stringify(resultado.uso)}`);
}

main().catch(console.error);
