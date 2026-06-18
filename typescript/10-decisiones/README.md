# Capítulo 10 — Toma de decisiones

Ejemplos en TypeScript para routing, selección de herramientas, confianza y abstención.

## Ejecutar

Desde `code/` (el Makefile carga `.env` automáticamente):

```bash
make ts SCRIPT=typescript/10-decisiones/routing.ts
make ts SCRIPT=typescript/10-decisiones/seleccion_herramientas.ts
make ts SCRIPT=typescript/10-decisiones/confianza.ts
make ts SCRIPT=typescript/10-decisiones/abstencion.ts
```

Sin Makefile:
```bash
set -a && source .env && set +a
bun typescript/10-decisiones/routing.ts
```

> Configura tu entorno primero: ver [`SETUP.md`](../../SETUP.md)
