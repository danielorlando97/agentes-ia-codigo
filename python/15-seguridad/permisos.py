# Permisos y capabilities: ToolRegistry con allow/deny lists, scope validation, RBAC
#
# Cómo ejecutar:
#   make py SCRIPT=python/15-seguridad/permisos.py
#
# Qué esperar:
#   ToolRegistry con allow/deny lists y RBAC. El agente no puede llamar
#   herramientas fuera de su scope o rol. Muestra intentos bloqueados.
#
# Variables de entorno:
#   MODEL — modelo a usar (default: claude-sonnet-4-6)

import os
from dataclasses import dataclass, field
from typing import Any, Callable, Optional
import anthropic

cliente = anthropic.Anthropic()


# ─── Modelo de herramienta con scope ─────────────────────────────────────────

@dataclass
class Herramienta:
    nombre: str
    descripcion: str
    schema: dict                      # JSON schema para el modelo
    funcion: Optional[Callable] = None
    requiere_aprobacion: bool = False
    scope: dict = field(default_factory=dict)


@dataclass
class ContextoAgente:
    usuario_id: str
    rol: str
    herramientas_autorizadas: set[str]
    directorios_permitidos: list[str] = field(default_factory=list)
    max_descuento: float = 0.0


# ─── Tool Registry ────────────────────────────────────────────────────────────

class ToolRegistry:
    def __init__(self) -> None:
        self._herramientas: dict[str, Herramienta] = {}
        self._deny_always: set[str] = set()

    def registrar(self, herramienta: Herramienta) -> None:
        self._herramientas[herramienta.nombre] = herramienta

    def denegar_siempre(self, *nombres: str) -> None:
        self._deny_always.update(nombres)

    def herramientas_para_contexto(self, ctx: ContextoAgente) -> list[dict]:
        """Devuelve el JSON schema de las herramientas visibles para el modelo."""
        visibles = []
        for nombre, h in self._herramientas.items():
            if nombre in self._deny_always:
                continue
            if nombre not in ctx.herramientas_autorizadas:
                continue
            visibles.append({
                "name": h.nombre,
                "description": h.descripcion,
                "input_schema": h.schema,
            })
        return visibles

    def ejecutar(self, nombre: str, params: dict, ctx: ContextoAgente) -> Any:
        # Deny list: siempre bloqueado
        if nombre in self._deny_always:
            raise PermissionError(f"'{nombre}' bloqueado permanentemente (deny list)")

        # Allow list: solo herramientas autorizadas para este contexto
        if nombre not in ctx.herramientas_autorizadas:
            raise PermissionError(f"'{nombre}' no autorizado para rol '{ctx.rol}'")

        herramienta = self._herramientas.get(nombre)
        if herramienta is None:
            raise ValueError(f"Herramienta '{nombre}' no registrada")

        # Validación de scope
        self._validar_scope(herramienta, params, ctx)

        if herramienta.funcion is None:
            return f"[SIMULADO] {nombre}({params})"

        return herramienta.funcion(**params)

    def _validar_scope(self, h: Herramienta, params: dict, ctx: ContextoAgente) -> None:
        # Validación de path traversal para operaciones de archivo
        if h.nombre in ("leer_archivo", "escribir_archivo"):
            ruta = params.get("path", "")
            if ".." in ruta:
                raise PermissionError(f"Path traversal detectado: '{ruta}'")
            if ctx.directorios_permitidos and not any(
                os.path.normpath(ruta).startswith(os.path.normpath(d))
                for d in ctx.directorios_permitidos
            ):
                raise PermissionError(f"Ruta '{ruta}' fuera del scope autorizado")

        # Validación de límites numéricos
        if h.nombre == "aplicar_descuento":
            porcentaje = params.get("porcentaje", 0)
            if porcentaje > ctx.max_descuento:
                raise PermissionError(
                    f"Descuento {porcentaje}% supera el límite para rol '{ctx.rol}' ({ctx.max_descuento}%)"
                )

        # Solo permite operar sobre el usuario de la sesión
        if "solo_usuario_actual" in h.scope and h.scope["solo_usuario_actual"]:
            usuario_params = params.get("usuario_id", ctx.usuario_id)
            if usuario_params != ctx.usuario_id:
                raise PermissionError(
                    f"'{h.nombre}' solo puede operar sobre el usuario de la sesión"
                )


# ─── RBAC: permisos por rol ───────────────────────────────────────────────────

PERMISOS_POR_ROL: dict[str, dict] = {
    "soporte_basico": {
        "allow": {"obtener_info_usuario", "estado_pedido", "crear_ticket"},
        "max_descuento": 0.0,
    },
    "soporte_premium": {
        "allow": {"obtener_info_usuario", "estado_pedido", "crear_ticket", "aplicar_descuento"},
        "max_descuento": 20.0,
    },
    "soporte_manager": {
        "allow": {"obtener_info_usuario", "estado_pedido", "crear_ticket", "aplicar_descuento", "modificar_usuario"},
        "max_descuento": 50.0,
    },
}

