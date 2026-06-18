# Capítulo 11 — RAG dentro de un agente

Ejemplos en Python.

## Ejecutar

Desde `code/` (el Makefile carga `.env` automáticamente):

```bash
make py SCRIPT=python/11-rag/rag_ingenuo.py
make py SCRIPT=python/11-rag/retrieval_herramienta.py
```

Script offline (no necesita API, usa TF-IDF/BM25):
```bash
python python/11-rag/mini-rag-lab.py
```

Sin Makefile:
```bash
set -a && source .env && set +a
python python/11-rag/rag_ingenuo.py
```

> Configura tu entorno primero: ver [`SETUP.md`](../../SETUP.md)
