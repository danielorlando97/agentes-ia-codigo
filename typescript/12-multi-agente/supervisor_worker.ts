// Patrón supervisor/worker: un LLM descompone la tarea y despacha a workers especializados.

// Cómo ejecutar: make ts SCRIPT=typescript/12-multi-agente/supervisor_worker.ts

import Anthropic from "@anthropic-ai/sdk";

const client = new Anthropic();
const MODEL = process.env["MODEL"] ?? "claude-haiku-4-5-20251001";

const WORKERS: Record<string, string> = {
  investigador:
    "Eres un investigador. Busca y sintetiza información factual. Devuelve hechos concretos.",
  redactor:
    "Eres un redactor. Redacta contenido claro y bien estructurado basado en el contexto dado.",
  revisor:
    "Eres un revisor. Identifica problemas concretos y devuelve el texto corregido.",
};

const SUPERVISOR_SYSTEM = `Eres un supervisor que descompone tareas y las despacha a workers.
Workers disponibles: investigador, redactor, revisor.
Planifica los pasos necesarios. Responde SIEMPRE con JSON válido.

Para planificar: {"accion": "planificar", "pasos": [{"worker": "<nombre>", "instruccion": "<qué hacer>"}]}
Para terminar:   {"accion": "terminar", "respuesta": "<respuesta final>"}
Para redirigir:  {"accion": "redirigir", "worker": "<nombre>", "correccion": "<qué corregir>"}`;

type Paso = { worker: string; instruccion: string };
type Decision =
  | { accion: "planificar"; pasos: Paso[] }
  | { accion: "terminar"; respuesta: string }
  | { accion: "redirigir"; worker: string; correccion: string };

async function llamarWorker(worker: string, instruccion: string): Promise<string> {
  const resp = await client.messages.create({
    model: MODEL,
    max_tokens: 800,
    system: WORKERS[worker],
    messages: [{ role: "user", content: instruccion }],
  });
  return (resp.content[0] as { text: string }).text;
}

async function llamarSupervisor(
  mensajes: Array<{ role: "user" | "assistant"; content: string }>
): Promise<Decision> {
  const resp = await client.messages.create({
    model: MODEL,
    max_tokens: 600,
    system: SUPERVISOR_SYSTEM,
    messages: mensajes,
  });
  const texto = (resp.content[0] as { text: string }).text;
  const inicio = texto.indexOf("{");
  const fin = texto.lastIndexOf("}") + 1;
  return JSON.parse(texto.slice(inicio, fin)) as Decision;
}

async function supervisorWorker(tarea: string, maxRondas = 3): Promise<string> {
  const mensajes: Array<{ role: "user" | "assistant"; content: string }> = [
    { role: "user", content: `Tarea: ${tarea}` },
  ];
  const resultados: Record<string, string> = {};

  // Fase 1: planificación
  let decision = await llamarSupervisor(mensajes);
  mensajes.push({ role: "assistant", content: JSON.stringify(decision) });

  if (decision.accion !== "planificar") {
    return (decision as { accion: "terminar"; respuesta: string }).respuesta ?? "Error al planificar.";
  }

  // Fase 2: ejecución del plan
  for (const paso of decision.pasos) {
    let instruccion = paso.instruccion;
    for (const [nombre, resultado] of Object.entries(resultados)) {
      instruccion = instruccion.replaceAll(`$${nombre}`, resultado.slice(0, 500));
    }

    const resultado = await llamarWorker(paso.worker, instruccion);
    resultados[paso.worker] = resultado;
    mensajes.push({ role: "user", content: `Resultado de ${paso.worker}:\n${resultado}` });
  }

  // Fase 3: evaluación del supervisor
  for (let ronda = 0; ronda < maxRondas; ronda++) {
    mensajes.push({
      role: "user",
      content: "¿La tarea está completa? Responde con JSON: terminar o redirigir.",
    });
    decision = await llamarSupervisor(mensajes);
    mensajes.push({ role: "assistant", content: JSON.stringify(decision) });

    if (decision.accion === "terminar") {
      return decision.respuesta;
    }

    if (decision.accion === "redirigir") {
      const resultadoCorregido = await llamarWorker(decision.worker, decision.correccion);
      resultados[decision.worker] = resultadoCorregido;
      mensajes.push({
        role: "user",
        content: `Resultado corregido de ${decision.worker}:\n${resultadoCorregido}`,
      });
    }
  }

  return resultados["revisor"] ?? resultados["redactor"] ?? "Sin resultado.";
}

const tarea = "Escribe un párrafo explicando qué es el patrón supervisor/worker en sistemas multi-agente.";
console.log(`Tarea: ${tarea}\n`);
supervisorWorker(tarea).then((r) => console.log(`Resultado:\n${r}`));
