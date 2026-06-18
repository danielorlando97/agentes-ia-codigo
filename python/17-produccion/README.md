# Capítulo 17 — Producción

Ejemplos en Python para el capítulo sobre patrones de producción.

## Archivos

| Archivo | Concepto |
|---|---|
| `streaming.py` | Stream SSE token a token + eventos de tool calls |
| `caching.py` | Prompt caching, response caching, semantic caching |
| `costos.py` | Presupuesto por tarea, routing de modelos por complejidad |
| `persistencia.py` | Checkpoints en SQLite para reanudar tareas interrumpidas |
| `versionado.py` | Registro de versiones de prompts, A/B testing, rollback |
| `recuperacion.py` | Retry con backoff, circuit breaker, context overflow, JSON inválido |

## Ejecutar

Desde `code/` (el Makefile carga `.env` automáticamente):

```bash
make py SCRIPT=python/17-produccion/streaming.py
make py SCRIPT=python/17-produccion/caching.py
make py SCRIPT=python/17-produccion/costos.py
make py SCRIPT=python/17-produccion/persistencia.py
make py SCRIPT=python/17-produccion/versionado.py
make py SCRIPT=python/17-produccion/recuperacion.py
```

Mini-proyecto (sin API, simula incidentes):
```bash
python python/17-produccion/mini-incident-sim.py
```

Sin Makefile:
```bash
set -a && source .env && set +a
python python/17-produccion/streaming.py
```

> Configura tu entorno primero: ver [`SETUP.md`](../../SETUP.md)

## Dependencias

```bash
pip install anthropic
```
