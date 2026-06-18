"""
Local vs API — benchmark de latencia y costo: cloud vs modelo local.

Qué demuestra:
    Comparacion empirica entre API cloud (Anthropic) y modelo local (Ollama).
    Mide para ambos: TTFT, latencia total, throughput (tokens/s) y costo real.
    Incluye el calculo del break-even: a partir de cuantos requests/mes el
    modelo local es mas barato que la API, considerando el costo del hardware.

Cuando local gana:
    - Volumen alto (>50k requests/mes en GPUs consumer): el costo por llamada
      cae a ~$0.0001 vs ~$0.001-0.01 en cloud
    - Latencia estricta: sin red, TTFT de 200-500ms vs 500ms-2s en cloud
    - Privacidad: los datos no salen del servidor

Cuando cloud gana:
    - Modelos grandes (70B+): requieren hardware caro (2x A100 ~$30k)
    - Volumen bajo (<10k requests/mes): el capex de hardware no se amortiza
    - Mantenimiento cero: sin gestionar actualizaciones, VRAM, escalado

Cómo ejecutar:
    # Solo benchmark cloud (sin Ollama):
    make py SCRIPT=python/03-motor-llm/local-vs-api.py
    # Con Ollama local:
    ollama serve  # en otra terminal
    ollama pull llama3.2:1b
    make py SCRIPT=python/03-motor-llm/local-vs-api.py

Qué esperar:
    Metricas de latencia por llamada, tabla de break-even, comparacion de costos.
    Si Ollama no esta disponible, el benchmark local se omite gracefully.

Variables de entorno:
    MODEL        — modelo cloud a usar (default: claude-sonnet-4-6)
    SMALL_MODEL  — modelo para el benchmark (default: claude-haiku-4-5-20251001)
"""
import os
import time
import urllib.request
import urllib.error
import json

import anthropic

MODEL = os.environ.get("MODEL", "claude-sonnet-4-6")
SMALL_MODEL = os.environ.get("SMALL_MODEL", "claude-haiku-4-5-20251001")

# Precios API Anthropic (USD por millón de tokens, Mayo 2025)
# Si el modelo no está en la tabla (ej: llama3.1 via Ollama/proxy), se usan $0.
PRECIOS_API = {
    "claude-haiku-4-5-20251001": {"input": 0.80, "output": 4.00},
    "claude-sonnet-4-6":         {"input": 3.00, "output": 15.00},
}
PRECIO_DESCONOCIDO = {"input": 0.0, "output": 0.0}  # proxy local / modelo gratuito

# Parámetros de infraestructura local (ajustar según hardware)
COSTO_GPU_HORA_USD      = 0.50   # ejemplo: RTX 4090 en hetzner cloud
HORAS_DIA_ACTIVAS       = 24.0
REQUESTS_HORA_LOCAL     = 120    # throughput estimado en hardware modesto

OLLAMA_URL   = "http://localhost:11434"
OLLAMA_MODEL = os.environ.get("OLLAMA_MODEL", "llama3.1")   # configurable; llama3.1 recomendado con tool use

PROMPT_BENCHMARK = (
    "Explica en exactamente dos oraciones qué es el attention mechanism en transformers."
)


# ─── 1. Benchmark API cloud ──────────────────────────────────────────────────

