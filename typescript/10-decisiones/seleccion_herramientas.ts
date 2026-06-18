/**
 * Selección dinámica de herramientas por similitud Jaccard.
 *
 * Demuestra el mecanismo de tool retrieval sin dependencias externas:
 * Jaccard sobre word sets reemplaza embeddings para mostrar el bucle
 * selección → agente → selección.
 *
 * Requiere: npm install @anthropic-ai/sdk
 */
import Anthropic from "@anthropic-ai/sdk";

const MODEL = process.env["MODEL"] ?? "claude-sonnet-4-6";
const JACCARD_THRESHOLD = 0.05;
const TOP_K = 3;

const SYSTEM_AGENTE =
  "Eres un agente de asistencia. Tienes acceso a un subconjunto de herramientas " +
  "relevantes para la tarea actual. Usa las herramientas disponibles para responder. " +
  "Si no tienes suficiente información después de una ronda, indica qué necesitarías.";

interface Tool {
  name: string;
  description: string;
  indexText: string;
}

// --- Indexación offline ---

// Cómo ejecutar: make ts SCRIPT=typescript/10-decisiones/seleccion_herramientas.ts


function indexarTools(tools: Tool[]): Map<string, Set<string>> {
  const index = new Map<string, Set<string>>();
  for (const tool of tools) {
    index.set(tool.name, new Set(tool.indexText.toLowerCase().split(/\s+/)));
  }
  return index;
}

// --- Selección en runtime ---

function seleccionarTools(
  query: string,
  index: Map<string, Set<string>>,
  toolsByName: Map<string, Tool>,
  k = TOP_K,
  threshold = JACCARD_THRESHOLD
): Tool[] {
  const queryWords = new Set(query.toLowerCase().split(/\s+/));
  const scores: [string, number][] = [];

  for (const [toolName, toolWords] of index) {
    const union = new Set([...queryWords, ...toolWords]);
    const inter = [...queryWords].filter((w) => toolWords.has(w));
    const score = union.size > 0 ? inter.length / union.size : 0;
    scores.push([toolName, score]);
  }

  scores.sort((a, b) => b[1] - a[1]);

  return scores
    .slice(0, k)
    .filter(([, score]) => score >= threshold)
    .map(([name]) => toolsByName.get(name)!)
    .filter(Boolean);
}

function construirQuerySeleccion(
  tarea: string,
  ultimoResultado: string | null
): string {
  if (!ultimoResultado) return tarea;
  return `${tarea} — contexto: ${ultimoResultado.slice(0, 200)}`;
}

// --- Agente con selección dinámica ---

async function agente(
  tarea: string,
  tools: Tool[],
  client: Anthropic
): Promise<void> {
  const index = indexarTools(tools);
  const toolsByName = new Map(tools.map((t) => [t.name, t]));
  let ultimoResultado: string | null = null;

  console.log(`\nTarea: ${tarea}`);
  console.log("=".repeat(60));

  for (let turno = 1; turno <= 2; turno++) {
    const querySel = construirQuerySeleccion(tarea, ultimoResultado);
    const toolsSel = seleccionarTools(querySel, index, toolsByName);

    console.log(`\n[Turno ${turno}] Query de selección: ${querySel.slice(0, 80)}`);
    console.log(`[Turno ${turno}] Tools seleccionadas: ${toolsSel.map((t) => t.name).join(", ")}`);

    const toolDefs = toolsSel.map((t) => ({
      name: t.name,
      description: t.description,
      input_schema: {
        type: "object" as const,
        properties: {
          query: { type: "string", description: "Parámetro de consulta" },
        },
        required: ["query"],
      },
    }));

    const response = await client.messages.create({
      model: MODEL,
      max_tokens: 512,
      system: SYSTEM_AGENTE,
      tools: toolDefs,
      messages: [{ role: "user", content: tarea }],
    });

    const acciones: string[] = [];
    for (const block of response.content) {
      if (block.type === "tool_use") {
        acciones.push(`${block.name}(${JSON.stringify(block.input)})`);
      }
    }

    if (acciones.length > 0) {
      ultimoResultado = `Llamadas planeadas: ${acciones.join("; ")}`;
      console.log(`[Turno ${turno}] Acciones: ${ultimoResultado}`);
    } else {
      const texto = response.content
        .filter((b) => b.type === "text")
        .map((b) => (b as { type: "text"; text: string }).text)
        .join("");
      ultimoResultado = texto.slice(0, 200);
      console.log(`[Turno ${turno}] Respuesta directa: ${ultimoResultado}`);
      break;
    }
  }
}

// --- Catálogo de herramientas ---

const TOOLS: Tool[] = [
  {
    name: "buscar_contratos",
    description: "Busca contratos por nombre de cliente, fecha o estado",
    indexText: "buscar contratos cliente acuerdo documento legal renovación fecha vencimiento",
  },
  {
    name: "calcular_fechas",
    description: "Calcula diferencias entre fechas, días hasta vencimiento, rangos",
    indexText: "calcular fechas diferencia días semanas meses vencimiento plazo duración",
  },
  {
    name: "consultar_crm",
    description: "Obtiene información de clientes del CRM: contactos, historial, estado",
    indexText: "crm cliente contacto historial estado cuenta empresa organización",
  },
  {
    name: "generar_factura",
    description: "Crea facturas con detalle de servicios, impuestos y datos de pago",
    indexText: "factura generar crear cobro pago servicio impuesto importe total",
  },
  {
    name: "enviar_email",
    description: "Envía correos electrónicos a clientes o equipos internos",
    indexText: "email correo enviar notificación mensaje destinatario asunto adjunto",
  },
  {
    name: "analizar_logs",
    description: "Analiza logs de sistema para diagnosticar errores y anomalías",
    indexText: "logs errores sistema diagnosticar anomalía stack trace excepción servidor",
  },
];

async function main(): Promise<void> {
  const client = new Anthropic();

  await agente(
    "Busca el contrato de Acme Corp y calcula cuántos días faltan para su renovación",
    TOOLS,
    client
  );
}

main().catch(console.error);
