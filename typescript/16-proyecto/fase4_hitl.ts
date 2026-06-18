// Fase 4: añade checkpoint HITL para hallazgos críticos.
// En producción: checkpoint → webhook/Slack; aquí: CLI interactiva.

// Cómo ejecutar: make ts SCRIPT=typescript/16-proyecto/fase4_hitl.ts

import * as crypto from "crypto";
import * as fs from "fs";
import * as readline from "readline";

import { agenteRevisionConMemoria } from "./fase3_memoria";
import { ejecutarHerramienta, Hallazgo, RevisionOutput } from "./fase2_herramientas";

interface RevisionConMeta extends RevisionOutput {
  cached?: boolean;
  hitl_descarte?: string;
}

interface ResultadoAprobacion {
  aprobado: boolean;
  revision: RevisionConMeta;
}

function necesitaAprobacion(revision: RevisionConMeta): boolean {
  return revision.hallazgos.some((h) => h.severidad === "critical");
}

async function pregunta(rl: readline.Interface, texto: string): Promise<string> {
  return new Promise((resolve) => rl.question(texto, resolve));
}

async function solicitarAprobacionCli(
  revision: RevisionConMeta
): Promise<ResultadoAprobacion> {
  const criticos = revision.hallazgos.filter((h) => h.severidad === "critical");

  console.log("\n=== REVISIÓN REQUIERE APROBACIÓN ===");
  console.log(`Se encontraron ${criticos.length} hallazgo(s) crítico(s):\n`);

  criticos.forEach((h, i) => {
    console.log(`${i + 1}. Línea ${h.linea ?? "?"}: ${h.descripcion}`);
    console.log(`   Sugerencia: ${h.sugerencia}\n`);
  });

  console.log("Opciones:");
  console.log("  [a] Aprobar y emitir informe completo");
  console.log("  [m] Modificar un hallazgo antes de emitir");
  console.log("  [d] Descartar hallazgos críticos con justificación");

  const rl = readline.createInterface({
    input: process.stdin,
    output: process.stdout,
  });

  try {
    const opcion = (await pregunta(rl, "\nElige [a/m/d]: ")).trim().toLowerCase();

    if (opcion === "a") {
      return { aprobado: true, revision };
    }

    if (opcion === "m") {
      const idxStr = await pregunta(
        rl,
        `Número de hallazgo a modificar (1-${criticos.length}): `
      );
      const idx = parseInt(idxStr, 10) - 1;
      const nuevaDesc = await pregunta(rl, "Nueva descripción: ");
      criticos[idx].descripcion = nuevaDesc;
      return { aprobado: true, revision };
    }

    if (opcion === "d") {
      const justificacion = await pregunta(rl, "Justificación para descartar: ");
      revision.hallazgos = revision.hallazgos.filter(
        (h) => h.severidad !== "critical"
      );
      revision.hitl_descarte = justificacion;
      return { aprobado: true, revision };
    }

    return { aprobado: false, revision };
  } finally {
    rl.close();
  }
}

export async function pipelineRevisionCompleto(
  codigo: string,
  ruta: string,
  proyectoDir: string
): Promise<RevisionConMeta | { estado: string; revision: null }> {
  let revision: RevisionConMeta = await agenteRevisionConMemoria(
    codigo,
    ruta,
    proyectoDir
  );

  if (necesitaAprobacion(revision)) {
    const resultado = await solicitarAprobacionCli(revision);
    if (!resultado.aprobado) {
      return { estado: "rechazado", revision: null };
    }
    revision = resultado.revision;
  }

  const nombreInforme = `revision_${crypto
    .createHash("md5")
    .update(ruta)
    .digest("hex")
    .slice(0, 8)}.json`;

  ejecutarHerramienta(
    "write_report",
    {
      content: JSON.stringify(revision, null, 2),
      filename: nombreInforme,
    },
    proyectoDir
  );

  return revision;
}

async function main() {
  const args = process.argv.slice(2);
  let codigo: string;
  let ruta: string;
  let proyectoDir: string;

  if (args.length > 0) {
    codigo = require("fs").readFileSync(args[0], "utf-8");
    ruta = args[0];
    proyectoDir = args[1] || process.cwd();
  } else {
    codigo = `
import subprocess

def ejecutar_comando(cmd_usuario):
    # CRÍTICO: inyección de comandos — nunca hacer esto
    resultado = subprocess.run(cmd_usuario, shell=True, capture_output=True, text=True)
    return resultado.stdout
`;
    ruta = "test.py";
    proyectoDir = process.cwd();
  }

  const resultado = await pipelineRevisionCompleto(codigo, ruta, proyectoDir);
  console.log(JSON.stringify(resultado, null, 2));
}

main().catch((err) => {
  console.error("Error:", err.message);
  process.exit(1);
});