def benchmark_api_cloud(repeticiones: int = 3) -> dict:
    client = anthropic.Anthropic()
    latencias: list[float] = []
    ttfts: list[float] = []
    tokens_input_total = 0
    tokens_output_total = 0

    print(f"\n[benchmark API cloud — {SMALL_MODEL}]")
    print(f"  Prompt: {PROMPT_BENCHMARK[:60]!r}...\n")

    for i in range(repeticiones):
        t_inicio = time.perf_counter()
        ttft: float | None = None

        # Streaming para medir TTFT real
        with client.messages.stream(
            model=SMALL_MODEL,
            max_tokens=128,
            messages=[{"role": "user", "content": PROMPT_BENCHMARK}],
        ) as stream:
            for text in stream.text_stream:
                if ttft is None:
                    ttft = time.perf_counter() - t_inicio
                _ = text  # consumir el stream

            final_msg = stream.get_final_message()

        t_total = time.perf_counter() - t_inicio
        latencias.append(t_total)
        ttfts.append(ttft or t_total)
        tokens_input_total  += final_msg.usage.input_tokens
        tokens_output_total += final_msg.usage.output_tokens

        print(f"  rep{i+1}: TTFT={ttft:.3f}s  total={t_total:.3f}s  "
              f"in={final_msg.usage.input_tokens}tok  out={final_msg.usage.output_tokens}tok")

    avg_ttft   = sum(ttfts) / len(ttfts)
    avg_total  = sum(latencias) / len(latencias)
    avg_in     = tokens_input_total / repeticiones
    avg_out    = tokens_output_total / repeticiones

    precios = PRECIOS_API.get(SMALL_MODEL, PRECIO_DESCONOCIDO)
    costo_por_call = (
        avg_in  / 1_000_000 * precios["input"]
        + avg_out / 1_000_000 * precios["output"]
    )

    print(f"\n  Promedio: TTFT={avg_ttft:.3f}s  total={avg_total:.3f}s")
    print(f"  Costo por call: ${costo_por_call:.6f}")

    return {
        "tipo": "api_cloud",
        "modelo": SMALL_MODEL,
        "avg_ttft_s": avg_ttft,
        "avg_latencia_s": avg_total,
        "avg_tokens_input": avg_in,
        "avg_tokens_output": avg_out,
        "costo_por_call_usd": costo_por_call,
    }


# ─── 2. Benchmark Ollama local ───────────────────────────────────────────────

def verificar_ollama() -> bool:
    """Comprueba si Ollama está disponible en localhost."""
    try:
        req = urllib.request.Request(f"{OLLAMA_URL}/api/tags")
        with urllib.request.urlopen(req, timeout=2) as resp:
            return resp.status == 200
    except (urllib.error.URLError, OSError):
        return False


def benchmark_ollama_local(repeticiones: int = 3) -> dict | None:
    if not verificar_ollama():
        print(f"\n[benchmark local — Ollama no disponible en {OLLAMA_URL}]")
        print("  Para ejecutar el benchmark local:")
        print("    1. Instala Ollama: https://ollama.com")
        print(f"   2. Descarga el modelo: ollama pull {OLLAMA_MODEL}")
        print("    3. Vuelve a ejecutar este script")
        return None

    print(f"\n[benchmark local — Ollama {OLLAMA_MODEL}]")
    print(f"  Prompt: {PROMPT_BENCHMARK[:60]!r}...\n")

    latencias: list[float] = []
    ttfts: list[float] = []

    for i in range(repeticiones):
        payload = json.dumps({
            "model": OLLAMA_MODEL,
            "prompt": PROMPT_BENCHMARK,
            "stream": True,
        }).encode()

        t_inicio = time.perf_counter()
        ttft: float | None = None
        tokens_generated = 0

        try:
            req = urllib.request.Request(
                f"{OLLAMA_URL}/api/generate",
                data=payload,
                headers={"Content-Type": "application/json"},
            )
            with urllib.request.urlopen(req, timeout=60) as resp:
                for line in resp:
                    if not line.strip():
                        continue
                    chunk = json.loads(line)
                    if ttft is None and chunk.get("response"):
                        ttft = time.perf_counter() - t_inicio
                    if chunk.get("eval_count"):
                        tokens_generated = chunk["eval_count"]
        except (urllib.error.URLError, json.JSONDecodeError) as exc:
            print(f"  rep{i+1}: error — {exc}")
            continue

        t_total = time.perf_counter() - t_inicio
        latencias.append(t_total)
        ttfts.append(ttft or t_total)
        tps = tokens_generated / t_total if t_total > 0 else 0

        print(f"  rep{i+1}: TTFT={ttft:.3f}s  total={t_total:.3f}s  "
              f"tokens_gen={tokens_generated}  TPS={tps:.1f}")

    if not latencias:
        return None

    avg_ttft  = sum(ttfts) / len(ttfts)
    avg_total = sum(latencias) / len(latencias)
    print(f"\n  Promedio: TTFT={avg_ttft:.3f}s  total={avg_total:.3f}s")

    return {
        "tipo": "local_ollama",
        "modelo": OLLAMA_MODEL,
        "avg_ttft_s": avg_ttft,
        "avg_latencia_s": avg_total,
        "costo_por_call_usd": 0.0,  # costo marginal ≈ 0
    }


