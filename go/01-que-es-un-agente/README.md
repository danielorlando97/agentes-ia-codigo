# Capítulo 1 — ¿Qué es un agente IA?

Ejemplos en Go para el capítulo sobre la definición operativa de agente.

Sin SDK: llamadas HTTP directas a la API de Anthropic. Mantiene la dependencia
externa en cero y evita el drift de versión del SDK oficial.

## Archivos

| Archivo | Nivel smolagents | Qué demuestra |
|---|---|---|
| `agente-minimo.go` | ★★☆ multi-step | Loop canónico LLM + tools hasta `end_turn`. |
| `agente-router.go` | ★☆☆ router | LLM clasifica un input en una de N rutas. Sin loop, sin tools. |
| `agente-react.go` | ★★☆ multi-step | Variante del loop con `Thought:` explícito antes de cada `Action`. |
| `chatbot.go` | Chatbot | Conversación turn-by-turn con memoria de sesión. Sin tools. |
| `copiloto.go` | Copiloto | Sugerencia inline disparada por evento. Sin loop, sin estado. |
| `clasificador-nivel.go` | (no es agente) | Test de localización: dado un set de features, devuelve el nivel del espectro. |
| `espectro-autonomia.go` | (mini-proyecto) | Explora las dos perillas (agencia, modalidad) y observa el cambio en comportamiento. No usa API. |

## Ejecutar

Desde `code/` (el Makefile carga `.env` automáticamente):

```bash
make go FILE=go/01-que-es-un-agente/agente-minimo.go
make go FILE=go/01-que-es-un-agente/chatbot.go
make go FILE=go/01-que-es-un-agente/agente-router.go
make go FILE=go/01-que-es-un-agente/agente-react.go
make go FILE=go/01-que-es-un-agente/copiloto.go
```

Sin Makefile:
```bash
set -a && source .env && set +a
go run go/01-que-es-un-agente/agente-minimo.go
```

Scripts offline (no necesitan API):
```bash
go run go/01-que-es-un-agente/clasificador-nivel.go
go run go/01-que-es-un-agente/espectro-autonomia.go
```

> Cada archivo es un `package main` independiente — usa `go run <archivo>.go`, no `go build .`
>
> Configura tu entorno primero: ver [`SETUP.md`](../../SETUP.md)
