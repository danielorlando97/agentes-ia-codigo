"""
Visualizacion BPE — como un texto se divide en tokens con colores.

Qué demuestra:
    El algoritmo BPE (Byte Pair Encoding) divide texto en subpalabras.
    Este script visualiza graficamente esa division con colores de terminal,
    mostrando los casos contraintuitivos que todo desarrollador debe conocer:
    - " world" (con espacio) es un token distinto a "world" (sin espacio)
    - "677" se divide en ["6", "77"] — dos tokens, no uno
    - "2025" → ["20", "25"] — años comunes son dos tokens
    - Los emojis pueden ser 2-4 tokens segun su complejidad
    - El espanol usa ~1.2-1.5x mas tokens que el ingles en el mismo texto

Por qué importa visualizarlo:
    Los conteos de tokens no son intuitivos. Un error comun es asumir que
    las palabras cortas = 1 token siempre. Esta visualizacion elimina esa
    suposicion con evidencia directa.

Dependencias:
    pip install tiktoken    (colores ANSI funciones en terminal macOS/Linux)

Cómo ejecutar:
    make py SCRIPT=python/03-motor-llm/visualizar-bpe.py

Qué esperar:
    Texto coloreado donde cada color = un token diferente, con IDs y conteos.
    No hace llamadas a la API — todo es procesamiento local con tiktoken.
"""
import tiktoken

COLORES = [
    "\033[41m",   # fondo rojo
    "\033[42m",   # fondo verde
    "\033[43m",   # fondo amarillo
    "\033[44m",   # fondo azul
    "\033[45m",   # fondo magenta
    "\033[46m",   # fondo cyan
]
RESET = "\033[0m"
BOLD  = "\033[1m"


def visualizar(text: str, encoding: str = "cl100k_base") -> None:
    enc = tiktoken.get_encoding(encoding)
    token_ids = enc.encode(text)
    piezas = [enc.decode([t]) for t in token_ids]

    print(f"\n{BOLD}Texto:{RESET} {repr(text)}")
    print(f"{BOLD}Tokenizador:{RESET} {encoding}  |  {len(token_ids)} tokens\n")

    # Colorear cada token alternativamente
    coloreado = ""
    for i, pieza in enumerate(piezas):
        color = COLORES[i % len(COLORES)]
        # Representar el texto de forma visible (espacios con punto medio)
        visible = pieza.replace(" ", "·").replace("\n", "↵")
        coloreado += f"{color}{visible}{RESET}"
    print(f"  {coloreado}\n")

    # Tabla de IDs
    print(f"  {'Pieza':<20} {'ID':>7}  {'Bytes':}")
    print(f"  {'-'*20} {'-'*7}  {'-'*30}")
    for pieza, tid in zip(piezas, token_ids):
        visible = repr(pieza)[1:-1]   # quitar comillas externas
        bytes_hex = " ".join(f"{b:02x}" for b in pieza.encode("utf-8", errors="replace"))
        print(f"  {visible:<20} {tid:>7}  {bytes_hex}")


EJEMPLOS = [
    # Casos básicos
    "Hello, world!",

    # Espacio líder: "world" y " world" son tokens distintos
    "world",
    " world",

    # Números: fragmentación en cl100k (bloques de máx 3 dígitos)
    "127",     # 1 token (frecuente en código)
    "677",     # 2 tokens: "6" + "77"
    "2025",    # 2 tokens: "20" + "25"
    "12345",   # 3 tokens: "123" + "4" + "5" o similar

    # Español (más tokens que inglés para el mismo significado)
    "tokenización",
    "tokenization",

    # JSON vs texto
    '{"nombre": "Juan", "edad": 30}',
    "nombre: Juan, edad: 30",

    # Emoji (multi-token en cl100k)
    "✅",
    "🇪🇸",

    # Contracciones y mayúsculas (bug en GPT-2, corregido en cl100k)
    "how's",
    "HOW'S",
]


if __name__ == "__main__":
    print("=== Visualizador BPE ===")
    print("Cada color representa un token distinto. '·' = espacio, '↵' = salto de línea.\n")

    enc_cl100k = "cl100k_base"
    enc_o200k  = "o200k_base"

    for ejemplo in EJEMPLOS:
        visualizar(ejemplo, enc_cl100k)

    # Comparación cl100k vs o200k para texto hindi
    hindi = "आज मौसम धूपदार और गर्म है।"
    print(f"\n{'='*60}")
    print(f"Comparación cl100k vs o200k para Hindi:")
    visualizar(hindi, enc_cl100k)
    visualizar(hindi, enc_o200k)
