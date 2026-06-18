# Capítulo 10 — Toma de decisiones

Ejemplos en Python para routing, selección de herramientas, confianza y abstención.

## Ejecutar

Desde `code/` (el Makefile carga `.env` automáticamente):

```bash
make py SCRIPT=python/10-decisiones/routing.py
make py SCRIPT=python/10-decisiones/seleccion_herramientas.py
make py SCRIPT=python/10-decisiones/confianza.py
make py SCRIPT=python/10-decisiones/abstencion.py
```

Mini-proyecto (sin API):
```bash
python python/10-decisiones/mini-router-abstension.py
```

Sin Makefile:
```bash
set -a && source .env && set +a
python python/10-decisiones/routing.py
```

> Configura tu entorno primero: ver [`SETUP.md`](../../SETUP.md)
