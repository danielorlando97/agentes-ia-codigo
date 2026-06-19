// UX de agentes interactivos — format_approval_request.
//
// Transforma una acción técnica del agente (SQL, parámetros de herramienta)
// en una solicitud de aprobación legible para humanos no técnicos.
//
// La clave: título en lenguaje de negocio, impacto cuantificado, opciones
// que capturan matices más allá de aprobar/rechazar.
//
// Cómo ejecutar:
//   make ts SCRIPT=typescript/13-hitl/ux.ts

interface Opcion {
  etiqueta: string;
  accion: string;
  descripcion: string;
}

interface SolicitudAprobacion {
  titulo: string;
  descripcion: string;
  impacto: string;
  reversible: boolean;
  vistaPrevia: string[];
  contexto: string;
  opciones: Opcion[];
}

function mostrarSolicitud(s: SolicitudAprobacion): void {
  const reversibleStr = s.reversible ? "Sí" : "NO (irreversible)";
  console.log(`  Título:      ${s.titulo}`);
  console.log(`  Descripción: ${s.descripcion}`);
  console.log(`  Impacto:     ${s.impacto}`);
  console.log(`  Reversible:  ${reversibleStr}`);
  if (s.vistaPrevia.length > 0) {
    console.log(`  Muestra:     ${s.vistaPrevia.join(", ")}`);
  }
  console.log(`  Contexto:    ${s.contexto}`);
  const opcionesStr = s.opciones.map((o) => `[${o.etiqueta}]`).join(" | ");
  console.log(`  Opciones:    ${opcionesStr}`);
}

interface AccionParams {
  tabla?: string;
  condicion?: string;
  n_registros?: number;
  ids?: unknown[];
  reversible?: boolean;
  ejemplos?: string[];
  to?: string[];
  asunto?: string;
}

interface ContextoAgente {
  razon?: string;
  ultimos_pasos?: string[];
}

function formatApprovalRequest(
  herramienta: string,
  params: AccionParams,
  contextoAgente: ContextoAgente,
): SolicitudAprobacion {
  const nAfectados = herramienta === "send_email"
    ? (params.to?.length ?? 0)
    : (params.n_registros ?? (params.ids?.length ?? 0));
  const entidad = params.tabla ?? "registros";
  const condicion = params.condicion ?? "";

  let titulo: string;
  if (herramienta === "db_delete") {
    titulo = `Eliminar ${nAfectados} ${entidad} ${condicion}`.trim();
  } else if (herramienta === "send_email") {
    titulo = `Enviar email a ${params.to?.length ?? 0} destinatarios`;
  } else if (herramienta === "db_update") {
    titulo = `Actualizar ${nAfectados} ${entidad} ${condicion}`.trim();
  } else {
    titulo = `Ejecutar ${herramienta}`;
  }

  const razon = contextoAgente.razon ?? "completar la tarea actual";
  const ultimosPasos = contextoAgente.ultimos_pasos ?? [];

  return {
    titulo,
    descripcion: `El agente propone esto porque: ${razon}`,
    impacto: `Afectará ${nAfectados} ${entidad}`,
    reversible: params.reversible ?? true,
    vistaPrevia: (params.ejemplos ?? []).slice(0, 5),
    contexto: ultimosPasos.length > 0
      ? ultimosPasos.slice(-3).join(" → ")
      : "inicio del flujo",
    opciones: [
      { etiqueta: "Aprobar", accion: "aprobar", descripcion: "Ejecutar la acción con los parámetros actuales" },
      { etiqueta: "Rechazar", accion: "rechazar", descripcion: "Cancelar la acción y notificar al agente" },
      { etiqueta: "Modificar parámetros", accion: "modificar", descripcion: "Ajustar el alcance antes de ejecutar" },
      { etiqueta: "Escalar a supervisor", accion: "escalar", descripcion: "Enviar la decisión a un nivel superior" },
      { etiqueta: "Posponer 24h", accion: "posponer", descripcion: "Ejecutar mañana a la misma hora" },
    ],
  };
}

function main(): void {
  console.log("=== UX de agentes: format_approval_request ===\n");

  // Caso 1: eliminación destructiva
  console.log("--- Caso 1: Eliminación irreversible ---\n");

  const solicitud1 = formatApprovalRequest(
    "db_delete",
    {
      tabla: "usuarios",
      condicion: "inactivos desde ene 2024",
      n_registros: 1247,
      reversible: false,
      ejemplos: ["user@ejemplo.com", "otro@ejemplo.com", "tercero@ejemplo.com"],
    },
    {
      razon: "completar la limpieza de cuentas inactivas solicitada",
      ultimos_pasos: ["analizar tabla usuarios", "filtrar por actividad", "calcular impacto"],
    },
  );

  console.log("SIN FORMAT (lo que el agente produce internamente):");
  console.log("  DELETE FROM usuarios WHERE inactive AND last_login < '2024-01-01'\n");
  console.log("CON FORMAT (lo que el humano ve):");
  mostrarSolicitud(solicitud1);

  // Caso 2: envío de emails
  console.log("\n--- Caso 2: Envío masivo de emails ---\n");

  const solicitud2 = formatApprovalRequest(
    "send_email",
    {
      to: new Array(843).fill("usuario@empresa.com"),
      asunto: "Actualización de términos de servicio",
      reversible: false,
    },
    {
      razon: "notificar a todos los usuarios del cambio de ToS antes del 30/06",
      ultimos_pasos: ["redactar email", "seleccionar destinatarios"],
    },
  );

  console.log("CON FORMAT:");
  mostrarSolicitud(solicitud2);

  // Indicador de progreso
  console.log("\n--- Indicador de progreso (ejecución larga) ---\n");
  const steps: [string, string, string][] = [
    ["✓", "Analizar estructura del repositorio", "2.3s"],
    ["✓", "Identificar tests existentes", "1.1s"],
    ["→", "Generando nuevos tests para módulo auth...", "30s estimado"],
    [" ", "Ejecutar suite de tests", "pendiente"],
    [" ", "Generar reporte de cobertura", "pendiente"],
  ];
  for (const [estado, descripcion, tiempo] of steps) {
    console.log(`  [${estado}] ${descripcion} (${tiempo})`);
  }
}

main();
