# Capítulo 16 — Proyecto integrador (TypeScript)

Implementación equivalente al agente de revisión de código en TypeScript.

## Ejecutar

Desde `code/` (el Makefile carga `.env` automáticamente):

```bash
# Fases individuales
make ts SCRIPT=typescript/16-proyecto/fase1_loop.ts
make ts SCRIPT=typescript/16-proyecto/fase2_herramientas.ts
make ts SCRIPT=typescript/16-proyecto/fase3_memoria.ts
make ts SCRIPT=typescript/16-proyecto/fase4_hitl.ts

# Agente completo
make ts SCRIPT=typescript/16-proyecto/agente_completo.ts
```

Sin Makefile:
```bash
set -a && source .env && set +a
bun typescript/16-proyecto/fase1_loop.ts
```

> Configura tu entorno primero: ver [`SETUP.md`](../../SETUP.md)

## Dependencias

```bash
bun add @anthropic-ai/sdk
```
