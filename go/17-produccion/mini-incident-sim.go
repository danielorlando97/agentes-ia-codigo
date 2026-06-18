// Mini-proyecto: El simulador de incidentes de producción (Go).
//
// Uso:
//
//	go run mini-incident-sim.go
//	go run mini-incident-sim.go -fallo timeout
//	go run mini-incident-sim.go -fallo todos

// Cómo ejecutar: make go FILE=go/17-produccion/mini-incident-sim.go

package main

import (
	"flag"
	"fmt"
	"math"
	"math/rand"
	"strings"
)

// ── tipos ─────────────────────────────────────────────────────────────────────

type Resultado struct {
	exito     bool
	intentos  int
	durMs     float64
	error     string
	estrategia string
	detalles  []string
}

// ── circuit breaker ────────────────────────────────────────────────────────────

type CircuitBreaker struct {
	nombre       string
	umbralFallos int
	fallos       int
	estado       string // closed | open | half-open
}

func newCB(nombre string, umbral int) *CircuitBreaker {
	return &CircuitBreaker{nombre: nombre, umbralFallos: umbral, estado: "closed"}
}

func (cb *CircuitBreaker) puedesPasar() bool {
	return cb.estado != "open"
}

func (cb *CircuitBreaker) registrarExito() {
	cb.fallos = 0
	cb.estado = "closed"
}

func (cb *CircuitBreaker) registrarFallo() {
	cb.fallos++
	if cb.fallos >= cb.umbralFallos {
		cb.estado = "open"
	}
}

// ── estrategias ───────────────────────────────────────────────────────────────

func jitter(baseMs float64, intento int, rng *rand.Rand) float64 {
	espera := baseMs * math.Pow(2, float64(intento))
	j := espera * 0.25 * (rng.Float64()*2 - 1)
	if espera+j < 100 {
		return 100
	}
	return espera + j
}

func recuperarTimeout(rng *rand.Rand) Resultado {
	var detalles []string
	for i := 0; i < 3; i++ {
		espera := jitter(500, i, rng)
		detalles = append(detalles, fmt.Sprintf("  Intento %d: espera %.0fms antes de reintentar", i+1, espera))
		if rng.Float64() > 0.4 {
			detalles = append(detalles, fmt.Sprintf("  ✓ LLM completada en intento %d", i+1))
			return Resultado{exito: true, intentos: i + 1, durMs: 1200 + float64(i)*800,
				estrategia: "retry_con_jitter", detalles: detalles}
		}
	}
	detalles = append(detalles, "  ✗ Todos los reintentos agotados")
	return Resultado{exito: false, intentos: 3, durMs: 5000,
		error: "LLM timeout tras 3 reintentos", estrategia: "retry_con_jitter", detalles: detalles}
}

func recuperarOutputMalformado(rng *rand.Rand) Resultado {
	detalles := []string{
		"  Output: '{\"hallazgos\": [broken json...'",
		"  Detección: JSONDecodeError",
		"  Estrategia: feedback al modelo con el error exacto",
		"  Prompt recovery: 'Tu respuesta no era JSON válido. Responde SOLO con JSON...'",
	}
	if rng.Float64() > 0.2 {
		detalles = append(detalles, "  ✓ Segundo intento produjo JSON válido")
		return Resultado{exito: true, intentos: 2, durMs: 1400,
			estrategia: "feedback_al_modelo", detalles: detalles}
	}
	detalles = append(detalles, "  ✗ Segundo intento también malformado")
	return Resultado{exito: false, intentos: 2, durMs: 1800,
		error: "Output malformado en 2 intentos", estrategia: "feedback_al_modelo", detalles: detalles}
}

func recuperarContextOverflow(tokensActuales, ventana int) Resultado {
	usoPct := float64(tokensActuales) / float64(ventana) * 100
	detalles := []string{
		fmt.Sprintf("  Contexto: %d tokens (%.1f%% de %d)", tokensActuales, usoPct, ventana),
	}
	if usoPct > 75 {
		objetivo := int(float64(ventana) * 0.6)
		liberados := tokensActuales - objetivo
		detalles = append(detalles,
			"  Umbral 75% superado — clearing de tool results",
			fmt.Sprintf("  Tokens liberados: ~%d", liberados),
			fmt.Sprintf("  Contexto resultante: %d (%.1f%%)", objetivo, float64(objetivo)/float64(ventana)*100),
		)
		return Resultado{exito: true, intentos: 1, durMs: 50,
			estrategia: "compresion_proactiva_75pct", detalles: detalles}
	}
	detalles = append(detalles, "  Dentro de límites — no requiere compresión.")
	return Resultado{exito: true, intentos: 1, durMs: 10, estrategia: "ninguna", detalles: detalles}
}

func recuperarToolFallo(cb *CircuitBreaker, rng *rand.Rand) Resultado {
	detalles := []string{
		fmt.Sprintf("  Circuit breaker '%s': estado=%s, fallos=%d", cb.nombre, cb.estado, cb.fallos),
	}
	if !cb.puedesPasar() {
		detalles = append(detalles,
			"  ✗ Circuit breaker ABIERTO — herramienta no disponible",
			"  Fallback: usando caché o resultado por defecto",
		)
		return Resultado{exito: false, intentos: 0, durMs: 5,
			error: fmt.Sprintf("Circuit breaker abierto para '%s'", cb.nombre),
			estrategia: "circuit_breaker_fallback", detalles: detalles}
	}
	detalles = append(detalles, "  Circuit breaker cerrado — intentando llamada")
	if rng.Float64() > 0.5 {
		cb.registrarExito()
		detalles = append(detalles, fmt.Sprintf("  ✓ Herramienta '%s' respondió", cb.nombre))
		return Resultado{exito: true, intentos: 1, durMs: 200,
			estrategia: "circuit_breaker_normal", detalles: detalles}
	}
	cb.registrarFallo()
	detalles = append(detalles, fmt.Sprintf("  ✗ Fallo — acumulados: %d/%d", cb.fallos, cb.umbralFallos))
	if cb.estado == "open" {
		detalles = append(detalles, "  Circuit breaker ahora ABIERTO")
	}
	return Resultado{exito: false, intentos: 1, durMs: 5000,
		error: "Tool timeout", estrategia: "circuit_breaker_normal", detalles: detalles}
}

