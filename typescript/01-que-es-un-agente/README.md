# Capítulo 1 — ¿Qué es un agente IA?

Ejemplos en TypeScript para el capítulo sobre la definición operativa de agente.

## Archivos

| Archivo | Nivel smolagents | Qué demuestra |
|---|---|---|
| `agente-minimo.ts` | ★★☆ multi-step | Loop canónico LLM + tools hasta `end_turn`. |
| `agente-router.ts` | ★☆☆ router | LLM clasifica un input en una de N rutas. Sin loop, sin tools. |
| `agente-react.ts` | ★★☆ multi-step | Variante del loop con `Thought:` explícito antes de cada `Action`. |
| `chatbot.ts` | Chatbot | Conversación turn-by-turn con memoria de sesión. Sin tools. |
| `copiloto.ts` | Copiloto | Sugerencia inline disparada por evento. Sin loop, sin estado. |
| `clasificador-nivel.ts` | (no es agente) | Test de localización: dado un set de features, devuelve el nivel del espectro. |
| `espectro-autonomia.ts` | (mini-proyecto) | Explora las dos perillas (agencia, modalidad) y observa el cambio en comportamiento. No usa API. |

## Ejecutar

Desde `code/` (Bun carga `.env` automáticamente con el Makefile):

```bash
make ts SCRIPT=typescript/01-que-es-un-agente/agente-minimo.ts
make ts SCRIPT=typescript/01-que-es-un-agente/chatbot.ts
make ts SCRIPT=typescript/01-que-es-un-agente/agente-router.ts
make ts SCRIPT=typescript/01-que-es-un-agente/agente-react.ts
make ts SCRIPT=typescript/01-que-es-un-agente/copiloto.ts
```

Sin Makefile (Bun carga `.env` automáticamente):
```bash
cd code
bun typescript/01-que-es-un-agente/agente-minimo.ts
```

Scripts offline (no necesitan API):
```bash
bun typescript/01-que-es-un-agente/clasificador-nivel.ts
bun typescript/01-que-es-un-agente/espectro-autonomia.ts
```

> Configura tu entorno primero: ver [`SETUP.md`](../../SETUP.md)
