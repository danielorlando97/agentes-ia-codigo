// Formato ReAct (Reasoning + Acting) para modelos sin function calling nativo.
//
// ReAct intercala Thought/Action/Observation en texto libre.
// El cliente parsea la Action con regex, ejecuta la herramienta,
// e inyecta la Observation antes de que el modelo continúe.
//
// Stop sequence "Observation:" interrumpe la generación para
// que el cliente inyecte el resultado real de la herramienta.

// Cómo ejecutar: make ts SCRIPT=typescript/05-herramientas/10-formatos/react-text.ts

import Anthropic from "@anthropic-ai/sdk";

const MODEL = process.env["MODEL"] ?? "claude-sonnet-4-6";

// --- System prompt ReAct ---

const REACT_SYSTEM = `Responde usando EXACTAMENTE el siguiente formato:

Thought: [tu razonamiento sobre qué hacer a continuación]
Action: ToolName[argumento]
Observation: [resultado de la herramienta — lo inyecta el sistema]

Repite Thought/Action/Observation hasta tener la respuesta final, luego:
Thought: Tengo la información necesaria para responder.
Action: Finish[respuesta completa aquí]

Herramientas disponibles:
- Search[query]: Busca información. Ejemplo: Search[capital of France]
- Calculate[expresion]: Evalúa expresión matemática. Ejemplo: Calculate[15 * 8 + 3]
- Finish[respuesta]: Termina y devuelve la respuesta final.

IMPORTANTE: usa exactamente el formato ToolName[argumento] con corchetes.`;

const REACT_FEW_SHOT = `
Ejemplo:
Pregunta: ¿Cuánto es el doble de la población de Madrid?
Thought: Necesito buscar la población de Madrid y luego multiplicarla por 2.
Action: Search[population of Madrid]
Observation: La población de Madrid es aproximadamente 3.3 millones de personas.
Thought: Ahora calculo el doble: 3.3 * 2.
Action: Calculate[3.3 * 2]
Observation: 6.6
Thought: Tengo la respuesta final.
Action: Finish[El doble de la población de Madrid es 6.6 millones de personas]

---`;

// --- Herramientas mock ---

function mockSearch(query: string): string {
  const db: Record<string, string> = {
    "population of Madrid": "La población de Madrid es aproximadamente 3.3 millones.",
    "population of Tokyo": "La población de Tokio es aproximadamente 13.96 millones.",
    "capital of France": "La capital de Francia es París.",
    "capital of Japan": "La capital de Japón es Tokio.",
    "height of Eiffel Tower": "La Torre Eiffel mide 330 metros.",
    "distance Madrid Barcelona": "La distancia Madrid-Barcelona es ~621 km.",
  };

  // Buscar coincidencia parcial
  const lower = query.toLowerCase();
  for (const [key, val] of Object.entries(db)) {
    if (lower.includes(key.toLowerCase()) || key.toLowerCase().includes(lower)) {
      return val;
    }
  }
  return `No se encontró información específica sobre "${query}". Intenta con términos más simples.`;
}

function mockCalculate(expression: string): string {
  try {
    const sanitized = expression.replace(/[^0-9+\-*/().\s]/g, "");
    if (!sanitized.trim()) throw new Error("expresión vacía");
    const result = Function(`"use strict"; return (${sanitized})`)() as number;
    return String(Math.round(result * 10000) / 10000);
  } catch {
    return `Error: no se pudo evaluar "${expression}"`;
  }
}

// --- Parser de ReAct ---

interface ParsedAction {
  thought: string;
  toolName: string;
  argument: string;
}

function parseReactOutput(text: string): ParsedAction | null {
  // Extraer Thought
  const thoughtMatch = text.match(/Thought:\s*(.+?)(?=Action:|$)/s);
  const thought = thoughtMatch ? thoughtMatch[1].trim() : "";

  // Extraer Action: ToolName[argumento]
  const actionMatch = text.match(/Action:\s*(\w+)\[([^\]]*)\]/);
  if (!actionMatch) return null;

  return {
    thought,
    toolName: actionMatch[1],
    argument: actionMatch[2].trim(),
  };
}

// --- Loop ReAct ---

async function reactLoop(
  pregunta: string,
  maxPasos = 10
): Promise<string> {
  const client = new Anthropic();

  // Construir el prompt inicial con few-shot
  const promptInicial =
    REACT_FEW_SHOT + `\nPregunta: ${pregunta}\n`;

  // Historial de mensajes — el contexto ReAct crece con cada paso
  let contexto = promptInicial;

  console.log(`Pregunta: ${pregunta}\n`);

  for (let paso = 0; paso < maxPasos; paso++) {
    // Generar hasta que el modelo escriba "Observation:" o "Action: Finish"
    const response = await client.messages.create({
      model: MODEL,
      max_tokens: 512,
      system: REACT_SYSTEM,
      messages: [{ role: "user", content: contexto }],
      stop_sequences: ["Observation:"],
    });

    const generado = response.content
      .filter((b): b is Anthropic.TextBlock => b.type === "text")
      .map((b) => b.text)
      .join("");

    console.log(`[Paso ${paso + 1}]`);
    console.log(generado.trim());

    // Parsear la acción generada
    const parsed = parseReactOutput(generado);
    if (!parsed) {
      console.log("  [warn] no se encontró Action en el output — terminando");
      break;
    }

    // Si es Finish, devolver el argumento como respuesta final
    if (parsed.toolName === "Finish") {
      console.log(`\n[Finish] ${parsed.argument}`);
      return parsed.argument;
    }

    // Ejecutar la herramienta
    let observacion: string;
    if (parsed.toolName === "Search") {
      observacion = mockSearch(parsed.argument);
    } else if (parsed.toolName === "Calculate") {
      observacion = mockCalculate(parsed.argument);
    } else {
      observacion = `Error: herramienta '${parsed.toolName}' no existe`;
    }

    console.log(`Observation: ${observacion}\n`);

    // Añadir al contexto: lo generado + la observación inyectada
    contexto += generado + `Observation: ${observacion}\n`;
  }

  return "Max pasos alcanzados sin respuesta final";
}

async function main() {
  console.log("=== Formato ReAct (Thought/Action/Observation) ===\n");

  const resultado = await reactLoop(
    "¿Cuántos metros cuadrados tiene una habitación de 4.5m × 3.2m?"
  );

  console.log(`\n=== Respuesta final ===\n${resultado}`);
}

main().catch(console.error);
