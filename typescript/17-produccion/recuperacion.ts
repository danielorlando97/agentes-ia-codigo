// Recuperación ante fallos: retry con backoff, circuit breaker, context overflow, JSON inválido

// Cómo ejecutar: make ts SCRIPT=typescript/17-produccion/recuperacion.ts

import Anthropic from "@anthropic-ai/sdk";

const cliente = new Anthropic();

// ─── Retry con backoff exponencial ───────────────────────────────────────────

async function conRetry<T>(
  func: () => Promise<T>,
  maxIntentos: number = 3,
  backoffBase: number = 1.0
): Promise<T> {
  let ultimoError: unknown;
  for (let intento = 0; intento < maxIntentos; intento++) {
    try {
      return await func();
    } catch (e) {
      ultimoError = e;
      if (intento === maxIntentos - 1) break;
      const espera = backoffBase * Math.pow(2, intento) + Math.random() * 0.5;
      const nombre = e instanceof Error ? e.constructor.name : "Error";
      console.log(
        `[retry] Intento ${intento + 1} fallido (${nombre}). Reintentando en ${espera.toFixed(1)}s...`
      );
      await new Promise((r) => setTimeout(r, espera * 1000));
    }
  }
  throw new Error(`Agotados ${maxIntentos} intentos`, { cause: ultimoError });
}

// ─── Circuit breaker para herramientas externas ───────────────────────────────

class CircuitBreaker {
  nombre: string;
  umbralFallos: number;
  ventanaResetMin: number;
  private fallos: Date[] = [];
  private abierto: boolean = false;
  private abiertoDesde: Date | null = null;

  constructor(nombre: string, umbralFallos: number = 5, ventanaResetMin: number = 2) {
    this.nombre = nombre;
    this.umbralFallos = umbralFallos;
    this.ventanaResetMin = ventanaResetMin;
  }

  private limpiarFallosAntiguos(): void {
    const corte = new Date(Date.now() - this.ventanaResetMin * 60 * 1000);
    this.fallos = this.fallos.filter((t) => t > corte);
  }

  registrarExito(): void {
    this.fallos = [];
    this.abierto = false;
  }

  registrarFallo(): void {
    this.fallos.push(new Date());
    this.limpiarFallosAntiguos();
    if (this.fallos.length >= this.umbralFallos) {
      this.abierto = true;
      this.abiertoDesde = new Date();
      console.log(`[circuit] ${this.nombre}: circuito ABIERTO tras ${this.umbralFallos} fallos`);
    }
  }

  puedeIntentar(): boolean {
    if (!this.abierto) return true;
    const ahora = new Date();
    if (ahora.getTime() - this.abiertoDesde!.getTime() > this.ventanaResetMin * 60 * 1000) {
      this.abierto = false;
      this.fallos = [];
      console.log(`[circuit] ${this.nombre}: circuito CERRADO (reset automático)`);
      return true;
    }
    return false;
  }

  ejecutar<T>(func: () => T): T {
    if (!this.puedeIntentar()) {
      throw new Error(`Circuito abierto para ${this.nombre} — servicio no disponible`);
    }
    try {
      const resultado = func();
      this.registrarExito();
      return resultado;
    } catch (e) {
      this.registrarFallo();
      throw e;
    }
  }
}

const breakers = new Map<string, CircuitBreaker>();

function obtenerBreaker(nombre: string): CircuitBreaker {
  if (!breakers.has(nombre)) {
    breakers.set(nombre, new CircuitBreaker(nombre));
  }
  return breakers.get(nombre)!;
}

function herramientaStub(nombre: string, params: Record<string, unknown>): string {
  if (Math.random() < 0.4) {
    throw new Error(`Servicio ${nombre} no disponible`);
  }
  return `Resultado de ${nombre}(${JSON.stringify(params)})`;
}

function ejecutarHerramientaSegura(nombre: string, params: Record<string, unknown>): string {
  const breaker = obtenerBreaker(nombre);
  try {
    return breaker.ejecutar(() => herramientaStub(nombre, params));
  } catch (e) {
    return `Error: ${e instanceof Error ? e.message : e}`;
  }
}

// ─── Compresión de contexto ───────────────────────────────────────────────────

