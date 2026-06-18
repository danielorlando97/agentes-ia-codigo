// Mini-proyecto: El costómetro (Go).
//
// Uso:
//
//	go run mini-costometro.go
//	go run mini-costometro.go -sesiones 5000
//	go run mini-costometro.go -max-tokens 8192
//	go run mini-costometro.go -prompt mi_prompt.txt

// Cómo ejecutar: make go FILE=go/03-motor-llm/mini-costometro.go

package main

import (
	"flag"
	"fmt"
	"math"
	"os"
	"strings"
)

// Snapshot precios mayo 2026 — verificar en docs del proveedor
var precios = map[string][2]float64{
	"haiku":  {0.80, 4.00},
	"sonnet": {3.00, 15.00},
	"opus":   {15.00, 75.00},
}
var ventanas = map[string]int{
	"haiku": 200_000, "sonnet": 200_000, "opus": 200_000,
}

var promptEjemplo = `Eres un agente de revisión de código Python. Tu trabajo es analizar el
código que te envíen y producir un informe estructurado en JSON.

REGLAS:
1. Responde SIEMPRE en JSON con el schema exacto indicado abajo.
2. Clasifica hallazgos por severidad: critical, high, medium, low.
3. No expliques el código; solo reporta problemas concretos.
4. Si no hay bugs, devuelve hallazgos = [].

SCHEMA:
{
  "hallazgos": [{"linea": null, "severidad": "...", "tipo": "...",
                 "descripcion": "...", "sugerencia": "..."}],
  "resumen": "..."
}

GUÍAS DE ESTILO DEL EQUIPO:
- PEP 8 obligatorio
- Type hints en todas las funciones públicas
- Cobertura de tests mínima: 80%
`

func estimarTokens(texto string) int {
	n := len([]rune(texto)) / 4
	if n < 1 {
		return 1
	}
	return n
}

type Seccion struct {
	label  string
	tokens int
}

func analizarSecciones(prompt string) []Seccion {
	var secciones []Seccion
	for _, bloque := range strings.Split(prompt, "\n\n") {
		bloque = strings.TrimSpace(bloque)
		if bloque == "" {
			continue
		}
		lineas := strings.SplitN(bloque, "\n", 2)
		label := lineas[0]
		if len([]rune(label)) > 45 {
			label = string([]rune(label)[:45])
		}
		secciones = append(secciones, Seccion{
			label:  label,
			tokens: estimarTokens(bloque),
		})
	}
	return secciones
}

func calcularCoste(tokens int, modelo string, input bool) float64 {
	p := precios[modelo]
	var precio float64
	if input {
		precio = p[0]
	} else {
		precio = p[1]
	}
	return float64(tokens) * precio / 1_000_000
}

func imprimirTablaModelos(tokensPrompt, maxTokensOutput, sesiones int) {
	fmt.Printf("\n%-10s %13s %9s %10s %18s %10s\n",
		"Modelo", "Tokens input", "USD/req", "USD/día", "Budget resp", "% ventana")
	fmt.Println(strings.Repeat("-", 75))
	for _, modelo := range []string{"haiku", "sonnet", "opus"} {
		costoReq := calcularCoste(tokensPrompt, modelo, true)
		costoDia := costoReq * float64(sesiones)
		ventana := ventanas[modelo]
		budget := ventana - tokensPrompt - maxTokensOutput
		budgetStr := fmt.Sprintf("%d", budget)
		if budget < 0 {
			budgetStr = fmt.Sprintf("OVERFLOW -%d", -budget)
		}
		pct := float64(tokensPrompt) / float64(ventana) * 100
		fmt.Printf("%-10s %13d $%8.5f $%9.4f %18s %9.1f%%\n",
			modelo, tokensPrompt, costoReq, costoDia, budgetStr, pct)
	}
}

func main() {
	sesiones := flag.Int("sesiones", 1000, "Sesiones/día para proyección")
	maxTokens := flag.Int("max-tokens", 4096, "Tokens de output reservados")
	promptFile := flag.String("prompt", "", "Archivo con el system prompt")
	flag.Parse()

	var prompt string
	if *promptFile != "" {
		data, err := os.ReadFile(*promptFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: no se encontró '%s'\n", *promptFile)
			os.Exit(1)
		}
		prompt = string(data)
	} else {
		prompt = promptEjemplo
		fmt.Println("[Usando prompt de ejemplo — pasa -prompt archivo.txt para usar el tuyo]\n")
	}

	tokensTotal := estimarTokens(prompt)
	secciones := analizarSecciones(prompt)

	fmt.Println(strings.Repeat("=", 60))
	fmt.Println("EL COSTÓMETRO — Análisis de system prompt")
	fmt.Println(strings.Repeat("=", 60))
	fmt.Printf("\nPrompt: %d chars  |  ~%d tokens\n", len([]rune(prompt)), tokensTotal)
	fmt.Printf("Proyección: %d sesiones/día  |  %d tokens output reservados\n", *sesiones, *maxTokens)

	fmt.Printf("\n%-43s %7s %5s\n", "Sección (primera línea)", "Tokens", "%")
	fmt.Println(strings.Repeat("-", 58))
	for _, sec := range secciones {
		pct := float64(sec.tokens) / float64(tokensTotal) * 100
		label := sec.label
		runes := []rune(label)
		if len(runes) > 43 {
			label = string(runes[:41]) + ".."
		}
		fmt.Printf("%-43s %7d %4.1f%%\n", label, sec.tokens, pct)
	}
	fmt.Println(strings.Repeat("-", 58))
	fmt.Printf("%-43s %7d %s\n", "TOTAL", tokensTotal, "100.0%")

	fmt.Printf("\n--- Coste por modelo (%d sesiones/día) ---\n", *sesiones)
	imprimirTablaModelos(tokensTotal, *maxTokens, *sesiones)

	fmt.Println("\n--- Efecto de truncar el prompt ---")
	fmt.Printf("\n%-12s %8s %17s %16s\n", "% prompt", "Tokens", "USD/día (sonnet)", "Ahorro USD/día")
	fmt.Println(strings.Repeat("-", 56))
	costeBase := calcularCoste(tokensTotal, "sonnet", true) * float64(*sesiones)
	for _, pct := range []int{100, 75, 50, 25} {
		tok := int(math.Floor(float64(tokensTotal) * float64(pct) / 100))
		coste := calcularCoste(tok, "sonnet", true) * float64(*sesiones)
		ahorro := costeBase - coste
		fmt.Printf("%10d%%   %8d  $%16.4f  $%15.4f\n", pct, tok, coste, ahorro)
	}

	anual := calcularCoste(tokensTotal, "sonnet", true) * float64(*sesiones) * 365
	fmt.Printf("\n→ Coste anual proyectado (sonnet, %d/día): $%.2f\n", *sesiones, anual)
	fmt.Printf("→ Con caching (10× más barato en zona estática): $%.2f/año\n", anual/10)
	fmt.Println("\n[Estimación ±10% — el conteo exacto requiere el tokenizador del proveedor]")
	fmt.Println("[Snapshot precios mayo 2026 — verificar en docs del proveedor]")
}
