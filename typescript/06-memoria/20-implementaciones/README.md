# Implementaciones de terceros: mem0 y Letta

Los archivos Python `mem0_integration.py` y `letta_integration.py` dependen de SDKs específicos de Python (`mem0ai`, `letta-client`) que no tienen equivalente directo en TypeScript o Go.

## Alternativa TypeScript/Go

Ambas plataformas exponen APIs REST. Para integrarlas desde TypeScript o Go, usa sus clientes HTTP nativos con los endpoints documentados en:

- **mem0**: https://docs.mem0.ai/api-reference
- **Letta**: https://docs.letta.com/api-reference

El patrón equivalente en TypeScript sería:

```typescript
import Anthropic from "@anthropic-ai/sdk";

// mem0 expone una API REST — usar fetch con tu API key
const mem0Client = {
  add: async (messages: object[], userId: string) => {
    return fetch("https://api.mem0.ai/v1/memories/", {
      method: "POST",
      headers: { Authorization: `Token ${process.env.MEM0_API_KEY}`, "Content-Type": "application/json" },
      body: JSON.stringify({ messages, user_id: userId }),
    });
  },
  search: async (query: string, userId: string) => {
    return fetch(`https://api.mem0.ai/v1/memories/search/?query=${encodeURIComponent(query)}&user_id=${userId}`, {
      headers: { Authorization: `Token ${process.env.MEM0_API_KEY}` },
    });
  },
};
```

Para casos de uso que no requieran infraestructura externa, los ejemplos en `../03-episodica/`, `../04-semantica/` y `../05-procedural/` implementan los mismos patrones de memoria de forma autónoma.
