"""
Conteo de tokens — tiktoken local y Anthropic count_tokens API.

Qué demuestra:
    Cuatro perspectivas del conteo de tokens que todo desarrollador de agentes
    necesita entender para controlar costos y ventana de contexto:
    1. Tokenizacion BPE con tiktoken (cl100k_base vs o200k_base) — variantes curiosas
    2. Overhead real de un tool schema en Claude via count_tokens API
    3. Tokens por idioma — el espanol usa ~1.2x mas tokens que el ingles
    4. JSON pretty vs JSON compacto vs TSV — diferencias de hasta 30%

Por qué importa el overhead de herramientas:
    Cada herramienta en `tools=[]` añade ~200-400 tokens de input en CADA llamada.
    Con 10 herramientas y 50 iteraciones, eso son potencialmente 200k tokens extra.

Dependencias extra:
    pip install tiktoken    (solo para las demos de tokenizacion local)

Cómo ejecutar:
    make py SCRIPT=python/03-motor-llm/contar-tokens.py

Qué esperar:
    Piezas de tokenizacion, tabla por idioma, tabla por formato, y
    el overhead de herramientas medido con la API real.

Variables de entorno:
    MODEL — usado para la llamada a count_tokens (default: claude-opus-4-7)
"""
import os
import anthropic
import tiktoken

MODEL = os.environ.get("MODEL", "claude-opus-4-7")

# ─── 1. Tiktoken básico ────────────────────────────────────────────────────────

def tokens_tiktoken(text: str, encoding: str = "cl100k_base") -> list[int]:
    enc = tiktoken.get_encoding(encoding)
    return enc.encode(text)

def mostrar_tokens(text: str, encoding: str = "cl100k_base") -> None:
    enc = tiktoken.get_encoding(encoding)
    token_ids = enc.encode(text)
    piezas = [enc.decode([t]) for t in token_ids]
    print(f"\n[{encoding}] '{text}'")
    print(f"  IDs:    {token_ids}")
    print(f"  Piezas: {piezas}")
    print(f"  Total:  {len(token_ids)} tokens")


# ─── 2. Overhead de tool schemas en Claude ────────────────────────────────────

def medir_overhead_herramienta() -> None:
    client = anthropic.Anthropic()

    sin_tool = client.messages.count_tokens(
        model=MODEL,
        system="You are a scientist",
        messages=[{"role": "user", "content": "Hello, Claude"}],
    )

    con_tool = client.messages.count_tokens(
        model=MODEL,
        system="You are a scientist",
        tools=[{
            "name": "get_weather",
            "description": "Get the current weather in a given location",
            "input_schema": {
                "type": "object",
                "properties": {
                    "location": {"type": "string", "description": "City name"}
                },
                "required": ["location"],
            },
        }],
        messages=[{"role": "user", "content": "Hello, Claude"}],
    )

    print(f"\n[overhead de herramienta]")
    print(f"  Sin herramienta:  {sin_tool.input_tokens} tokens")
    print(f"  Con herramienta:  {con_tool.input_tokens} tokens")
    print(f"  Overhead:         {con_tool.input_tokens - sin_tool.input_tokens} tokens/herramienta")


# ─── 3. Comparación por idioma ─────────────────────────────────────────────────

FRASES = {
    "inglés":   "The weather today is sunny and warm. I would like to go to the park.",
    "español":  "El tiempo hoy está soleado y cálido. Me gustaría ir al parque.",
    "hindi":    "आज मौसम धूपदार और गर्म है। मैं पार्क जाना चाहूंगा।",
    "japonés":  "今日の天気は晴れて暖かいです。公園に行きたいです。",
}

def comparar_idiomas_tiktoken(encoding: str = "cl100k_base") -> None:
    enc = tiktoken.get_encoding(encoding)
    print(f"\n[comparación por idioma — {encoding}]")
    base = None
    for idioma, frase in FRASES.items():
        n = len(enc.encode(frase))
        chars = len(frase)
        ratio = f"{n / base:.1f}x" if base else "referencia"
        if base is None:
            base = n
        print(f"  {idioma:10s}: {n:4d} tokens  ({chars:3d} chars, {chars/n:.1f} chars/token)  {ratio}")


# ─── 4. JSON vs TSV para los mismos datos ─────────────────────────────────────

DATOS_JSON = """[
  {"pais": "España", "capital": "Madrid", "poblacion": 47000000},
  {"pais": "México", "capital": "Ciudad de México", "poblacion": 126000000},
  {"pais": "Argentina", "capital": "Buenos Aires", "poblacion": 45000000},
  {"pais": "Colombia", "capital": "Bogotá", "poblacion": 50000000},
  {"pais": "Chile", "capital": "Santiago", "poblacion": 19000000}
]"""

DATOS_TSV = """pais\tcapital\tpoblacion
España\tMadrid\t47000000
México\tCiudad de México\t126000000
Argentina\tBuenos Aires\t45000000
Colombia\tBogotá\t50000000
Chile\tSantiago\t19000000"""

DATOS_JSON_COMPACTO = '[{"p":"España","c":"Madrid","n":47000000},{"p":"México","c":"Ciudad de México","n":126000000},{"p":"Argentina","c":"Buenos Aires","n":45000000},{"p":"Colombia","c":"Bogotá","n":50000000},{"p":"Chile","c":"Santiago","n":19000000}]'

def comparar_formatos() -> None:
    enc = tiktoken.get_encoding("cl100k_base")
    n_json = len(enc.encode(DATOS_JSON))
    n_tsv = len(enc.encode(DATOS_TSV))
    n_compact = len(enc.encode(DATOS_JSON_COMPACTO))
    print("\n[comparación de formatos — mismos datos]")
    print(f"  JSON pretty:     {n_json:3d} tokens  (referencia)")
    print(f"  JSON compacto:   {n_compact:3d} tokens  ({n_compact/n_json:.2f}x del JSON pretty)")
    print(f"  TSV:             {n_tsv:3d} tokens  ({n_tsv/n_json:.2f}x del JSON pretty)")


# ─── Main ──────────────────────────────────────────────────────────────────────

if __name__ == "__main__":
    print("=== Tokenización: demos ===")

    # Tiktoken: casos curiosos
    mostrar_tokens("Hello, world!")
    mostrar_tokens("world")       # sin espacio
    mostrar_tokens(" world")      # con espacio previo — token distinto
    mostrar_tokens("127")         # un token
    mostrar_tokens("677")         # dos tokens (6 + 77)
    mostrar_tokens("2025")        # dos tokens (20 + 25)

    # Comparación cl100k vs o200k en hindi
    comparar_idiomas_tiktoken("cl100k_base")
    comparar_idiomas_tiktoken("o200k_base")

    # JSON vs TSV
    comparar_formatos()

    # Overhead real de herramienta (requiere API key)
    medir_overhead_herramienta()
