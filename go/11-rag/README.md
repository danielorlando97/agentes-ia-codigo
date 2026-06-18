# Capítulo 11 — RAG dentro de un agente

Ejemplos en Go.

## Ejecutar

Desde `code/` (el Makefile carga `.env` automáticamente):

```bash
make go FILE=go/11-rag/rag_ingenuo.go
make go FILE=go/11-rag/retrieval_herramienta.go
```

Mini-proyecto (sin API, pipeline TF-IDF/BM25 offline):
```bash
go run go/11-rag/mini-rag-lab.go
```

Sin Makefile:
```bash
set -a && source .env && set +a
go run go/11-rag/rag_ingenuo.go
```

> Configura tu entorno primero: ver [`SETUP.md`](../../SETUP.md)
