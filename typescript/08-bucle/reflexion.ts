/**
 * Reflexion — Shinn et al. 2023 (arXiv:2303.11366).
 *
 * ReflexionAgent: loop actor → evaluador → reflector hasta maxIntentos.
 * Evaluator: interfaz común con tres implementaciones:
 *   - UnitTestEvaluator: test determinista
 *   - HeuristicEvaluator: heurísticas sin modelo
 *   - LLMJudgeEvaluator: LLM-as-judge
 * slidingWindowMemory: mantiene solo las últimas N reflexiones.
 *
 * Requiere: npm install @anthropic-ai/sdk
 */
import Anthropic from "@anthropic-ai/sdk";

const MODEL = process.env["MODEL"] ?? "claude-sonnet-4-6";
const MAX_INTENTOS = 3;
const MAX_REFLEXIONES = 3;

interface Trayectoria {
  pasos: string[];
  resultadoFinal: string;
}

interface Evaluator {
  evaluar(t: Trayectoria, tarea: string): Promise<[boolean, string]>;
}

// UnitTestEvaluator: test determinista

// Cómo ejecutar: make ts SCRIPT=typescript/08-bucle/reflexion.ts

function makeUnitTestEvaluator(testFn: (r: string) => boolean): Evaluator {
  return {
    async evaluar(t, _tarea) {
      try {
        const ok = testFn(t.resultadoFinal);
        return [ok, ok ? "Test superado." : "El resultado no cumple el criterio."];
      } catch (e) {
        return [false, `Excepción en el test: ${e}`];
      }
    },
  };
}

// HeuristicEvaluator: sin llamada a modelo
function makeHeuristicEvaluator(): Evaluator {
  return {
    async evaluar(t, _tarea) {
      if (!t.resultadoFinal.trim())
        return [false, "El agente no produjo respuesta."];
      const pasos = t.pasos;
      if (pasos.length >= 2 && pasos.at(-1) === pasos.at(-2))
        return [false, "El agente repitió el mismo paso dos veces."];
      return [true, "Heurísticas superadas."];
    },
  };
}

// LLMJudgeEvaluator: LLM-as-judge
function makeLLMJudgeEvaluator(
  client: Anthropic,
  model = MODEL
): Evaluator {
  return {
    async evaluar(t, tarea) {
      const resp = await client.messages.create({
        model,
        max_tokens: 150,
        messages: [
          {
            role: "user",
            content:
              `Tarea: ${tarea}\nRespuesta: ${t.resultadoFinal}\n\n` +
              "¿La respuesta completa la tarea? Responde: ÉXITO o FALLO y una frase de feedback.",
          },
        ],
      });
      const texto =
        resp.content[0].type === "text" ? resp.content[0].text : "";
      return [texto.toUpperCase().includes("ÉXITO"), texto];
    },
  };
}

function slidingWindowMemory(reflexiones: string[], maxN = MAX_REFLEXIONES): string[] {
  return reflexiones.slice(-maxN);
}

async function reflexionar(
  client: Anthropic,
  tarea: string,
  intento: number,
  trayectoria: Trayectoria,
  feedback: string,
  model = MODEL
): Promise<string> {
  const prompt =
    `Tarea: ${tarea}\nIntento #${intento} — resultado: FALLIDO\n` +
    `Trayectoria:\n${trayectoria.pasos.join("\n").slice(0, 1500)}\n` +
    `Feedback: ${feedback}\n\n` +
    "Reflexiona sobre qué salió mal y qué harías diferente (máx 80 palabras).";
  const resp = await client.messages.create({
    model,
    max_tokens: 200,
    messages: [{ role: "user", content: prompt }],
  });
  return resp.content[0].type === "text" ? resp.content[0].text.trim() : "";
}

async function ejecutarActor(
  client: Anthropic,
  tarea: string,
  reflexiones: string[],
  model = MODEL
): Promise<Trayectoria> {
  const bloque = reflexiones.length
    ? `Reflexiones de intentos previos:\n${reflexiones.map((r) => `- ${r}`).join("\n")}\n\n`
    : "";
  const resp = await client.messages.create({
    model,
    max_tokens: 500,
    messages: [
      { role: "user", content: `${bloque}Completa esta tarea:\n\n${tarea}` },
    ],
  });
  return {
    pasos: [`Actor ejecutado con ${reflexiones.length} reflexiones previas.`],
    resultadoFinal:
      resp.content[0].type === "text" ? resp.content[0].text.trim() : "",
  };
}

async function runReflexion(
  client: Anthropic,
  tarea: string,
  evaluator: Evaluator,
  maxIntentos = MAX_INTENTOS,
  actorModel = MODEL
): Promise<string> {
  const memoria: string[] = [];
  let lastTrayectoria: Trayectoria = { pasos: [], resultadoFinal: "" };

  for (let intento = 1; intento <= maxIntentos; intento++) {
    const tray = await ejecutarActor(
      client,
      tarea,
      slidingWindowMemory(memoria),
      actorModel
    );
    lastTrayectoria = tray;
    const [exito, feedback] = await evaluator.evaluar(tray, tarea);

    console.log(
      `[intento ${intento}/${maxIntentos}] ${exito ? "✓" : "✗"} ${feedback.slice(0, 70)}`
    );

    if (exito) return tray.resultadoFinal;

    if (intento < maxIntentos) {
      const ref = await reflexionar(client, tarea, intento, tray, feedback);
      memoria.push(ref);
      console.log(`  Reflexión: ${ref.slice(0, 90)}`);
    }
  }

  return lastTrayectoria.resultadoFinal;
}

// Demo
(async () => {
  const client = new Anthropic();

  const evaluator = makeUnitTestEvaluator((resultado) => {
    const nums = [...resultado.matchAll(/\b\d+\b/g)].map((m) => parseInt(m[0]));
    return nums.some((n) => n >= 10 && n <= 50);
  });

  const resultado = await runReflexion(
    client,
    "Escribe exactamente un número entero entre 10 y 50, " +
      "seguido de por qué elegiste ese número.",
    evaluator
  );
  console.log(`\nResultado final: ${resultado.slice(0, 200)}`);
})();
