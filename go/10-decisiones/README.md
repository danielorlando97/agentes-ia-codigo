# Capítulo 10 — Toma de decisiones

Ejemplos en Go para routing, selección de herramientas, confianza y abstención.

## Ejecutar

Desde `code/` (el Makefile carga `.env` automáticamente):

```bash
make go FILE=go/10-decisiones/routing.go
make go FILE=go/10-decisiones/seleccion_herramientas.go
make go FILE=go/10-decisiones/confianza.go
make go FILE=go/10-decisiones/abstencion.go
```

Sin Makefile:
```bash
set -a && source .env && set +a
go run go/10-decisiones/routing.go
```

> Configura tu entorno primero: ver [`SETUP.md`](../../SETUP.md)
