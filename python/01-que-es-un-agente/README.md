# Capítulo 1 — ¿Qué es un agente IA?

Ejemplos en Python para el capítulo sobre la definición operativa de agente.

## Archivos

| Archivo | Nivel smolagents | Qué demuestra |
|---|---|---|
| `agente-minimo.py` | ★★☆ multi-step | Loop canónico LLM + tools hasta `end_turn`. |
| `agente-router.py` | ★☆☆ router | LLM clasifica un input en una de N rutas. Sin loop, sin tools. |
| `agente-react.py` | ★★☆ multi-step | Variante del loop con `Thought:` explícito antes de cada `Action`. |
| `chatbot.py` | Chatbot | Conversación turn-by-turn con memoria de sesión. Sin tools. |
| `copiloto.py` | Copiloto | Sugerencia inline disparada por evento. Sin loop, sin estado. |
| `clasificador-nivel.py` | (no es agente) | Test de localización: dado un set de features, devuelve el nivel del espectro. |
| `espectro-autonomia.py` | (mini-proyecto) | Explora las dos perillas (agencia, modalidad) y observa el cambio en comportamiento. No usa API. |

## Ejecutar

Desde `code/` (el Makefile carga `.env` automáticamente):

```bash
make py SCRIPT=python/01-que-es-un-agente/agente-minimo.py
make py SCRIPT=python/01-que-es-un-agente/chatbot.py
make py SCRIPT=python/01-que-es-un-agente/agente-router.py
make py SCRIPT=python/01-que-es-un-agente/agente-react.py
make py SCRIPT=python/01-que-es-un-agente/copiloto.py
```

Sin Makefile:
```bash
set -a && source .env && set +a
python python/01-que-es-un-agente/agente-minimo.py
```

Scripts offline (no necesitan API):
```bash
python python/01-que-es-un-agente/clasificador-nivel.py
python python/01-que-es-un-agente/espectro-autonomia.py
```

> Configura tu entorno primero: ver [`SETUP.md`](../../SETUP.md)
