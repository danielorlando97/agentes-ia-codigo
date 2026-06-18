// Permisos y capabilities: ToolRegistry con allow/deny lists, scope validation, RBAC

// Cómo ejecutar: make ts SCRIPT=typescript/15-seguridad/permisos.ts

import Anthropic from "@anthropic-ai/sdk";
import path from "path";

const cliente = new Anthropic();

// ─── Modelo de herramienta con scope ─────────────────────────────────────────

interface Herramienta {
  nombre: string;
  descripcion: string;
  schema: Record<string, unknown>;
  funcion?: (params: Record<string, unknown>) => unknown;
  requiereAprobacion?: boolean;
  scope?: Record<string, unknown>;
}

interface ContextoAgente {
  usuarioId: string;
  rol: string;
  herramientasAutorizadas: Set<string>;
  directoriosPermitidos: string[];
  maxDescuento: number;
}

// ─── Tool Registry ────────────────────────────────────────────────────────────

class ToolRegistry {
  private herramientas: Map<string, Herramienta> = new Map();
  private denyAlways: Set<string> = new Set();

  registrar(herramienta: Herramienta): void {
    this.herramientas.set(herramienta.nombre, herramienta);
  }

  denegarSiempre(...nombres: string[]): void {
    for (const n of nombres) this.denyAlways.add(n);
  }

  herramientasParaContexto(ctx: ContextoAgente): Anthropic.Tool[] {
    const visibles: Anthropic.Tool[] = [];
    for (const [nombre, h] of this.herramientas) {
      if (this.denyAlways.has(nombre)) continue;
      if (!ctx.herramientasAutorizadas.has(nombre)) continue;
      visibles.push({
        name: h.nombre,
        description: h.descripcion,
        input_schema: h.schema as Anthropic.Tool["input_schema"],
      });
    }
    return visibles;
  }

  ejecutar(nombre: string, params: Record<string, unknown>, ctx: ContextoAgente): unknown {
    if (this.denyAlways.has(nombre)) {
      throw new Error(`PermissionError: '${nombre}' bloqueado permanentemente (deny list)`);
    }
    if (!ctx.herramientasAutorizadas.has(nombre)) {
      throw new Error(`PermissionError: '${nombre}' no autorizado para rol '${ctx.rol}'`);
    }
    const herramienta = this.herramientas.get(nombre);
    if (!herramienta) {
      throw new Error(`ValueError: Herramienta '${nombre}' no registrada`);
    }
    this.validarScope(herramienta, params, ctx);
    if (!herramienta.funcion) {
      return `[SIMULADO] ${nombre}(${JSON.stringify(params)})`;
    }
    return herramienta.funcion(params);
  }

  private validarScope(h: Herramienta, params: Record<string, unknown>, ctx: ContextoAgente): void {
    if (h.nombre === "leer_archivo" || h.nombre === "escribir_archivo") {
      const ruta = (params["path"] as string) ?? "";
      if (ruta.includes("..")) {
        throw new Error(`PermissionError: Path traversal detectado: '${ruta}'`);
      }
      if (ctx.directoriosPermitidos.length > 0) {
        const normalizado = path.normalize(ruta);
        const permitido = ctx.directoriosPermitidos.some((d) =>
          normalizado.startsWith(path.normalize(d))
        );
        if (!permitido) {
          throw new Error(`PermissionError: Ruta '${ruta}' fuera del scope autorizado`);
        }
      }
    }

    if (h.nombre === "aplicar_descuento") {
      const porcentaje = (params["porcentaje"] as number) ?? 0;
      if (porcentaje > ctx.maxDescuento) {
        throw new Error(
          `PermissionError: Descuento ${porcentaje}% supera el límite para rol '${ctx.rol}' (${ctx.maxDescuento}%)`
        );
      }
    }

    if (h.scope && h.scope["solo_usuario_actual"]) {
      const usuarioParams = (params["usuario_id"] as string) ?? ctx.usuarioId;
      if (usuarioParams !== ctx.usuarioId) {
        throw new Error(`PermissionError: '${h.nombre}' solo puede operar sobre el usuario de la sesión`);
      }
    }
  }
}

// ─── RBAC: permisos por rol ───────────────────────────────────────────────────

const PERMISOS_POR_ROL: Record<string, { allow: Set<string>; maxDescuento: number }> = {
  soporte_basico: {
    allow: new Set(["obtener_info_usuario", "estado_pedido", "crear_ticket"]),
    maxDescuento: 0.0,
  },
  soporte_premium: {
    allow: new Set(["obtener_info_usuario", "estado_pedido", "crear_ticket", "aplicar_descuento"]),
    maxDescuento: 20.0,
  },
  soporte_manager: {
    allow: new Set([
      "obtener_info_usuario",
      "estado_pedido",
      "crear_ticket",
      "aplicar_descuento",
      "modificar_usuario",
    ]),
    maxDescuento: 50.0,
  },
};

const DENY_ALWAYS = ["borrar_usuario", "acceso_admin", "exportar_todos_usuarios"];

