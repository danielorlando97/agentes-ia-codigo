# Capítulo 16 — Proyecto integrador

Agente de revisión de código construido en cuatro fases incrementales.

## Invariante

El agente recibe código Python, usa herramientas para analizarlo, y produce una revisión estructurada con hallazgos clasificados por severidad.

## Fases

| Fase | Archivo | Añade |
|---|---|---|
| 1 | `fase1_loop.py` | Loop básico sin herramientas |
| 2 | `fase2_herramientas.py` | 4 herramientas (read, run, search, write) |
| 3 | `fase3_memoria.py` | Memoria episódica SQLite |
| 4 | `fase4_hitl.py` | Checkpoint HITL para críticos |
| Completo | `agente_completo.py` | Pipeline integrado con tracing OTel |

## Ejecutar

Desde `code/` (el Makefile carga `.env` automáticamente):

```bash
# Fases individuales
make py SCRIPT=python/16-proyecto/fase1_loop.py
make py SCRIPT=python/16-proyecto/fase2_herramientas.py
make py SCRIPT=python/16-proyecto/fase3_memoria.py
make py SCRIPT=python/16-proyecto/fase4_hitl.py

# Agente completo (pasa un archivo Python a revisar)
make py SCRIPT=python/16-proyecto/agente_completo.py

# Golden set de evaluación
make py SCRIPT=python/16-proyecto/eval/evaluar.py
```

Sin Makefile:
```bash
set -a && source .env && set +a
python python/16-proyecto/fase1_loop.py
```

> Configura tu entorno primero: ver [`SETUP.md`](../../SETUP.md)

## Dependencias

```bash
pip install anthropic chromadb opentelemetry-sdk opentelemetry-exporter-otlp
```
