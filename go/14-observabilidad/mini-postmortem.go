// Mini-proyecto: El post-mortem automatizado (Go).
//
// Uso:
//
//	go run mini-postmortem.go
//	go run mini-postmortem.go -incidente latencia
//	go run mini-postmortem.go -incidente todos

// Cómo ejecutar: make go FILE=go/14-observabilidad/mini-postmortem.go

package main

import (
	"flag"
	"fmt"
	"math"
	"math/rand"
	"sort"
	"strings"
)

// ── tipos ─────────────────────────────────────────────────────────────────────

type SpanTipo string

const (
	LLMCall  SpanTipo = "llm_call"
	ToolCall SpanTipo = "tool_call"
)

type Span struct {
	spanID       string
	tipo         SpanTipo
	duracionMS   float64
	tokensInput  int
	tokensOutput int
	finishReason string
	toolSuccess  bool
	turno        int
}

type Sesion struct {
	sessionID  string
	turnos     int
	spans      []Span
	completada bool
}

type Severidad string

const (
	Info     Severidad = "info"
	Warning  Severidad = "warning"
	Critical Severidad = "critical"
)

type Hallazgo struct {
	tipo             string
	severidad        Severidad
	descripcion      string
	metrica          string
	umbral           string
	valorObservado   string
	sesionesAfectadas int
}

// ── generador ─────────────────────────────────────────────────────────────────

func generarSesionNormal(sessionID string, n int, rng *rand.Rand) Sesion {
	turnos := rng.Intn(6) + 3
	var spans []Span
	k := 0
	for t := 0; t < turnos; t++ {
		spans = append(spans, Span{
			spanID: fmt.Sprintf("span_%d_%d", n*100, k), tipo: LLMCall,
			duracionMS: 800 + rng.Float64()*1000, tokensInput: 800 + rng.Intn(1200),
			tokensOutput: 100 + rng.Intn(500), finishReason: "end_turn", toolSuccess: true, turno: t,
		})
		k++
		if rng.Float64() < 0.6 {
			spans = append(spans, Span{
				spanID: fmt.Sprintf("span_%d_%d", n*100, k), tipo: ToolCall,
				duracionMS: 50 + rng.Float64()*250, toolSuccess: true, finishReason: "end_turn", turno: t,
			})
			k++
		}
	}
	return Sesion{sessionID: sessionID, turnos: turnos, spans: spans, completada: true}
}

func generarHistorial(n int, incidente string, rng *rand.Rand) []Sesion {
	sesiones := make([]Sesion, n)
	for i := range sesiones {
		s := generarSesionNormal(fmt.Sprintf("sess_%03d", i), i, rng)
		if (incidente == "latencia" || incidente == "todos") && i >= n/2 {
			for j := range s.spans {
				if s.spans[j].tipo == LLMCall && rng.Float64() < 0.4 {
					s.spans[j].duracionMS = 12000 + rng.Float64()*13000
				}
			}
		}
		if (incidente == "costos" || incidente == "todos") && i >= n/3 {
			for j := range s.spans {
				if s.spans[j].tipo == LLMCall {
					s.spans[j].tokensInput = 18000 + rng.Intn(17000)
				}
			}
		}
		if (incidente == "loop_infinito" || incidente == "todos") && i >= n*2/3 {
			base := s.turnos
			for extra := 0; extra < 12; extra++ {
				s.spans = append(s.spans, Span{
					spanID: fmt.Sprintf("span_extra_%d", extra), tipo: LLMCall,
					duracionMS: 800 + rng.Float64()*800, tokensInput: 5000 + rng.Intn(3000),
					tokensOutput: 600, finishReason: "max_tokens", toolSuccess: true, turno: base + extra,
				})
			}
			s.turnos += 12
			s.completada = false
		}
		if (incidente == "tool_failures" || incidente == "todos") && i >= n/4 {
			for j := range s.spans {
				if s.spans[j].tipo == ToolCall && rng.Float64() < 0.7 {
					s.spans[j].toolSuccess = false
				}
			}
		}
		sesiones[i] = s
	}
	return sesiones
}

// ── análisis ──────────────────────────────────────────────────────────────────

