// Memoria episódica sin decay — log plano para prototipado.
// Variante mínima: solo append y búsqueda por texto o sesión.

// Cómo ejecutar: make go FILE=go/06-memoria/03-episodica/episodic_log.go

package main

import (
	"fmt"
	"strings"
	"time"
)

type EntradaLog struct {
	Contenido string
	SesionID  string
	Timestamp time.Time
}

type MemoriaLog struct {
	log []EntradaLog
}

func (m *MemoriaLog) Append(contenido, sesionID string) EntradaLog {
	e := EntradaLog{Contenido: contenido, SesionID: sesionID, Timestamp: time.Now()}
	m.log = append(m.log, e)
	return e
}

func (m *MemoriaLog) RecallRecent(n int) []EntradaLog {
	if len(m.log) <= n {
		return m.log
	}
	return m.log[len(m.log)-n:]
}

func (m *MemoriaLog) RecallSearch(query string, n int) []EntradaLog {
	q := strings.ToLower(query)
	var matches []EntradaLog
	for _, e := range m.log {
		if strings.Contains(strings.ToLower(e.Contenido), q) {
			matches = append(matches, e)
		}
	}
	if len(matches) <= n {
		return matches
	}
	return matches[len(matches)-n:]
}

func (m *MemoriaLog) RecallSession(sesionID string) []EntradaLog {
	var result []EntradaLog
	for _, e := range m.log {
		if e.SesionID == sesionID {
			result = append(result, e)
		}
	}
	return result
}

func (m *MemoriaLog) Len() int {
	return len(m.log)
}

func main() {
	mem := &MemoriaLog{}

	mem.Append("El usuario usa Python 3.12 en producción", "s1")
	mem.Append("Bug en auth.py línea 247: condición invertida", "s1")
	mem.Append("Decidimos usar PostgreSQL en lugar de SQLite", "s2")
	mem.Append("El módulo de billing tiene deuda técnica", "s2")
	mem.Append("Prefiere respuestas sin código cuando no es necesario", "s3")

	fmt.Printf("Total: %d episodios\n\n", mem.Len())

	fmt.Println("--- últimos 3 ---")
	for _, e := range mem.RecallRecent(3) {
		contenido := e.Contenido
		if len(contenido) > 60 {
			contenido = contenido[:60]
		}
		fmt.Printf("  [%s] %s\n", e.SesionID, contenido)
	}

	fmt.Println("\n--- búsqueda: 'usuario' ---")
	for _, e := range mem.RecallSearch("usuario", 5) {
		contenido := e.Contenido
		if len(contenido) > 60 {
			contenido = contenido[:60]
		}
		fmt.Printf("  [%s] %s\n", e.SesionID, contenido)
	}

	fmt.Println("\n--- sesión s1 ---")
	for _, e := range mem.RecallSession("s1") {
		contenido := e.Contenido
		if len(contenido) > 60 {
			contenido = contenido[:60]
		}
		fmt.Printf("  %s\n", contenido)
	}
}
