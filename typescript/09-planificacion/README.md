# Capítulo 9 — Planificación

Ejemplos en TypeScript para el capítulo sobre descomposición de tareas y ejecución de DAGs.

## Ejecutar

Desde `code/` (el Makefile carga `.env` automáticamente):

```bash
make ts SCRIPT=typescript/09-planificacion/descomposicion.ts
make ts SCRIPT=typescript/09-planificacion/llm_compiler.ts
make ts SCRIPT=typescript/09-planificacion/replanificacion.ts
make ts SCRIPT=typescript/09-planificacion/supervisor_worker.ts
```

Sin Makefile:
```bash
set -a && source .env && set +a
bun typescript/09-planificacion/descomposicion.ts
```

> Configura tu entorno primero: ver [`SETUP.md`](../../SETUP.md)
