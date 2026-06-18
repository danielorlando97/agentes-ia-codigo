# Capítulo 11 — RAG dentro de un agente

Ejemplos en TypeScript.

## Ejecutar

Desde `code/` (el Makefile carga `.env` automáticamente):

```bash
make ts SCRIPT=typescript/11-rag/rag_ingenuo.ts
make ts SCRIPT=typescript/11-rag/retrieval_herramienta.ts
```

Mini-proyecto (sin API, pipeline TF-IDF/BM25 offline):
```bash
bun typescript/11-rag/mini-rag-lab.ts
```

Sin Makefile:
```bash
set -a && source .env && set +a
bun typescript/11-rag/rag_ingenuo.ts
```

> Configura tu entorno primero: ver [`SETUP.md`](../../SETUP.md)
