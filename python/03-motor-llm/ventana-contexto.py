"""
Ventana de contexto — efecto "lost in the middle" y estrategias de recuperacion.

Qué demuestra:
    Los LLMs tienen mejor recall de informacion al inicio y al final del contexto
    que en el medio — el fenomeno "lost in the middle" documentado empiricamente.
    Este script mide el accuracy de recuperacion por posicion y compara el costo
    de dos estrategias:
    1. Full-context: enviar todo el documento (maximo recall, maximo costo)
    2. RAG selectivo: recuperar solo el chunk relevante (peor en casos distribuidos, barato)

Implicacion para agentes:
    Si la informacion critica puede estar en cualquier parte del contexto, RAG
    con una busqueda precisa es mas fiable que enviar todo. Pero si la tarea
    requiere razonamiento sobre el documento completo, full-context es necesario.

Dependencias extra:
    pip install tiktoken    (para el conteo local de tokens)

Cómo ejecutar:
    make py SCRIPT=python/03-motor-llm/ventana-contexto.py

Qué esperar:
    Accuracy por posicion (inicio/medio/final), comparativa de costo en tokens
    y USD, y tabla de savings estimados con RAG vs full-context.

Variables de entorno:
    MODEL        — modelo principal (default: claude-sonnet-4-6)
    SMALL_MODEL  — modelo para las preguntas de recuperacion (default: claude-haiku-4-5-20251001)
"""
import os
import tiktoken

import anthropic

MODEL = os.environ.get("MODEL", "claude-sonnet-4-6")
SMALL_MODEL = os.environ.get("SMALL_MODEL", "claude-haiku-4-5-20251001")

# Precio Haiku 4.5 (USD por millón de tokens, Mayo 2025)
PRECIO_INPUT_POR_MILLON  = 0.80
PRECIO_OUTPUT_POR_MILLON = 4.00


# ─── 1. Construir un contexto largo con hechos clave en posiciones distintas ──

HECHO_INICIO = "CÓDIGO_INICIO: el código de seguridad del servidor es ALFA-7742."
HECHO_MEDIO  = "CÓDIGO_MEDIO: el código de seguridad del servidor es BETA-3319."
HECHO_FINAL  = "CÓDIGO_FINAL: el código de seguridad del servidor es GAMMA-8851."

RELLENO_PARRAFO = (
    "El equipo de infraestructura realizó mantenimiento preventivo en todos los nodos "
    "del clúster. Se actualizaron las dependencias de seguridad y se realizaron pruebas "
    "de carga con resultados satisfactorios. El tiempo de respuesta promedio se mantuvo "
    "por debajo de los 200ms durante toda la ventana de mantenimiento programada. "
    "Los registros de auditoría no mostraron anomalías y el sistema quedó estable. "
)

def construir_contexto(relleno_bloques: int = 40) -> str:
    """Construye un documento con hechos en inicio, medio y final."""
    bloque_relleno = RELLENO_PARRAFO * 5  # ~80 palabras × 5 = ~400 palabras por bloque
    mitad = relleno_bloques // 2

    partes = [
        "=== INFORME DE INFRAESTRUCTURA ===\n\n",
        HECHO_INICIO + "\n\n",
    ]
    for _ in range(mitad):
        partes.append(bloque_relleno + "\n\n")
    partes.append(HECHO_MEDIO + "\n\n")
    for _ in range(mitad):
        partes.append(bloque_relleno + "\n\n")
    partes.append(HECHO_FINAL + "\n\n")
    partes.append("=== FIN DEL INFORME ===\n")

    return "".join(partes)


# ─── 2. Medir accuracy de recuperación por posición ──────────────────────────

PREGUNTAS = {
    "inicio": ("¿Cuál es el CÓDIGO_INICIO mencionado en el informe?", "ALFA-7742"),
    "medio":  ("¿Cuál es el CÓDIGO_MEDIO mencionado en el informe?",  "BETA-3319"),
    "final":  ("¿Cuál es el CÓDIGO_FINAL mencionado en el informe?",  "GAMMA-8851"),
}

