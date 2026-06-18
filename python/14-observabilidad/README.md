# Capítulo 14 — Observabilidad

Ejemplos en Python para el capítulo sobre observabilidad y evaluación de agentes.

## Archivos

| Archivo | Concepto |
|---|---|
| `tracing.py` | Tracing manual con spans por llamada LLM y por tool call |
| `logs.py` | Logging estructurado JSON con correlation IDs |
| `golden_sets.py` | Runner de golden set con criterios múltiples y pesos |
| `simulacion.py` | Simulación de usuario: agente evaluado vs simulador, juez LLM |
| `trajectory.py` | Evaluación de trayectoria: precision, recall, step efficiency |
| `metricas.py` | Métricas agregadas: task completion rate, P50/P95, cost per task |

## Ejecutar

Desde `code/` (el Makefile carga `.env` automáticamente):

```bash
make py SCRIPT=python/14-observabilidad/tracing.py
make py SCRIPT=python/14-observabilidad/logs.py
make py SCRIPT=python/14-observabilidad/golden_sets.py
make py SCRIPT=python/14-observabilidad/simulacion.py
make py SCRIPT=python/14-observabilidad/trajectory.py
make py SCRIPT=python/14-observabilidad/metricas.py
```

Mini-proyecto (sin API):
```bash
python python/14-observabilidad/mini-postmortem.py
```

Sin Makefile:
```bash
set -a && source .env && set +a
python python/14-observabilidad/tracing.py
```

> Configura tu entorno primero: ver [`SETUP.md`](../../SETUP.md)

## Dependencias

```bash
pip install anthropic
```
