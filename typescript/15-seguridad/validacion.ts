/**
 * Validación de output de agentes — tres capas independientes.
 *
 * Demuestra las tres capas de validación:
 * 1. Esquema: el output tiene el formato correcto (JSON)
 * 2. Contenido: el output no contiene datos sensibles (regex)
 * 3. Acción: las tool calls son seguras para ejecutar
 *
 * Sin API key — las llamadas al LLM son simuladas.
 *
 * Uso:
 *   npx ts-node validacion.ts
 *   npx ts-node validacion.ts --capa esquema
 *   npx ts-node validacion.ts --capa contenido
 *   npx ts-node validacion.ts --capa accion
 */

// ─────────────────────────────────────────────
// Tipos
// ─────────────────────────────────────────────

// Cómo ejecutar: make ts SCRIPT=typescript/15-seguridad/validacion.ts


interface ToolCall {
  nombre: string;
  params: Record<string, unknown>;
}

interface OutputAgente {
  respuesta: string;
  acciones: ToolCall[];
  confianza: number;
  referencias: string[];
}

interface ResultadoValidacion {
  valido: boolean;
  capaFallida: string | null;
  motivo: string | null;
  output: OutputAgente | null;
}

// ─────────────────────────────────────────────
// Capa 1: validación de esquema
// ─────────────────────────────────────────────

function validarEsquema(outputRaw: string): [OutputAgente | null, string | null] {
  let data: Record<string, unknown>;
  try {
    data = JSON.parse(outputRaw);
  } catch (e) {
    return [null, `JSON inválido: ${(e as Error).message}`];
  }

  if (!("respuesta" in data)) return [null, "Campo 'respuesta' ausente"];

  if (typeof data.confianza !== "undefined" && typeof data.confianza !== "number") {
    return [null, "Campo 'confianza' debe ser número"];
  }
  const confianza = typeof data.confianza === "number" ? data.confianza : 1.0;
  if (confianza < 0.0 || confianza > 1.0) {
    return [null, "Campo 'confianza' debe estar entre 0.0 y 1.0"];
  }

  const acciones: ToolCall[] = [];
  for (const a of (data.acciones as Array<Record<string, unknown>>) ?? []) {
    if (!("nombre" in a)) return [null, "Tool call sin campo 'nombre'"];
    acciones.push({
      nombre: a.nombre as string,
      params: (a.params as Record<string, unknown>) ?? {},
    });
  }

  return [
    {
      respuesta: data.respuesta as string,
      acciones,
      confianza,
      referencias: (data.referencias as string[]) ?? [],
    },
    null,
  ];
}

// ─────────────────────────────────────────────
// Capa 2: validación de contenido
// ─────────────────────────────────────────────

const PATRONES_SENSIBLES: Array<[RegExp, string]> = [
  [/\b\d{3}-\d{2}-\d{4}\b/, "SSN"],
  [/\b\d{4}[\s-]\d{4}[\s-]\d{4}[\s-]\d{4}\b/, "tarjeta de crédito"],
  [/password:\s*\S+/i, "contraseña"],
  [/api[_-]?key:\s*\S+/i, "API key"],
  [/token:\s*[A-Za-z0-9._-]{20,}/i, "token de sesión"],
];

function validarContenido(output: OutputAgente): [boolean, string | null] {
  for (const [patron, tipo] of PATRONES_SENSIBLES) {
    if (patron.test(output.respuesta)) {
      return [false, `Dato sensible en respuesta: ${tipo}`];
    }
  }
  return [true, null];
}

// ─────────────────────────────────────────────
// Capa 3: validación de acción
// ─────────────────────────────────────────────

const ACCIONES_PROHIBIDAS = new Set(["delete_database", "drop_table", "rm_rf", "send_bulk_email"]);
const DIRECTORIO_TRABAJO = "/workspace";

function validarAccion(toolCall: ToolCall): [boolean, string | null] {
  if (ACCIONES_PROHIBIDAS.has(toolCall.nombre)) {
    return [false, `Acción prohibida: '${toolCall.nombre}'`];
  }

  if (toolCall.nombre === "write_file") {
    const ruta = (toolCall.params.path as string) ?? "";
    if (ruta.includes("..")) return [false, `Path traversal detectado: '${ruta}'`];
    if (!ruta.startsWith(DIRECTORIO_TRABAJO)) {
      return [false, `Escritura fuera del directorio de trabajo: '${ruta}'`];
    }
  }

  if (toolCall.nombre === "send_email") {
    const destinatarios = (toolCall.params.to as string[]) ?? [];
    const externos = destinatarios.filter((d) => !d.endsWith("@empresa.com"));
    if (externos.length > 0) {
      return [false, `Email a destino no autorizado: ${externos.join(", ")}`];
    }
  }

  return [true, null];
}

