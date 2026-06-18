// Fase 1: llamada única al LLM sin herramientas. Verifica que el system prompt
// produce el schema JSON correcto antes de añadir complejidad.

// Cómo ejecutar: make ts SCRIPT=typescript/16-proyecto/fase1_loop.ts

import Anthropic from "@anthropic-ai/sdk";

const SYSTEM_PROMPT = `Eres un agente de revisión de código.
Recibes código Python y produces una revisión técnica estructurada.

Tu revisión debe identificar:
- Bugs (severidad: critical, high, medium, low)
- Problemas de estilo o mantenibilidad
- Sugerencias de mejora

Responde SIEMPRE en JSON con este schema:
{
  "hallazgos": [
    {
      "linea": <número o null>,
      "severidad": "<critical|high|medium|low>",
      "tipo": "<bug|estilo|rendimiento|seguridad>",
      "descripcion": "<descripción del hallazgo>",
      "sugerencia": "<cómo corregirlo>"
    }
  ],
  "resumen": "<párrafo de resumen de la revisión>"
}`;

interface Hallazgo {
  linea?: number | null;
  severidad: "critical" | "high" | "medium" | "low";
  tipo: "bug" | "estilo" | "rendimiento" | "seguridad";
  descripcion: string;
  sugerencia: string;
}

interface RevisionOutput {
  hallazgos: Hallazgo[];
  resumen: string;
}

function extraerJSON(texto: string): unknown {
  const inicio = texto.indexOf("{");
  if (inicio === -1) throw new Error("No se encontró JSON en la respuesta");
  const segmento = texto.slice(inicio);
  const fin = segmento.lastIndexOf("}");
  if (fin === -1) throw new Error("JSON incompleto en la respuesta");
  return JSON.parse(segmento.slice(0, fin + 1));
}

async function revisarCodigo(codigo: string): Promise<RevisionOutput> {
  const cliente = new Anthropic();

  const respuesta = await cliente.messages.create({
    model: process.env["MODEL"] ?? "claude-sonnet-4-6",
    max_tokens: 2048,
    system: SYSTEM_PROMPT,
    messages: [
      {
        role: "user",
        content: `Revisa este código:\n\n\`\`\`python\n${codigo}\n\`\`\``,
      },
    ],
  });

  const bloque = respuesta.content[0];
  if (bloque.type !== "text") {
    throw new Error("Respuesta inesperada: no es texto");
  }

  return extraerJSON(bloque.text) as RevisionOutput;
}

async function main() {
  const codigoEjemplo = `
def calcular_promedio(numeros):
    total = 0
    for n in numeros:
        total += n
    return total / len(numeros)  # bug: ZeroDivisionError si numeros está vacío

usuarios = {}
def get_usuario(id):
    return usuarios[id]  # bug: KeyError si id no existe
`;

  const resultado = await revisarCodigo(codigoEjemplo);
  console.log(JSON.stringify(resultado, null, 2));
}

main().catch((err) => {
  console.error("Error:", err.message);
  process.exit(1);
});
