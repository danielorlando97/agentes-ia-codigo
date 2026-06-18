// Cómo ejecutar: make ts SCRIPT=typescript/08-bucle/ralph-loop.ts
/**
 * Ralph Loop — patrón de Claude Code (arXiv:2604.14228).
 *
 * Sin límite de iteraciones; condición de salida semántica (sin tool_use).
 *   - compactarCascada: 4 capas de reducción de contexto
 *   - ejecutarConPermisos: 5 niveles de autorización
 *   - DiminishingReturnsChecker: detiene el loop si varias iters no producen output útil
 *
 * Requiere: npm install @anthropic-ai/sdk
 */
import Anthropic from "@anthropic-ai/sdk";

const MODEL = process.env["MODEL"] ?? "claude-sonnet-4-6";
const DIMINISHING_RETURNS_TOKENS = 100;
const DIMINISHING_RETURNS_ITERS = 3;
const COMPACTION_THRESHOLD = 0.8;
const HISTORY_BUDGET = 80_000;

enum PermissionLevel {
  READ_ONLY = 1,
  WORKSPACE_READ = 2,
  WORKSPACE_WRITE = 3,
  NETWORK_ACCESS = 4,
  DANGER_FULL = 5,
}

interface ToolSpec {
  name: string;
  fn: (args: Record<string, unknown>) => string;
  permission: PermissionLevel;
  description: string;
  inputSchema: Record<string, unknown>;
}

function estimateTokens(messages: Anthropic.Messages.MessageParam[]): number {
  return Math.floor(JSON.stringify(messages).length / 4);
}

async function compactarCascada(
  messages: Anthropic.Messages.MessageParam[],
  client: Anthropic
): Promise<Anthropic.Messages.MessageParam[]> {
  // Capa 1: limpiar tool_results antiguos
  let trCount = 0;
  const result: Anthropic.Messages.MessageParam[] = [];
  for (const msg of [...messages].reverse()) {
    const content = msg.content;
    if (Array.isArray(content)) {
      const newBlocks = content.map((b) => {
        if (typeof b === "object" && b !== null && "type" in b && b.type === "tool_result") {
          trCount++;
          if (trCount > 6) return { ...b, content: "[cleared]" };
        }
        return b;
      });
      result.unshift({ ...msg, content: newBlocks });
    } else {
      result.unshift(msg);
    }
  }
  let msgs: Anthropic.Messages.MessageParam[] = result;

  if (estimateTokens(msgs) <= HISTORY_BUDGET) return msgs;

  // Capa 2: FIFO
  while (estimateTokens(msgs) > HISTORY_BUDGET && msgs.length > 2) {
    msgs = msgs.slice(1);
  }
  if (estimateTokens(msgs) <= HISTORY_BUDGET) return msgs;

  // Capa 3: sumarización LLM
  if (msgs.length > 8) {
    const head = msgs.slice(0, 2);
    const tail = msgs.slice(-4);
    const middle = msgs.slice(2, -4);
    const resp = await client.messages.create({
      model: process.env["SMALL_MODEL"] ?? "claude-haiku-4-5-20251001",
      max_tokens: 800,
      messages: [
        {
          role: "user",
          content:
            "Resume este historial preservando decisiones y resultados clave:\n" +
            JSON.stringify(middle).slice(0, 8000),
        },
      ],
    });
    const summary =
      resp.content[0].type === "text" ? resp.content[0].text : "";
    msgs = [
      ...head,
      { role: "user", content: `[HISTORIAL COMPRIMIDO]\n${summary}` },
      ...tail,
    ];
  }

  return msgs;
}

function ejecutarConPermisos(
  spec: ToolSpec,
  args: Record<string, unknown>,
  level: PermissionLevel
): [string, boolean] {
  if (level < spec.permission) {
    return [
      `[Denegado: requiere ${PermissionLevel[spec.permission]}, actual ${PermissionLevel[level]}]`,
      false,
    ];
  }
  try {
    return [spec.fn(args), true];
  } catch (e) {
    return [`[Error en ${spec.name}]: ${e}`, false];
  }
}