func percentile(vals []float64, p float64) float64 {
	sorted := make([]float64, len(vals))
	copy(sorted, vals)
	sort.Float64s(sorted)
	idx := int(math.Floor(float64(len(sorted)) * p))
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

func analizarLatencia(sesiones []Sesion) []Hallazgo {
	var duraciones []float64
	sesionesSlow := 0
	for _, s := range sesiones {
		hasSlow := false
		for _, sp := range s.spans {
			if sp.tipo == LLMCall {
				duraciones = append(duraciones, sp.duracionMS)
				if sp.duracionMS > 10000 {
					hasSlow = true
				}
			}
		}
		if hasSlow {
			sesionesSlow++
		}
	}
	if len(duraciones) == 0 {
		return nil
	}
	p95 := percentile(duraciones, 0.95)
	var hallazgos []Hallazgo
	if p95 > 8000 {
		sev := Warning
		if p95 > 15000 {
			sev = Critical
		}
		hallazgos = append(hallazgos, Hallazgo{
			tipo: "latencia_p95", severidad: sev,
			descripcion: "P95 de latencia LLM supera umbral operacional.",
			metrica: "llm_call.duration_ms p95", umbral: "< 5,000ms",
			valorObservado: fmt.Sprintf("%.0fms", p95), sesionesAfectadas: sesionesSlow,
		})
	}
	return hallazgos
}

func analizarCostos(sesiones []Sesion) []Hallazgo {
	var hallazgos []Hallazgo
	var costos []float64
	var tokInputs []int
	for _, s := range sesiones {
		ti, to := 0, 0
		for _, sp := range s.spans {
			if sp.tipo == LLMCall {
				ti += sp.tokensInput
				to += sp.tokensOutput
				tokInputs = append(tokInputs, sp.tokensInput)
			}
		}
		costos = append(costos, (float64(ti)*3.0+float64(to)*15.0)/1_000_000)
	}
	costoMedio := 0.0
	for _, c := range costos {
		costoMedio += c
	}
	costoMedio /= math.Max(float64(len(costos)), 1)

	tokMedio := 0.0
	for _, t := range tokInputs {
		tokMedio += float64(t)
	}
	tokMedio /= math.Max(float64(len(tokInputs)), 1)

	if costoMedio > 0.05 {
		sev := Warning
		if costoMedio > 0.10 {
			sev = Critical
		}
		afectadas := 0
		for _, c := range costos {
			if c > 0.05 {
				afectadas++
			}
		}
		hallazgos = append(hallazgos, Hallazgo{
			tipo: "costo_por_sesion", severidad: sev,
			descripcion: "Costo por sesión supera presupuesto.",
			metrica: "session.cost_usd mean", umbral: "< $0.05",
			valorObservado: fmt.Sprintf("$%.4f", costoMedio), sesionesAfectadas: afectadas,
		})
	}
	if tokMedio > 10000 {
		afectadas := 0
		for _, t := range tokInputs {
			if t > 10000 {
				afectadas++
			}
		}
		hallazgos = append(hallazgos, Hallazgo{
			tipo: "contexto_inflado", severidad: Warning,
			descripcion: "Tokens input alto — historial sin compactar.",
			metrica: "llm_call.tokens_input mean", umbral: "< 5,000",
			valorObservado: fmt.Sprintf("%.0f", tokMedio), sesionesAfectadas: afectadas,
		})
	}
	return hallazgos
}

func analizarLoop(sesiones []Sesion) []Hallazgo {
	var hallazgos []Hallazgo
	conMaxTok, incompletas, turnosAltos := 0, 0, 0
	maxTurnos := 0
	for _, s := range sesiones {
		cnt := 0
		for _, sp := range s.spans {
			if sp.finishReason == "max_tokens" {
				cnt++
			}
		}
		if cnt > 3 {
			conMaxTok++
		}
		if !s.completada {
			incompletas++
		}
		if s.turnos > 15 {
			turnosAltos++
			if s.turnos > maxTurnos {
				maxTurnos = s.turnos
			}
		}
	}
	if conMaxTok > 0 {
		hallazgos = append(hallazgos, Hallazgo{
			tipo: "loop_max_tokens", severidad: Critical,
			descripcion: "Múltiples max_tokens — probable loop sin condición de salida.",
			metrica: "finish_reason == max_tokens", umbral: "< 1/sesión",
			valorObservado: fmt.Sprintf("%d sesiones", conMaxTok), sesionesAfectadas: conMaxTok,
		})
	}
	if incompletas > 0 {
		hallazgos = append(hallazgos, Hallazgo{
			tipo: "sesiones_incompletas", severidad: Critical,
			descripcion: "Sesiones no completadas.",
			metrica: "session.completada", umbral: "100%",
			valorObservado: fmt.Sprintf("%d/%d", incompletas, len(sesiones)), sesionesAfectadas: incompletas,
		})
	}
	if turnosAltos > 0 {
		hallazgos = append(hallazgos, Hallazgo{
			tipo: "turnos_excesivos", severidad: Warning,
			descripcion: "Sesiones con turnos anormalmente altos.",
			metrica: "session.turnos", umbral: "< 15",
			valorObservado: fmt.Sprintf("max=%d", maxTurnos), sesionesAfectadas: turnosAltos,
		})
	}
	return hallazgos
}

func analizarTools(sesiones []Sesion) []Hallazgo {
	total, failures := 0, 0
	for _, s := range sesiones {
		for _, sp := range s.spans {
			if sp.tipo == ToolCall {
				total++
				if !sp.toolSuccess {
					failures++
				}
			}
		}
	}
	if total == 0 {
		return nil
	}
	tasa := float64(failures) / float64(total) * 100
	if tasa <= 20 {
		return nil
	}
	afectadas := 0
	for _, s := range sesiones {
		for _, sp := range s.spans {
			if sp.tipo == ToolCall && !sp.toolSuccess {
				afectadas++
				break
			}
		}
	}
	sev := Warning
	if tasa > 50 {
		sev = Critical
	}
	return []Hallazgo{{
		tipo: "tool_failure_rate", severidad: sev,
		descripcion: "Tasa de fallos de herramientas sobre umbral.",
		metrica: "tool_call.success_rate", umbral: "> 95%",
		valorObservado: fmt.Sprintf("%.1f%% (%d/%d)", 100-tasa, failures, total),
		sesionesAfectadas: afectadas,
	}}
}

// ── reporte ───────────────────────────────────────────────────────────────────

var iconosSev = map[Severidad]string{Info: "ℹ️ ", Warning: "⚠️ ", Critical: "🚨"}

func imprimirReporte(sesiones []Sesion, hallazgos []Hallazgo, incidente string) {
	totalTokens, totalCosto := 0, 0.0
	for _, s := range sesiones {
		for _, sp := range s.spans {
			totalTokens += sp.tokensInput + sp.tokensOutput
			if sp.tipo == LLMCall {
				totalCosto += (float64(sp.tokensInput)*3.0 + float64(sp.tokensOutput)*15.0) / 1_000_000
			}
		}
	}

	fmt.Printf("\n%s\n", strings.Repeat("=", 64))
	fmt.Println("  POST-MORTEM AUTOMATIZADO")
	fmt.Printf("  Incidente: %s  |  %d sesiones\n", incidente, len(sesiones))
	fmt.Println(strings.Repeat("=", 64))
	fmt.Printf("\n  Tokens totales: %d\n", totalTokens)
	fmt.Printf("  Costo total: $%.4f\n", totalCosto)
	incompletas := 0
	for _, s := range sesiones {
		if !s.completada {
			incompletas++
		}
	}
	fmt.Printf("  Sesiones incompletas: %d\n", incompletas)

	criticos, warnings := 0, 0
	for _, h := range hallazgos {
		if h.severidad == Critical {
			criticos++
		} else {
			warnings++
		}
	}
	fmt.Printf("\n  Hallazgos: %d críticos, %d warnings\n", criticos, warnings)
	fmt.Printf("  %s\n", strings.Repeat("─", 56))

	if len(hallazgos) == 0 {
		fmt.Println("  ✅ Sin anomalías detectadas.")
	} else {
		sort.Slice(hallazgos, func(i, j int) bool {
			order := map[Severidad]int{Critical: 0, Warning: 1, Info: 2}
			return order[hallazgos[i].severidad] < order[hallazgos[j].severidad]
		})
		for _, h := range hallazgos {
			fmt.Printf("\n  %s [%s] %s\n", iconosSev[h.severidad], strings.ToUpper(string(h.severidad)), h.tipo)
			fmt.Printf("     %s\n", h.descripcion)
			fmt.Printf("     Umbral: %s | Observado: %s | Afectadas: %d/%d\n",
				h.umbral, h.valorObservado, h.sesionesAfectadas, len(sesiones))
		}
	}

	fmt.Printf("\n%s\n", strings.Repeat("=", 64))
	fmt.Println("  Causa raíz probable:")
	hasCrit := false
	for _, h := range hallazgos {
		if h.severidad == Critical {
			hasCrit = true
			switch h.tipo {
			case "loop_max_tokens":
				fmt.Println("  → Loop sin condición de salida — agregar max_turns")
			case "latencia_p95":
				fmt.Println("  → Picos de latencia — revisar timeouts")
			case "costo_por_sesion", "contexto_inflado":
				fmt.Println("  → Historial inflado — aplicar clearing")
			case "tool_failure_rate":
				fmt.Println("  → Servicio externo inestable — revisar circuit breaker")
			}
		}
	}
	if !hasCrit {
		fmt.Println("  → Sin anomalías críticas.")
	}
	fmt.Println(strings.Repeat("=", 64))
}

// ── main ──────────────────────────────────────────────────────────────────────

func main() {
	incidente := flag.String("incidente", "todos", "Tipo: latencia|costos|loop_infinito|tool_failures|todos|ninguno")
	nSesiones := flag.Int("sesiones", 20, "Número de sesiones a simular")
	flag.Parse()

	rng := rand.New(rand.NewSource(42))
	sesiones := generarHistorial(*nSesiones, *incidente, rng)
	var hallazgos []Hallazgo
	hallazgos = append(hallazgos, analizarLatencia(sesiones)...)
	hallazgos = append(hallazgos, analizarCostos(sesiones)...)
	hallazgos = append(hallazgos, analizarLoop(sesiones)...)
	hallazgos = append(hallazgos, analizarTools(sesiones)...)
	imprimirReporte(sesiones, hallazgos, *incidente)
}