DENY_ALWAYS = {"borrar_usuario", "acceso_admin", "exportar_todos_usuarios"}


def construir_contexto(usuario_id: str, rol: str) -> ContextoAgente:
    permisos = PERMISOS_POR_ROL.get(rol, {"allow": set(), "max_descuento": 0.0})
    return ContextoAgente(
        usuario_id=usuario_id,
        rol=rol,
        herramientas_autorizadas=permisos["allow"],
        directorios_permitidos=[f"/data/{usuario_id}/"],
        max_descuento=permisos["max_descuento"],
    )


# ─── Agente con ToolRegistry ──────────────────────────────────────────────────

def construir_registry() -> ToolRegistry:
    registry = ToolRegistry()
    registry.denegar_siempre(*DENY_ALWAYS)

    herramientas = [
        Herramienta(
            nombre="obtener_info_usuario",
            descripcion="Obtiene información del usuario de la sesión.",
            schema={"type": "object", "properties": {"usuario_id": {"type": "string"}}, "required": ["usuario_id"]},
            scope={"solo_usuario_actual": True},
        ),
        Herramienta(
            nombre="estado_pedido",
            descripcion="Obtiene el estado de un pedido.",
            schema={"type": "object", "properties": {"pedido_id": {"type": "string"}}, "required": ["pedido_id"]},
        ),
        Herramienta(
            nombre="crear_ticket",
            descripcion="Crea un ticket de soporte.",
            schema={"type": "object", "properties": {"descripcion": {"type": "string"}}, "required": ["descripcion"]},
        ),
        Herramienta(
            nombre="aplicar_descuento",
            descripcion="Aplica un descuento a un pedido (requiere rol premium o superior).",
            schema={"type": "object", "properties": {"pedido_id": {"type": "string"}, "porcentaje": {"type": "number"}}, "required": ["pedido_id", "porcentaje"]},
        ),
        Herramienta(
            nombre="modificar_usuario",
            descripcion="Modifica datos del usuario (solo managers).",
            schema={"type": "object", "properties": {"usuario_id": {"type": "string"}, "campo": {"type": "string"}, "valor": {"type": "string"}}, "required": ["usuario_id", "campo", "valor"]},
        ),
    ]
    for h in herramientas:
        registry.registrar(h)

    return registry


def agente_con_permisos(tarea: str, usuario_id: str, rol: str) -> str:
    registry = construir_registry()
    ctx = construir_contexto(usuario_id, rol)
    herramientas_visibles = registry.herramientas_para_contexto(ctx)

    mensajes = [{"role": "user", "content": tarea}]

    for _ in range(10):
        respuesta = cliente.messages.create(
            model=os.environ.get("MODEL", "claude-sonnet-4-6"),
            max_tokens=512,
            tools=herramientas_visibles,
            messages=mensajes,
        )
        mensajes.append({"role": "assistant", "content": respuesta.content})

        if respuesta.stop_reason == "end_turn":
            return next((b.text for b in respuesta.content if hasattr(b, "text")), "")

        if respuesta.stop_reason == "tool_use":
            tool_results = []
            for bloque in respuesta.content:
                if bloque.type != "tool_use":
                    continue
                try:
                    resultado = registry.ejecutar(bloque.name, bloque.input, ctx)
                    contenido = str(resultado)
                except PermissionError as e:
                    contenido = f"Error de permisos: {e}"
                    print(f"[PERM DENIED] {bloque.name}: {e}")

                tool_results.append({
                    "type": "tool_result",
                    "tool_use_id": bloque.id,
                    "content": contenido,
                })
            mensajes.append({"role": "user", "content": tool_results})

    return "[max iteraciones]"


if __name__ == "__main__":
    print("=== Allow/Deny list ===")
    registry = construir_registry()
    ctx_basico = construir_contexto("user_123", "soporte_basico")

    herramientas = registry.herramientas_para_contexto(ctx_basico)
    print(f"Herramientas visibles para soporte_basico: {[h['name'] for h in herramientas]}")

    print("\n=== Validación de scope — intento de exceder descuento ===")
    ctx_premium = construir_contexto("user_123", "soporte_premium")
    try:
        registry.ejecutar("aplicar_descuento", {"pedido_id": "P001", "porcentaje": 80}, ctx_premium)
    except PermissionError as e:
        print(f"Bloqueado: {e}")

    print("\n=== Agente soporte_basico (no puede aplicar descuento) ===")
    resultado = agente_con_permisos(
        "Obtén mi información y aplica un descuento del 15% al pedido P001.",
        usuario_id="user_123",
        rol="soporte_basico",
    )
    print(f"Respuesta: {resultado[:300]}")