def medir_accuracy_por_posicion(
    contexto: str,
    repeticiones: int = 3,
) -> dict[str, float]:
    client = anthropic.Anthropic()
    print("\n[accuracy de recuperación por posición del hecho]")
    print(f"  Repeticiones por posición: {repeticiones}\n")

    resultados: dict[str, float] = {}

    for posicion, (pregunta, respuesta_esperada) in PREGUNTAS.items():
        aciertos = 0
        for _ in range(repeticiones):
            resp = client.messages.create(
                model=SMALL_MODEL,
                max_tokens=64,
                system=(
                    "Responde SOLO con el código pedido, sin explicaciones. "
                    "Si no lo encuentras, responde 'NO_ENCONTRADO'."
                ),
                messages=[
                    {
                        "role": "user",
                        "content": f"{contexto}\n\n---\n{pregunta}",
                    }
                ],
            )
            texto = "".join(b.text for b in resp.content if b.type == "text").strip()
            if respuesta_esperada in texto:
                aciertos += 1

        tasa = aciertos / repeticiones
        resultados[posicion] = tasa
        print(f"  {posicion:6s}  accuracy={tasa:.0%}  (esperado: {respuesta_esperada})")

    return resultados


# ─── 3. Calcular coste: full-context vs RAG selectivo ────────────────────────

def contar_tokens_tiktoken(texto: str) -> int:
    enc = tiktoken.get_encoding("cl100k_base")
    return len(enc.encode(texto))


def calcular_coste_usd(tokens_input: int, tokens_output: int = 50) -> float:
    """Calcula el costo estimado en USD con precios de Haiku 4.5."""
    return (
        tokens_input  / 1_000_000 * PRECIO_INPUT_POR_MILLON
        + tokens_output / 1_000_000 * PRECIO_OUTPUT_POR_MILLON
    )


def comparar_estrategias_contexto(contexto_full: str) -> None:
    """Compara el coste de enviar el contexto completo vs solo el chunk relevante."""
    print("\n[comparación de estrategias de contexto]")

    tokens_full = contar_tokens_tiktoken(contexto_full)

    # Chunk relevante: solo el párrafo que contiene el hecho (simulación de RAG)
    chunk_inicio = f"Sección de inicio del informe:\n{HECHO_INICIO}"
    chunk_medio  = f"Sección intermedia del informe:\n{HECHO_MEDIO}"
    chunk_final  = f"Sección final del informe:\n{HECHO_FINAL}"

    for nombre, chunk in [
        ("inicio (RAG)", chunk_inicio),
        ("medio (RAG)",  chunk_medio),
        ("final (RAG)",  chunk_final),
    ]:
        tokens_chunk = contar_tokens_tiktoken(chunk)
        saving_tokens = tokens_full - tokens_chunk
        saving_pct    = saving_tokens / tokens_full * 100

        coste_full  = calcular_coste_usd(tokens_full)
        coste_chunk = calcular_coste_usd(tokens_chunk)
        ahorro_usd  = coste_full - coste_chunk

        print(f"\n  Recuperar hecho de {nombre}:")
        print(f"    Full-context:  {tokens_full:6d} tokens  ${coste_full:.6f}")
        print(f"    Solo chunk:    {tokens_chunk:6d} tokens  ${coste_chunk:.6f}")
        print(f"    Ahorro:        {saving_tokens:6d} tokens  ({saving_pct:.1f}%)  ${ahorro_usd:.6f}")

    # Proyección a escala: 10.000 requests/día
    requests_dia = 10_000
    coste_full_dia  = calcular_coste_usd(tokens_full) * requests_dia
    coste_rag_dia   = calcular_coste_usd(contar_tokens_tiktoken(chunk_inicio)) * requests_dia
    print(f"\n  Proyección a {requests_dia:,} requests/día:")
    print(f"    Full-context: ${coste_full_dia:.2f}/día  ≈ ${coste_full_dia * 30:.0f}/mes")
    coste_rag_mes = coste_rag_dia * 30
    print(f"    RAG selectivo: ${coste_rag_dia:.2f}/día  ≈ ${coste_rag_mes:.0f}/mes")
    ratio = coste_full_dia / coste_rag_dia if coste_rag_dia > 0 else float("inf")
    print(f"    Full-context cuesta ~{ratio:.0f}x más que RAG selectivo")


# ─── Main ──────────────────────────────────────────────────────────────────

if __name__ == "__main__":
    print("=== Ventana de contexto: lost-in-the-middle y coste de estrategias ===")

    contexto = construir_contexto(relleno_bloques=30)
    tokens_total = contar_tokens_tiktoken(contexto)
    print(f"\nContexto construido: ~{tokens_total} tokens ({len(contexto.split()):,} palabras)")

    medir_accuracy_por_posicion(contexto, repeticiones=3)
    comparar_estrategias_contexto(contexto)