# ─── 3. Calcular break-even ───────────────────────────────────────────────────

def calcular_breakeven(
    costo_por_call_api_usd: float,
    costo_gpu_hora: float = COSTO_GPU_HORA_USD,
    horas_dia: float = HORAS_DIA_ACTIVAS,
    requests_hora: int = REQUESTS_HORA_LOCAL,
) -> None:
    print("\n[break-even: ¿cuándo conviene el modelo local?]")

    costo_infra_dia  = costo_gpu_hora * horas_dia
    costo_infra_mes  = costo_infra_dia * 30
    requests_mes_max = requests_hora * int(horas_dia) * 30

    # ¿Cuántos requests necesito para que el local sea igual de caro que la API?
    if costo_por_call_api_usd > 0:
        breakeven_requests = costo_infra_mes / costo_por_call_api_usd
    else:
        breakeven_requests = float("inf")

    print(f"\n  Supuestos infraestructura local:")
    print(f"    GPU alquilada:        ${costo_gpu_hora:.2f}/hora")
    print(f"    Horas activas/día:    {horas_dia:.0f}h")
    print(f"    Costo infra/mes:      ${costo_infra_mes:.2f}")
    print(f"    Capacidad máx/mes:    {requests_mes_max:,} requests")

    print(f"\n  API cloud ({SMALL_MODEL}):")
    print(f"    Costo por call:       ${costo_por_call_api_usd:.6f}")
    print(f"    Break-even:           {breakeven_requests:,.0f} requests/mes")
    if breakeven_requests < requests_mes_max:
        pct = breakeven_requests / requests_mes_max * 100
        print(f"    ↳ Equivale al {pct:.1f}% de la capacidad del hardware")
        print(f"    ↳ Si superas {breakeven_requests:,.0f} req/mes, el local es MÁS barato")
    else:
        print(f"    ↳ La API es más barata incluso al 100% de capacidad del hardware")


def tabla_costo_mensual(
    requests_scenarios: list[int] | None = None,
    costo_por_call_api_usd: float = 0.0001,
) -> None:
    if requests_scenarios is None:
        requests_scenarios = [1_000, 10_000, 100_000, 500_000, 1_000_000]

    costo_infra_mes = COSTO_GPU_HORA_USD * HORAS_DIA_ACTIVAS * 30

    print("\n[tabla de costo mensual: API cloud vs local]")
    header = (
        f"  {'Requests/mes':>15}  {'API cloud ($)':>14}  "
        f"{'Local ($)':>12}  {'Diferencia':>12}  Ventaja"
    )
    sep = "  " + "-" * (len(header) - 2)
    print(header)
    print(sep)

    for req in requests_scenarios:
        coste_api   = req * costo_por_call_api_usd
        coste_local = costo_infra_mes  # fijo, no escala con requests
        diff        = coste_api - coste_local
        ventaja     = "local" if coste_local < coste_api else "API"
        print(
            f"  {req:>15,}  {coste_api:>14.2f}  "
            f"{coste_local:>12.2f}  {diff:>+12.2f}  {ventaja}"
        )


# ─── Main ──────────────────────────────────────────────────────────────────

if __name__ == "__main__":
    print("=== Local vs API: latencia y break-even de costos ===")

    resultado_api = benchmark_api_cloud(repeticiones=3)
    resultado_local = benchmark_ollama_local(repeticiones=3)

    if resultado_local:
        print("\n[comparación directa]")
        ratio_ttft    = resultado_api["avg_ttft_s"]    / resultado_local["avg_ttft_s"]
        ratio_latencia = resultado_api["avg_latencia_s"] / resultado_local["avg_latencia_s"]
        print(f"  API / Local TTFT ratio:    {ratio_ttft:.2f}x  "
              f"({'API más rápida' if ratio_ttft < 1 else 'Local más rápida'})")
        print(f"  API / Local latencia ratio:{ratio_latencia:.2f}x  "
              f"({'API más rápida' if ratio_latencia < 1 else 'Local más rápida'})")

    calcular_breakeven(
        costo_por_call_api_usd=resultado_api["costo_por_call_usd"],
    )
    tabla_costo_mensual(
        costo_por_call_api_usd=resultado_api["costo_por_call_usd"],
    )
