# Capítulo 13 — HITL (Human-in-the-Loop)

Ejemplos en Python para el capítulo sobre supervisión humana de agentes.

## Archivos

| Archivo | Concepto |
|---|---|
| `approval_flows.py` | Clasificación de acciones por riesgo, gates HITL y HOTL |
| `interrupcion.py` | Checkpointing de estado para interrupción/reanudación asíncrona |

## Ejecutar

Desde `code/` (el Makefile carga `.env` automáticamente):

```bash
make py SCRIPT=python/13-hitl/approval_flows.py
make py SCRIPT=python/13-hitl/interrupcion.py
```

Mini-proyecto (sin API, simula checkpointing offline):
```bash
python python/13-hitl/mini-checkpoint-sim.py
```

Sin Makefile:
```bash
set -a && source .env && set +a
python python/13-hitl/approval_flows.py
```

> Configura tu entorno primero: ver [`SETUP.md`](../../SETUP.md)

## Dependencias

```bash
pip install anthropic
```
