# Capítulo 9 — Planificación

Ejemplos en Python para el capítulo sobre descomposición de tareas y ejecución de DAGs.

## Ejecutar

Desde `code/` (el Makefile carga `.env` automáticamente):

```bash
make py SCRIPT=python/09-planificacion/descomposicion.py
make py SCRIPT=python/09-planificacion/llm_compiler.py
make py SCRIPT=python/09-planificacion/replanificacion.py
make py SCRIPT=python/09-planificacion/supervisor_worker.py
```

Mini-proyecto (sin API):
```bash
python python/09-planificacion/mini-plan-falla.py
```

Sin Makefile:
```bash
set -a && source .env && set +a
python python/09-planificacion/descomposicion.py
```

> Configura tu entorno primero: ver [`SETUP.md`](../../SETUP.md)
