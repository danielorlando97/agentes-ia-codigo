# Capítulo 15 — Seguridad

Ejemplos en Python para el capítulo sobre seguridad en agentes.

## Archivos

| Archivo | Concepto |
|---|---|
| `sandboxing.py` | Ejecución aislada de código con timeout, límites de recursos y bloqueo de red |
| `permisos.py` | ToolRegistry con allow/deny lists, scope validation y RBAC |

## Ejecutar

Desde `code/` (el Makefile carga `.env` automáticamente):

```bash
make py SCRIPT=python/15-seguridad/sandboxing.py
make py SCRIPT=python/15-seguridad/permisos.py
make py SCRIPT=python/15-seguridad/validacion.py
```

Sin API (estudio de jailbreaks offline):
```bash
python python/15-seguridad/jailbreaks.py
python python/15-seguridad/mini-injection-lab.py
```

Sin Makefile:
```bash
set -a && source .env && set +a
python python/15-seguridad/sandboxing.py
```

> Configura tu entorno primero: ver [`SETUP.md`](../../SETUP.md)

## Dependencias

```bash
pip install anthropic
```