class DiminishingReturnsChecker {
  private belowCount = 0;
  constructor(
    private minTokens = DIMINISHING_RETURNS_TOKENS,
    private maxConsecutive = DIMINISHING_RETURNS_ITERS
  ) {}
  check(tokensOutput: number): boolean {
    if (tokensOutput < this.minTokens) this.belowCount++;
    else this.belowCount = 0;
    return this.belowCount >= this.maxConsecutive;
  }
}

async function ralphLoop(
  userRequest: string,
  toolSpecs: ToolSpec[],
  client: Anthropic,
  systemPrompt = "Eres un asistente útil.",
  permissionLevel = PermissionLevel.WORKSPACE_READ
): Promise<string> {
  let messages: Anthropic.Messages.MessageParam[] = [
    { role: "user", content: userRequest },
  ];

  const toolsApi: Anthropic.Messages.Tool[] = toolSpecs.map((ts) => ({
    name: ts.name,
    description: ts.description,
    input_schema: ts.inputSchema as Anthropic.Messages.Tool["input_schema"],
  }));
  const toolMap = new Map(toolSpecs.map((ts) => [ts.name, ts]));
  const dr = new DiminishingReturnsChecker();
  let iteration = 0;

  while (true) {
    iteration++;

    if (estimateTokens(messages) > HISTORY_BUDGET * COMPACTION_THRESHOLD) {
      messages = await compactarCascada(messages, client);
      console.log(`  [compactado → ~${estimateTokens(messages)}t]`);
    }

    const resp = await client.messages.create({
      model: MODEL,
      max_tokens: 2048,
      system: systemPrompt,
      tools: toolsApi,
      messages,
    });

    const tokensOut = resp.usage.output_tokens;
    console.log(`[iter ${iteration}] stop=${resp.stop_reason} | output=${tokensOut}t`);

    if (resp.stop_reason === "end_turn") {
      return resp.content
        .filter((b) => b.type === "text")
        .map((b) => (b as Anthropic.Messages.TextBlock).text)
        .join("");
    }

    if (dr.check(tokensOut)) {
      console.log(`  [ralph] ${DIMINISHING_RETURNS_ITERS} iters no productivas → stop`);
      return resp.content
        .filter((b) => b.type === "text")
        .map((b) => (b as Anthropic.Messages.TextBlock).text)
        .join("") || "[loop detenido]";
    }

    if (resp.stop_reason === "tool_use") {
      const results: Anthropic.Messages.ToolResultBlockParam[] = [];
      for (const b of resp.content) {
        if (b.type === "tool_use") {
          const spec = toolMap.get(b.name);
          const [r] = spec
            ? ejecutarConPermisos(spec, b.input as Record<string, unknown>, permissionLevel)
            : [`[tool '${b.name}' no registrada]`, false];
          console.log(`  ${b.name}(${JSON.stringify(b.input)}) → ${r.slice(0, 60)}`);
          results.push({ type: "tool_result", tool_use_id: b.id, content: r });
        }
      }
      messages.push({ role: "assistant", content: resp.content });
      messages.push({ role: "user", content: results });
    }
  }
}

// Demo
(async () => {
  const client = new Anthropic();

  const tools: ToolSpec[] = [
    {
      name: "calcular",
      fn: ({ expresion }) => {
        // eslint-disable-next-line no-new-func
        return String(new Function(`return ${expresion}`)());
      },
      permission: PermissionLevel.READ_ONLY,
      description: "Evalúa una expresión matemática.",
      inputSchema: {
        type: "object",
        properties: { expresion: { type: "string" } },
        required: ["expresion"],
      },
    },
    {
      name: "obtener_fecha",
      fn: () => new Date().toISOString().split("T")[0],
      permission: PermissionLevel.READ_ONLY,
      description: "Devuelve la fecha actual en formato ISO.",
      inputSchema: { type: "object", properties: {} },
    },
  ];

  const resultado = await ralphLoop(
    "¿Cuántos días han pasado desde el 1 de enero de 2025 hasta hoy?",
    tools,
    client,
    "Eres un asistente útil.",
    PermissionLevel.READ_ONLY
  );
  console.log(`\nRespuesta: ${resultado}`);
})();
