# Capítulo 9 — Planificación

Ejemplos en Go para el capítulo sobre descomposición de tareas y ejecución de DAGs.

## Ejecutar

Desde `code/` (el Makefile carga `.env` automáticamente):

```bash
make go FILE=go/09-planificacion/descomposicion.go
make go FILE=go/09-planificacion/llm_compiler.go
make go FILE=go/09-planificacion/replanificacion.go
make go FILE=go/09-planificacion/supervisor_worker.go
```

Sin Makefile:
```bash
set -a && source .env && set +a
go run go/09-planificacion/descomposicion.go
```

> Configura tu entorno primero: ver [`SETUP.md`](../../SETUP.md)