func recuperarBudgetExcedido(costoActual, budget float64) Resultado {
	exceso := costoActual - budget
	detalles := []string{
		fmt.Sprintf("  Costo acumulado: $%.4f", costoActual),
		fmt.Sprintf("  Budget de tarea: $%.4f", budget),
		fmt.Sprintf("  Exceso: $%.4f (%.0f%% sobre budget)", exceso, exceso/budget*100),
	}
	if costoActual > budget {
		detalles = append(detalles,
			"  Estrategia: degradación a modelo económico para pasos restantes",
			"  Haiku ($0.80/Mtok) reemplaza Sonnet ($3.00/Mtok) — 3.75× más barato",
		)
		return Resultado{exito: true, intentos: 1, durMs: 0,
			estrategia: "model_downgrade_budget", detalles: detalles}
	}
	detalles = append(detalles, "  Budget no excedido.")
	return Resultado{exito: true, intentos: 1, durMs: 0, estrategia: "ninguna", detalles: detalles}
}

// ── presentación ──────────────────────────────────────────────────────────────

func imprimirResultado(tipo string, r Resultado) {
	estado := "✓ RECUPERADO"
	if !r.exito {
		estado = "✗ FALLIDO"
	}
	fmt.Printf("\n  %s\n", strings.Repeat("─", 56))
	fmt.Printf("  Fallo: %s\n", strings.ToUpper(tipo))
	fmt.Printf("  Estado: %s  |  Estrategia: %s\n", estado, r.estrategia)
	fmt.Printf("  Intentos: %d  |  Duración: %.0fms\n", r.intentos, r.durMs)
	if r.error != "" {
		fmt.Printf("  Error final: %s\n", r.error)
	}
	fmt.Println("\n  Traza de recuperación:")
	for _, d := range r.detalles {
		fmt.Printf("  %s\n", d)
	}
}

func imprimirResumen(fallos []string, resultados map[string]Resultado) {
	recuperados := 0
	for _, r := range resultados {
		if r.exito {
			recuperados++
		}
	}
	fmt.Printf("\n%s\n", strings.Repeat("=", 60))
	fmt.Println("  RESUMEN — Simulador de Incidentes de Producción")
	fmt.Println(strings.Repeat("=", 60))
	fmt.Printf("\n  %d/%d fallos recuperados automáticamente\n", recuperados, len(resultados))
	fmt.Printf("\n  %-22s %-16s %s\n", "Fallo", "Estado", "Estrategia")
	fmt.Printf("  %s\n", strings.Repeat("─", 56))
	for _, f := range fallos {
		r := resultados[f]
		estado := "RECUPERADO"
		if !r.exito {
			estado = "FALLIDO"
		}
		fmt.Printf("  %-22s %-16s %s\n", f, estado, r.estrategia)
	}
	fmt.Println("\n  Lecciones clave:")
	fmt.Println("  • Timeout: retry con jitter previene thundering herd")
	fmt.Println("  • Output malformado: feedback exacto al modelo")
	fmt.Println("  • Context overflow: compresión al 75% de uso, no al 100%")
	fmt.Println("  • Tool fallo: circuit breaker evita cascada de timeouts")
	fmt.Println("  • Budget excedido: degradar modelo antes de abortar")
	fmt.Println(strings.Repeat("=", 60))
}

// ── main ──────────────────────────────────────────────────────────────────────

func main() {
	fallo := flag.String("fallo", "todos", "Tipo: timeout|output_malformado|context_overflow|tool_fallo|budget_excedido|todos")
	flag.Parse()

	rng := rand.New(rand.NewSource(42))
	cb := newCB("search_docs", 3)
	cb.fallos = 3
	cb.estado = "open"

	fmt.Printf("\n%s\n", strings.Repeat("=", 60))
	fmt.Println("  SIMULADOR DE INCIDENTES DE PRODUCCIÓN")
	fmt.Printf("  Fallo: %s\n", *fallo)
	fmt.Println(strings.Repeat("=", 60))

	todos := []string{"timeout", "output_malformado", "context_overflow", "tool_fallo", "budget_excedido"}
	var fallos []string
	if *fallo == "todos" {
		fallos = todos
	} else {
		fallos = []string{*fallo}
	}

	resultados := make(map[string]Resultado)
	for _, f := range fallos {
		var r Resultado
		switch f {
		case "timeout":
			r = recuperarTimeout(rng)
		case "output_malformado":
			r = recuperarOutputMalformado(rng)
		case "context_overflow":
			r = recuperarContextOverflow(7200, 8192)
		case "tool_fallo":
			r = recuperarToolFallo(cb, rng)
		case "budget_excedido":
			r = recuperarBudgetExcedido(0.082, 0.06)
		default:
			continue
		}
		imprimirResultado(f, r)
		resultados[f] = r
	}

	if len(resultados) > 1 {
		imprimirResumen(fallos, resultados)
	}
}
