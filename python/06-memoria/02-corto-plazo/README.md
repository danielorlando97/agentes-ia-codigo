# Memoria de corto plazo — versiones por nivel

## Invariante

Un buffer conversacional que mantiene el historial del agente dentro de un presupuesto de tokens, evictando mensajes en FIFO (oldest-first) cuando se supera el umbral, y preservando siempre los mensajes marcados como `pinned`.

## Niveles

| Nivel | Archivo | Líneas | Añade respecto al anterior |
|---|---|---|---|
| 1 | `nivel-1-minimo.py` | ~20 | — (ventana deslizante por conteo de turns) |
| 2 | `nivel-2-basico.py` | ~40 | estimación de tokens, mensajes anclados (`pinned`) |
| 3 | `nivel-3-produccion.py` | ~85 | `ContextBudget` con 5 regiones, `clip_text`, umbral configurable, logging |
| 4 | `nivel-4-completo.py` | ~155 | `ContextManager`, conteo exacto via API, compactación LLM como fallback, métricas |

## Ejecutar

Desde `code/` (el Makefile carga `.env` automáticamente):

```bash
# Niveles 1–3: sin API (solo stdlib Python)
make py SCRIPT=python/06-memoria/02-corto-plazo/nivel-1-minimo.py
make py SCRIPT=python/06-memoria/02-corto-plazo/nivel-2-basico.py
make py SCRIPT=python/06-memoria/02-corto-plazo/nivel-3-produccion.py

# Nivel 4: requiere ANTHROPIC_API_KEY en .env
make py SCRIPT=python/06-memoria/02-corto-plazo/nivel-4-completo.py
```

Sin Makefile:
```bash
python python/06-memoria/02-corto-plazo/nivel-1-minimo.py   # offline
set -a && source .env && set +a
python python/06-memoria/02-corto-plazo/nivel-4-completo.py
```

> Configura tu entorno primero: ver [`SETUP.md`](../../../SETUP.md)

## Cuándo usar cada nivel

- **Nivel 1**: aprender el mecanismo, demos, conversaciones cortas donde el conteo de turns es suficiente
- **Nivel 2**: scripts internos, prototipos que necesitan presupuesto de tokens y mensajes anclados
- **Nivel 3**: endpoint interno, equipo pequeño; el presupuesto por región y el umbral configurable son los parámetros que más impactan en producción
- **Nivel 4**: sistema con SLA, sesiones largas, múltiples usuarios; la compactación LLM preserva semántica cuando FIFO no es suficiente

## Conceptos que ilustran

| Nivel | Técnica principal |
|---|---|
| 1 | Ventana deslizante (sliding window) |
| 2 | Mensajes anclados (pinned messages) |
| 3 | Presupuesto de contexto (context budget) |
| 4 | Compactación por resumen (LLM-based compaction) |
