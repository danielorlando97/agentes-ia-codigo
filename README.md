# Agentes IA — Código del libro

![Python](https://img.shields.io/badge/Python-3.11+-3776AB?style=flat&logo=python&logoColor=white)
![TypeScript](https://img.shields.io/badge/TypeScript-5.0+-3178C6?style=flat&logo=typescript&logoColor=white)
![Go](https://img.shields.io/badge/Go-1.21+-00ADD8?style=flat&logo=go&logoColor=white)
![License: MIT](https://img.shields.io/badge/License-MIT-green.svg)

Código ejecutable de los 17 capítulos del libro **[Agentes IA](https://gumroad.com/l/agentes-ia)** — el primer libro técnico en español que desmonta un agente IA pieza a pieza, sin frameworks, con código en tres lenguajes.

---

## El agente más simple que califica como agente

```python
client = anthropic.Anthropic()
messages = [{"role": "user", "content": tarea}]

for _ in range(MAX_ITERATIONS):
    response = client.messages.create(model=MODEL, tools=TOOLS, messages=messages, max_tokens=1024)

    if response.stop_reason in ("end_turn", "stop_sequence"):
        return "".join(b.text for b in response.content if b.type == "text")

    if response.stop_reason == "tool_use":
        tool_results = [
            {"type": "tool_result", "tool_use_id": b.id, "content": execute_tool(b.name, b.input)}
            for b in response.content if b.type == "tool_use"
        ]
        messages.append({"role": "assistant", "content": response.content})
        messages.append({"role": "user", "content": tool_results})
        continue

    break
```

Este es el loop central. El resto del libro —memoria, planificación, multi-agente, evaluación— es expansión de este núcleo.

---

## Qué hay en este repositorio

289 archivos de código que acompañan cada sección del libro. La misma implementación en los tres lenguajes, con las mismas ideas pero aprovechando los idioms de cada uno.

```
python/          TypeScript/      go/
├── 01-que-es-un-agente/
├── 02-anatomia-minima/
├── 03-motor-llm/
├── 04-prompts/
├── 05-herramientas/
├── 06-memoria/
├── 07-estado-contexto/
├── 08-bucle/
├── 09-planificacion/
├── 10-decisiones/
├── 11-rag/
├── 12-multi-agente/
├── 13-hitl/
├── 14-observabilidad/
├── 15-seguridad/
├── 16-proyecto/
└── 17-produccion/
```

---

## Cómo ejecutar

### Requisitos

- Una API key de Anthropic: [console.anthropic.com](https://console.anthropic.com)
- Python 3.11+, Node 18+, o Go 1.21+ (según el lenguaje que quieras usar)

### Setup

```bash
git clone https://github.com/tuusuario/agentes-ia-codigo
cd agentes-ia-codigo
export ANTHROPIC_API_KEY=sk-ant-...
```

**Python:**
```bash
pip install anthropic
python python/01-que-es-un-agente/agente-minimo.py
```

**TypeScript:**
```bash
cd typescript/01-que-es-un-agente
npm install
npx ts-node agente-minimo.ts
```

**Go:**
```bash
cd go/01-que-es-un-agente
go run agente-minimo.go
```

---

## Lo que cubre cada capítulo

| # | Capítulo | Qué implementa |
|---|----------|----------------|
| 01 | ¿Qué es un agente? | El loop mínimo. Router vs agente vs ReAct |
| 02 | Anatomía mínima | Hello agent en 50 líneas. Las 5 zonas de un archivo de agente |
| 03 | El motor: LLMs | Tokenización, sampling, ventana de contexto como recurso |
| 04 | Prompts | Few-shot, chain-of-thought, structured output, role prompting |
| 05 | Herramientas | Tool calling en JSON, XML y ReAct. Paralelo. MCP |
| 06 | Memoria | Buffer, sumarización, vector store, knowledge graph, scratchpad |
| 07 | Estado y contexto | Truncación, compactación, context engineering |
| 08 | El bucle del agente | ReAct, Plan-and-Execute, Tree of Thoughts, Reflexion, Ralph Loop |
| 09 | Planificación | Descomposición de tareas, subagentes, replanificación, LLM Compiler |
| 10 | Toma de decisiones | Routing, selección de tools, confianza, abstención |
| 11 | RAG dentro del agente | GraphRAG, LightRAG, RAG multimodal, Agentic RAG |
| 12 | Multi-agente | Supervisor/Worker, Debate, Equipo de roles, AutoGen vs CrewAI |
| 13 | Human-in-the-loop | Approval flows, interrupción/reanudación, UX |
| 14 | Observabilidad | Tracing, logs estructurados, golden sets, LLM-as-judge, trajectory eval |
| 15 | Seguridad | Prompt injection, jailbreaks, validación de output, sandboxing |
| 16 | Construir desde cero | Agente completo sin frameworks en los 3 lenguajes |
| 17 | Producción | Streaming, caching, costos, persistencia, versionado, recuperación |

---

## El libro

Este código es el acompañante de **Agentes IA**, 246.000 palabras sobre cómo funciona un agente IA por dentro.

El código sin el libro funciona — los archivos tienen comentarios que explican cada decisión. Pero el libro cubre los *por qués*: cuándo falla cada técnica, qué cuesta, qué se pierde, qué variantes existen. Es la diferencia entre saber que el loop existe y entender por qué está diseñado así.

**[Consigue el libro →](https://gumroad.com/l/agentes-ia)**

Lo que incluye:
- PDF + EPUB del libro completo (246K palabras, 17 capítulos)
- Este repositorio de código (ya lo tienes)
- Actualizaciones durante 1 año

---

## Por qué sin frameworks

LangChain, LlamaIndex, AutoGen y CrewAI son útiles. También ocultan exactamente lo que necesitas entender cuando algo falla.

Este código usa la API directamente. `client.messages.create()` en Python y TypeScript. HTTP directo en Go. Cuando el mecanismo es visible, los bugs son debuggeables y las optimizaciones son obvias.

Los frameworks aparecen en el libro como sujetos de estudio: su código fuente, sus decisiones de diseño, sus limitaciones. No como el camino principal.

---

## Licencia

MIT. Úsalo, modifícalo, inclúyelo en tus proyectos.

Si el código te resulta útil, el libro explica por qué funciona.