// ─────────────────────────────────────────────
// Pipeline completo
// ─────────────────────────────────────────────

function validarPipeline(outputRaw: string): ResultadoValidacion {
  const [output, errorEsquema] = validarEsquema(outputRaw);
  if (output === null) {
    return { valido: false, capaFallida: "esquema", motivo: errorEsquema, output: null };
  }

  const [contenidoOk, motivoContenido] = validarContenido(output);
  if (!contenidoOk) {
    return { valido: false, capaFallida: "contenido", motivo: motivoContenido, output: null };
  }

  for (const accion of output.acciones) {
    const [accionOk, motivoAccion] = validarAccion(accion);
    if (!accionOk) {
      return {
        valido: false,
        capaFallida: "accion",
        motivo: `[${accion.nombre}] ${motivoAccion}`,
        output: null,
      };
    }
  }

  return { valido: true, capaFallida: null, motivo: null, output };
}

// ─────────────────────────────────────────────
// Casos de prueba
// ─────────────────────────────────────────────

const CASOS: Record<string, string> = {
  valido_sin_acciones: JSON.stringify({
    respuesta: "Tu pedido llega el jueves.",
    confianza: 0.95,
    referencias: ["pedido_12345"],
  }),
  valido_con_accion_segura: JSON.stringify({
    respuesta: "He creado el ticket de soporte.",
    acciones: [{ nombre: "write_file", params: { path: "/workspace/tickets/t001.txt", content: "..." } }],
    confianza: 0.88,
  }),
  falla_esquema_json: '{"respuesta": "incompleto"',
  falla_esquema_campo: JSON.stringify({ texto: "sin campo respuesta" }),
  falla_contenido_ssn: JSON.stringify({
    respuesta: "El SSN del usuario es 123-45-6789.",
    confianza: 0.5,
  }),
  falla_contenido_apikey: JSON.stringify({
    respuesta: "La API key es: api_key: sk-abcdef123456",
    confianza: 0.7,
  }),
  falla_accion_prohibida: JSON.stringify({
    respuesta: "Limpiando base de datos.",
    acciones: [{ nombre: "delete_database", params: { confirm: true } }],
    confianza: 0.9,
  }),
  falla_accion_path_traversal: JSON.stringify({
    respuesta: "Archivo escrito.",
    acciones: [{ nombre: "write_file", params: { path: "../../etc/passwd", content: "..." } }],
    confianza: 0.85,
  }),
  falla_email_externo: JSON.stringify({
    respuesta: "Email enviado.",
    acciones: [{ nombre: "send_email", params: { to: ["atacante@external.com"], body: "datos..." } }],
    confianza: 0.7,
  }),
};

const CASOS_FILTRADOS: Record<string, string[]> = {
  esquema: ["falla_esquema_json", "falla_esquema_campo", "valido_sin_acciones"],
  contenido: ["falla_contenido_ssn", "falla_contenido_apikey", "valido_sin_acciones"],
  accion: [
    "falla_accion_prohibida",
    "falla_accion_path_traversal",
    "falla_email_externo",
    "valido_con_accion_segura",
  ],
};

function demoCapa(capa: string | null): void {
  const nombres = capa ? (CASOS_FILTRADOS[capa] ?? Object.keys(CASOS)) : Object.keys(CASOS);
  const sep = "=".repeat(64);
  const titulo = capa ? `capa ${capa.toUpperCase()}` : "pipeline completo";

  console.log(`\n${sep}`);
  console.log(`  VALIDACIÓN DE OUTPUT — ${titulo}`);
  console.log(`${sep}`);
  console.log(`  ${"Caso".padEnd(38)} ${"Válido".padEnd(8)} ${"Detalle"}`);
  console.log(`  ${"-".repeat(38)} ${"-".repeat(8)} ${"-".repeat(28)}`);

  for (const nombre of nombres) {
    if (!(nombre in CASOS)) continue;
    const r = validarPipeline(CASOS[nombre]);
    const validoStr = r.valido ? "✓" : `✗ [${r.capaFallida}]`;
    const detalle = r.motivo ? r.motivo.substring(0, 35) : r.valido ? "OK" : "";
    console.log(`  ${nombre.padEnd(38)} ${validoStr.padEnd(8)} ${detalle}`);
  }

  console.log(`${sep}\n`);
}

function main(): void {
  const args = process.argv;
  const capaIdx = args.indexOf("--capa");
  const capa = capaIdx >= 0 ? args[capaIdx + 1] : null;
  demoCapa(capa);
}

main();
