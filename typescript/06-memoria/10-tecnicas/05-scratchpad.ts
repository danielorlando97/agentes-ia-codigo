// Scratchpad: memoria legible por humanos, editable, persistida en archivo.
// El agente lee el archivo al inicio de sesión e inyecta el contenido en el system prompt.
// El agente puede escribir entradas tipadas durante la sesión.
//
// Cómo ejecutar: make ts SCRIPT=typescript/06-memoria/10-tecnicas/05-scratchpad.ts

import * as fs from "fs";
import * as path from "path";
import * as os from "os";

const ESTRUCTURA_INICIAL = `# Notas del agente — proyecto

## Convenciones del proyecto

## Decisiones de arquitectura

## Deuda técnica conocida

## Notas de sesiones recientes
`;

class Scratchpad {
  private readonly ruta: string;

  constructor(ruta = path.join(os.tmpdir(), "agente-scratchpad.md")) {
    this.ruta = ruta;
  }

  inicializar(): void {
    if (!fs.existsSync(this.ruta)) {
      fs.writeFileSync(this.ruta, ESTRUCTURA_INICIAL, "utf-8");
    }
  }

  leerContexto(): string | null {
    if (!fs.existsSync(this.ruta)) return null;
    const contenido = fs.readFileSync(this.ruta, "utf-8").trim();
    return contenido || null;
  }

  buildSystemPrompt(base: string): string {
    const ctx = this.leerContexto();
    return ctx ? `${base}\n\n## Notas de sesiones anteriores\n${ctx}` : base;
  }

  escribirNota(seccion: string, contenido: string): void {
    let texto = fs.existsSync(this.ruta)
      ? fs.readFileSync(this.ruta, "utf-8")
      : ESTRUCTURA_INICIAL;

    const marcaSeccion = `## ${seccion}`;
    if (!texto.includes(marcaSeccion)) {
      texto += `\n${marcaSeccion}\n`;
    }

    const lineas = texto.split("\n");
    const resultado: string[] = [];
    let dentroDSeccion = false;
    let insertado = false;

    for (const linea of lineas) {
      if (linea.trim() === marcaSeccion) {
        dentroDSeccion = true;
      } else if (linea.startsWith("## ") && dentroDSeccion && !insertado) {
        resultado.push(`- ${contenido}`);
        resultado.push("");
        insertado = true;
        dentroDSeccion = false;
      }
      resultado.push(linea);
    }

    if (!insertado) resultado.push(`- ${contenido}`);
    fs.writeFileSync(this.ruta, resultado.join("\n"), "utf-8");
  }

  escribirNotaSesion(texto: string): void {
    const ts = new Date().toISOString().slice(0, 16).replace("T", " ");
    fs.appendFileSync(this.ruta, `\n### ${ts}\n${texto}\n`, "utf-8");
  }

  tamanoTokens(): number {
    if (!fs.existsSync(this.ruta)) return 0;
    return Math.floor(fs.readFileSync(this.ruta, "utf-8").length / 4);
  }

  limpiar(): void {
    if (fs.existsSync(this.ruta)) fs.unlinkSync(this.ruta);
  }
}

// ── Demo ──────────────────────────────────────────────────────────────────

const rutaDemo = path.join(os.tmpdir(), "agente-scratchpad-demo.md");
const sp = new Scratchpad(rutaDemo);
sp.limpiar();
sp.inicializar();

console.log("=== Inicio de sesión ===");
const system = sp.buildSystemPrompt("Eres un asistente de desarrollo.");
console.log(`System prompt: ${system.length} chars, ~${sp.tamanoTokens()} tokens de contexto`);

console.log("\n=== El agente aprende durante la sesión ===");
sp.escribirNota("Convenciones del proyecto", "Usar snake_case para variables, PascalCase para clases");
sp.escribirNota(
  "Decisiones de arquitectura",
  `${new Date().toISOString().slice(0, 10)}: Elegimos SQLite sobre PostgreSQL para la fase inicial`
);
sp.escribirNota("Deuda técnica conocida", "src/auth/login.ts:247 — condición de guarda incorrecta");
sp.escribirNotaSesion("Bug de auth localizado. Pendiente: escribir test de regresión antes del merge.");

console.log("Notas guardadas.\n");
console.log("=== Contenido del scratchpad ===");
console.log(fs.readFileSync(rutaDemo, "utf-8"));
console.log(`=== Tamaño final: ~${sp.tamanoTokens()} tokens ===`);

sp.limpiar();
