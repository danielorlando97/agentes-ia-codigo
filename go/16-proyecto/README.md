# Capítulo 16 — Proyecto integrador (Go)

Implementación equivalente al agente de revisión de código en Go.

## Ejecutar

Desde `code/` (el Makefile carga `.env` automáticamente):

```bash
# Fases individuales
make go FILE=go/16-proyecto/fase1_loop.go
make go FILE=go/16-proyecto/fase2_herramientas.go
make go FILE=go/16-proyecto/fase3_memoria.go
make go FILE=go/16-proyecto/fase4_hitl.go

# Agente completo
make go FILE=go/16-proyecto/agente_completo.go
```

Sin Makefile:
```bash
set -a && source .env && set +a
go run go/16-proyecto/fase1_loop.go
```

> Configura tu entorno primero: ver [`SETUP.md`](../../SETUP.md)

## Dependencias

Solo stdlib de Go — las llamadas a la API de Anthropic se hacen con `net/http`.
