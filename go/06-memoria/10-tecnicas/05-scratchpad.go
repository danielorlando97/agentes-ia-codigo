// Scratchpad: memoria en texto plano, legible por humanos y editable por el equipo.
// El agente lee el archivo al inicio e inyecta el contenido en el system prompt.
// Durante la sesión puede añadir notas tipadas y entradas de sesión.
//
// Cómo ejecutar: make go FILE=go/06-memoria/10-tecnicas/05-scratchpad.go

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const estructuraInicial = `# Notas del agente — proyecto

## Convenciones del proyecto

## Decisiones de arquitectura

## Deuda técnica conocida

## Notas de sesiones recientes
`

type Scratchpad struct {
	Ruta string
}

func NuevoScratchpad(ruta string) *Scratchpad {
	return &Scratchpad{Ruta: ruta}
}

func (s *Scratchpad) Inicializar() error {
	if _, err := os.Stat(s.Ruta); os.IsNotExist(err) {
		return os.WriteFile(s.Ruta, []byte(estructuraInicial), 0o644)
	}
	return nil
}

func (s *Scratchpad) LeerContexto() string {
	data, err := os.ReadFile(s.Ruta)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func (s *Scratchpad) BuildSystemPrompt(base string) string {
	ctx := s.LeerContexto()
	if ctx == "" {
		return base
	}
	return base + "\n\n## Notas de sesiones anteriores\n" + ctx
}

func (s *Scratchpad) EscribirNota(seccion, contenido string) error {
	var texto string
	if data, err := os.ReadFile(s.Ruta); err == nil {
		texto = string(data)
	} else {
		texto = estructuraInicial
	}

	marcaSeccion := "## " + seccion
	if !strings.Contains(texto, marcaSeccion) {
		texto += "\n" + marcaSeccion + "\n"
	}

	entrada := "- " + contenido
	lineas := strings.Split(texto, "\n")
	var nuevas []string
	dentroSeccion := false
	insertado := false

	for _, linea := range lineas {
		if strings.TrimSpace(linea) == marcaSeccion {
			dentroSeccion = true
		} else if strings.HasPrefix(linea, "## ") && dentroSeccion && !insertado {
			nuevas = append(nuevas, entrada)
			nuevas = append(nuevas, "")
			insertado = true
			dentroSeccion = false
		}
		nuevas = append(nuevas, linea)
	}
	if !insertado {
		nuevas = append(nuevas, entrada)
	}

	return os.WriteFile(s.Ruta, []byte(strings.Join(nuevas, "\n")), 0o644)
}

func (s *Scratchpad) EscribirNotaSesion(texto string) error {
	ts := time.Now().Format("2006-01-02 15:04")
	nota := fmt.Sprintf("\n### %s\n%s\n", ts, texto)
	f, err := os.OpenFile(s.Ruta, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(nota)
	return err
}

func (s *Scratchpad) TamañoTokens() int {
	data, err := os.ReadFile(s.Ruta)
	if err != nil {
		return 0
	}
	return len(data) / 4
}

func main() {
	rutaDemo := filepath.Join(os.TempDir(), "agente-scratchpad-demo.go.md")
	os.Remove(rutaDemo)

	sp := NuevoScratchpad(rutaDemo)
	if err := sp.Inicializar(); err != nil {
		fmt.Printf("Error al inicializar: %v\n", err)
		return
	}

	fmt.Println("=== Inicio de sesión ===")
	fmt.Printf("System prompt incluye %d tokens del scratchpad\n", sp.TamañoTokens())

	fmt.Println("\n=== El agente aprende durante la sesión ===")
	sp.EscribirNota("Convenciones del proyecto", "Usar snake_case para variables, PascalCase para tipos")
	sp.EscribirNota(
		"Decisiones de arquitectura",
		time.Now().Format("2006-01-02")+": Elegimos SQLite sobre PostgreSQL para la fase inicial",
	)
	sp.EscribirNota("Deuda técnica conocida", "src/auth/login.go:247 — condición de guarda incorrecta")
	sp.EscribirNotaSesion("Bug de auth localizado. Pendiente: escribir test de regresión antes del merge.")

	fmt.Println("Notas guardadas.\n")
	fmt.Println("=== Contenido del scratchpad ===")
	data, _ := os.ReadFile(rutaDemo)
	fmt.Println(string(data))
	fmt.Printf("=== Tamaño final: ~%d tokens ===\n", sp.TamañoTokens())

	os.Remove(rutaDemo)
}