function construirContexto(usuarioId: string, rol: string): ContextoAgente {
  const permisos = PERMISOS_POR_ROL[rol] ?? { allow: new Set<string>(), maxDescuento: 0.0 };
  return {
    usuarioId,
    rol,
    herramientasAutorizadas: permisos.allow,
    directoriosPermitidos: [`/data/${usuarioId}/`],
    maxDescuento: permisos.maxDescuento,
  };
}

// ─── Agente con ToolRegistry ──────────────────────────────────────────────────

function construirRegistry(): ToolRegistry {
  const registry = new ToolRegistry();
  registry.denegarSiempre(...DENY_ALWAYS);

  const herramientas: Herramienta[] = [
    {
      nombre: "obtener_info_usuario",
      descripcion: "Obtiene información del usuario de la sesión.",
      schema: {
        type: "object",
        properties: { usuario_id: { type: "string" } },
        required: ["usuario_id"],
      },
      scope: { solo_usuario_actual: true },
    },
    {
      nombre: "estado_pedido",
      descripcion: "Obtiene el estado de un pedido.",
      schema: {
        type: "object",
        properties: { pedido_id: { type: "string" } },
        required: ["pedido_id"],
      },
    },
    {
      nombre: "crear_ticket",
      descripcion: "Crea un ticket de soporte.",
      schema: {
        type: "object",
        properties: { descripcion: { type: "string" } },
        required: ["descripcion"],
      },
    },
    {
      nombre: "aplicar_descuento",
      descripcion: "Aplica un descuento a un pedido (requiere rol premium o superior).",
      schema: {
        type: "object",
        properties: {
          pedido_id: { type: "string" },
          porcentaje: { type: "number" },
        },
        required: ["pedido_id", "porcentaje"],
      },
    },
    {
      nombre: "modificar_usuario",
      descripcion: "Modifica datos del usuario (solo managers).",
      schema: {
        type: "object",
        properties: {
          usuario_id: { type: "string" },
          campo: { type: "string" },
          valor: { type: "string" },
        },
        required: ["usuario_id", "campo", "valor"],
      },
    },
  ];

  for (const h of herramientas) registry.registrar(h);
  return registry;
}

async function agenteConPermisos(tarea: string, usuarioId: string, rol: string): Promise<string> {
  const registry = construirRegistry();
  const ctx = construirContexto(usuarioId, rol);
  const herramientasVisibles = registry.herramientasParaContexto(ctx);

  const mensajes: Anthropic.MessageParam[] = [{ role: "user", content: tarea }];

  for (let i = 0; i < 10; i++) {
    const respuesta = await cliente.messages.create({
      model: process.env["MODEL"] ?? "claude-sonnet-4-6",
      max_tokens: 512,
      tools: herramientasVisibles,
      messages: mensajes,
    });

    mensajes.push({ role: "assistant", content: respuesta.content });

    if (respuesta.stop_reason === "end_turn") {
      const textBlock = respuesta.content.find((b) => b.type === "text");
      return textBlock && textBlock.type === "text" ? textBlock.text : "";
    }

    if (respuesta.stop_reason === "tool_use") {
      const toolResults: Anthropic.ToolResultBlockParam[] = [];
      for (const bloque of respuesta.content) {
        if (bloque.type !== "tool_use") continue;
        let contenido: string;
        try {
          const resultado = registry.ejecutar(bloque.name, bloque.input as Record<string, unknown>, ctx);
          contenido = String(resultado);
        } catch (e) {
          contenido = `Error de permisos: ${(e as Error).message}`;
          console.log(`[PERM DENIED] ${bloque.name}: ${(e as Error).message}`);
        }
        toolResults.push({ type: "tool_result", tool_use_id: bloque.id, content: contenido });
      }
      mensajes.push({ role: "user", content: toolResults });
    }
  }

  return "[max iteraciones]";
}

// ─── Main ─────────────────────────────────────────────────────────────────────

async function main() {
  console.log("=== Allow/Deny list ===");
  const registry = construirRegistry();
  const ctxBasico = construirContexto("user_123", "soporte_basico");

  const herramientas = registry.herramientasParaContexto(ctxBasico);
  console.log(`Herramientas visibles para soporte_basico: ${herramientas.map((h) => h.name)}`);

  console.log("\n=== Validación de scope — intento de exceder descuento ===");
  const ctxPremium = construirContexto("user_123", "soporte_premium");
  try {
    registry.ejecutar("aplicar_descuento", { pedido_id: "P001", porcentaje: 80 }, ctxPremium);
  } catch (e) {
    console.log(`Bloqueado: ${(e as Error).message}`);
  }

  console.log("\n=== Agente soporte_basico (no puede aplicar descuento) ===");
  const resultado = await agenteConPermisos(
    "Obtén mi información y aplica un descuento del 15% al pedido P001.",
    "user_123",
    "soporte_basico"
  );
  console.log(`Respuesta: ${resultado.slice(0, 300)}`);
}

main().catch(console.error);
