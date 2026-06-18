# Implementaciones de terceros: mem0 y Letta

Los archivos Python `mem0_integration.py` y `letta_integration.py` dependen de SDKs específicos de Python (`mem0ai`, `letta-client`) que no tienen equivalente directo en Go.

## Alternativa Go

Ambas plataformas exponen APIs REST. Para integrarlas desde Go, usa `net/http`:

```go
package main

import (
    "bytes"
    "encoding/json"
    "fmt"
    "net/http"
    "os"
)

func mem0Add(messages []map[string]string, userID string) error {
    body, _ := json.Marshal(map[string]interface{}{
        "messages": messages,
        "user_id":  userID,
    })
    req, _ := http.NewRequest("POST", "https://api.mem0.ai/v1/memories/", bytes.NewReader(body))
    req.Header.Set("Authorization", "Token "+os.Getenv("MEM0_API_KEY"))
    req.Header.Set("Content-Type", "application/json")
    _, err := http.DefaultClient.Do(req)
    return err
}

func mem0Search(query, userID string) (*http.Response, error) {
    url := fmt.Sprintf("https://api.mem0.ai/v1/memories/search/?query=%s&user_id=%s", query, userID)
    req, _ := http.NewRequest("GET", url, nil)
    req.Header.Set("Authorization", "Token "+os.Getenv("MEM0_API_KEY"))
    return http.DefaultClient.Do(req)
}
```

Para casos de uso que no requieran infraestructura externa, los ejemplos en `../03-episodica/`, `../04-semantica/` y `../05-procedural/` implementan los mismos patrones de memoria de forma autónoma.