const VENTANA_TOKENS = 200_000;
const UMBRAL_COMPRESION = 0.75;

async function comprimirSiNecesario(
  mensajes: Anthropic.MessageParam[],
  tokensUsados: number
): Promise<Anthropic.MessageParam[]> {
  if (tokensUsados < VENTANA_TOKENS * UMBRAL_COMPRESION) return mensajes;

  console.log(
    `[context] Comprimiendo historial (${tokensUsados} tokens > ${Math.round(VENTANA_TOKENS * UMBRAL_COMPRESION)} umbral)`
  );

  const mensajesAntiguos = mensajes.slice(1, -4);
  const resumenResp = await cliente.messages.create({
    model: process.env["SMALL_MODEL"] ?? "claude-haiku-4-5-20251001",
    max_tokens: 512,
    messages: [
      {
        role: "user",
        content: `Resume este historial en 3-5 bullets:\n${JSON.stringify(mensajesAntiguos.slice(0, 5))}`,
      },
    ],
  });
  const resumen = (resumenResp.content[0] as Anthropic.TextBlock).text;

  return [
    mensajes[0],
    { role: "assistant", content: `[Resumen de pasos anteriores: ${resumen}]` },
    ...mensajes.slice(-4),
  ];
}

// ─── Retry para output JSON mal formado ──────────────────────────────────────

async function obtenerJsonValido(
  prompt: string,
  schemaDesc: string,
  maxIntentos: number = 3
): Promise<Record<string, unknown>> {
  const mensajes: Anthropic.MessageParam[] = [
    { role: "user", content: `${prompt}\n\nDevuelve JSON con: ${schemaDesc}` },
  ];

  for (let intento = 0; intento < maxIntentos; intento++) {
    const respuesta = await cliente.messages.create({
      model: process.env["MODEL"] ?? "claude-sonnet-4-6",
      max_tokens: 512,
      messages: mensajes,
    });
    const texto = (respuesta.content[0] as Anthropic.TextBlock).text;

    let textoLimpio = texto.trim();
    if (textoLimpio.includes("```")) {
      const inicio = textoLimpio.indexOf("{");
      const fin = textoLimpio.lastIndexOf("}") + 1;
      textoLimpio = inicio >= 0 ? textoLimpio.slice(inicio, fin) : textoLimpio;
    }

    try {
      return JSON.parse(textoLimpio);
    } catch (e) {
      if (intento === maxIntentos - 1) {
        throw new Error(`El modelo no produjo JSON válido en ${maxIntentos} intentos`);
      }
      mensajes.push(
        { role: "assistant", content: texto },
        {
          role: "user",
          content: `Tu respuesta no es JSON válido. Error: ${e}. Devuelve exactamente el JSON especificado, sin texto adicional.`,
        }
      );
      console.log(`[json_retry] Intento ${intento + 1} fallido — retrying con feedback`);
    }
  }

  throw new Error("Loop sin resultado");
}

async function main(): Promise<void> {
  console.log("=== Retry con backoff ===");
  let intentos = 0;
  const llamadaQueFallaDoVeces = async () => {
    intentos++;
    if (intentos < 3) throw new Error("ConnectionError simulado");
    return cliente.messages.create({
      model: process.env["SMALL_MODEL"] ?? "claude-haiku-4-5-20251001",
      max_tokens: 32,
      messages: [{ role: "user", content: "Di 'ok'" }],
    });
  };
  // await conRetry(llamadaQueFallaDoVeces);  // descomenta para probar retry real
  console.log("(retry demo comentado para no gastar tokens)");

  console.log("=== Circuit breaker ===");
  for (let i = 0; i < 8; i++) {
    const resultado = ejecutarHerramientaSegura("search_docs", { q: "test" });
    console.log(`  Intento ${i + 1}: ${resultado.slice(0, 60)}`);
  }

  console.log("\n=== JSON con retry ===");
  try {
    const datos = await obtenerJsonValido(
      "Describe en JSON un agente simple",
      '{"nombre": string, "herramientas": string[], "pasos_max": number}'
    );
    console.log(`JSON válido recibido: ${JSON.stringify(datos)}`);
  } catch (e) {
    console.log(`Error tras retries: ${e}`);
  }
}

main().catch(console.error);
