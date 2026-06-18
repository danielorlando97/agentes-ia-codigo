// Cómo ejecutar: make ts SCRIPT=typescript/07-estado-contexto/context_template.ts
import Anthropic from "@anthropic-ai/sdk";

enum TrustLevel {
  HIGH = "high",
  MEDIUM = "medium",
  LOW = "low",
}

class SlotBudgetError extends Error {
  slot: string;
  actual: number;
  budget: number;

  constructor(slot: string, actual: number, budget: number) {
    super(`Slot '${slot}': ${actual}t > budget ${budget}t`);
    this.slot = slot;
    this.actual = actual;
    this.budget = budget;
  }
}

interface SlotDef {
  name: string;
  budget: number;
  trust: TrustLevel;
  required: boolean;
}

function tokensText(text: string): number {
  return Buffer.byteLength(text, "utf8") / 4;
}

function tokensList(obj: unknown[]): number {
  return JSON.stringify(obj).length / 4;
}

class ContextTemplate {
  total: number;
  slots: SlotDef[];

  constructor(total = 128_000) {
    this.total = total;
    this.slots = [
      { name: "system",      budget: 4_000, trust: TrustLevel.HIGH,   required: true },
      { name: "constraints", budget: 2_000, trust: TrustLevel.HIGH,   required: false },
      { name: "retrieved",   budget: 3_000, trust: TrustLevel.MEDIUM, required: false },
      { name: "tools",       budget: 2_000, trust: TrustLevel.HIGH,   required: false },
      { name: "response",    budget: 8_000, trust: TrustLevel.HIGH,   required: false },
    ];
  }

  getSlot(name: string): SlotDef | undefined {
    return this.slots.find((s) => s.name === name);
  }

  get historyBudget(): number {
    return this.total - this.slots.reduce((sum, s) => sum + s.budget, 0);
  }
}

class ContextValidator {
  template: ContextTemplate;

  constructor(template: ContextTemplate) {
    this.template = template;
  }

  validate(assembled: Record<string, unknown>): string[] {
    const errors: string[] = [];
    for (const slot of this.template.slots) {
      const content = assembled[slot.name];
      if (!(slot.name in assembled)) {
        if (slot.required) {
          errors.push(`Slot requerido '${slot.name}' está vacío`);
        }
        continue;
      }
      if (content && typeof content === "string") {
        const actual = tokensText(content);
        if (actual > slot.budget) {
          errors.push(`Slot '${slot.name}': ${actual}t > budget ${slot.budget}t`);
        }
      }
    }
    return errors;
  }
}

class ContextAssembler {
  template: ContextTemplate;
  validator: ContextValidator;

  constructor(template?: ContextTemplate) {
    this.template = template ?? new ContextTemplate();
    this.validator = new ContextValidator(this.template);
  }

  private clip(text: string, budget: number): string {
    const maxChars = budget * 4;
    return text.length > maxChars ? text.slice(0, maxChars) : text;
  }

  private wrapUntrusted(content: string, label: string): string {
    const upper = label.toUpperCase();
    return `[${upper}]\n${content}\n[/${upper}]`;
  }

  assemble(opts: {
    system: string;
    history: object[];
    retrieved?: string;
    constraints?: string;
    tools?: object[];
    strict?: boolean;
  }): Record<string, unknown> {
    const { system, history, retrieved = "", constraints = "", tools = [], strict = true } = opts;
    const result: Record<string, unknown> = {};

    const sysSlot = this.template.getSlot("system")!;
    const sysTokens = tokensText(system);
    if (strict && sysTokens > sysSlot.budget) {
      throw new SlotBudgetError("system", sysTokens, sysSlot.budget);
    }
    result["system"] = this.clip(system, sysSlot.budget);

    if (constraints) {
      const cSlot = this.template.getSlot("constraints")!;
      result["constraints"] = this.clip(constraints, cSlot.budget);
    }

    if (retrieved) {
      const rSlot = this.template.getSlot("retrieved")!;
      const wrapped = this.wrapUntrusted(retrieved, "retrieved");
      result["retrieved"] = this.clip(wrapped, rSlot.budget);
    }

    result["tools"] = tools;
    result["messages"] = history;
    return result;
  }

  validate(assembled: Record<string, unknown>): string[] {
    return this.validator.validate(assembled);
  }
}

function progressiveToolLoading(
  allTools: Record<string, unknown>[],
  budget: number,
  priorityField = "priority"
): Record<string, unknown>[] {
  const sorted = [...allTools].sort(
    (a, b) => ((a[priorityField] as number) ?? 99) - ((b[priorityField] as number) ?? 99)
  );
  const selected: Record<string, unknown>[] = [];
  let used = 0;
  for (const tool of sorted) {
    const cost = tokensList([tool]);
    if (used + cost > budget) break;
    selected.push(tool);
    used += cost;
  }
  return selected;
}

const template = new ContextTemplate(32_000);
const assembler = new ContextAssembler(template);

const tools = [
  { name: "read_file",  description: "Lee un archivo",         priority: 1 },
  { name: "write_file", description: "Escribe un archivo",     priority: 2 },
  { name: "run_tests",  description: "Ejecuta los tests",      priority: 3 },
  { name: "deploy",     description: "Despliega el servicio",  priority: 4 },
];

const selectedTools = progressiveToolLoading(tools, 500);
console.log(`Tools seleccionadas con budget=500t: ${selectedTools.map((t) => t["name"])}`);

const ctx = assembler.assemble({
  system: "Eres un asistente de código experto en Python.",
  history: [{ role: "user", content: "Analiza este repositorio." }],
  retrieved: "Sesión anterior: el usuario trabaja en un proyecto de facturación.",
  constraints: "No modificar archivos de test. Penalización máxima 15%.",
  tools: selectedTools,
  strict: false,
});

const errors = assembler.validate(ctx);
console.log(`Errores de validación: ${errors.length === 0 ? "ninguno" : errors}`);
console.log(`Slots ensamblados: ${Object.keys(ctx)}`);
console.log(`Budget de historial disponible: ${template.historyBudget}t`);

try {
  assembler.assemble({
    system: "X".repeat(20_000),
    history: [],
    strict: true,
  });
} catch (e) {
  if (e instanceof SlotBudgetError) {
    console.log(`\nSlotBudgetError capturado: ${e.message}`);
  }
}
