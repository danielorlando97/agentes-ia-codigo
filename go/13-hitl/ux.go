// UX de agentes interactivos — formatApprovalRequest.
//
// Transforma una acción técnica del agente (SQL, parámetros de herramienta)
// en una solicitud de aprobación legible para humanos no técnicos.
//
// La clave: título en lenguaje de negocio, impacto cuantificado, opciones
// que capturan matices más allá de aprobar/rechazar.
//
// Cómo ejecutar:
//
//	make go FILE=go/13-hitl/ux.go
package main

import (
	"fmt"
	"strings"
)

// ── Tipos ──────────────────────────────────────────────────────────────────

type opcion struct {
	Etiqueta   string
	Accion     string
	Descripcion string
}

type solicitudAprobacion struct {
	Titulo      string
	Descripcion string
	Impacto     string
	Reversible  bool
	VistaPrev   []string
	Contexto    string
	Opciones    []opcion
}

func (s solicitudAprobacion) mostrar() {
	reversibleStr := "Sí"
	if !s.Reversible {
		reversibleStr = "NO (irreversible)"
	}
	fmt.Printf("  Título:      %s\n", s.Titulo)
	fmt.Printf("  Descripción: %s\n", s.Descripcion)
	fmt.Printf("  Impacto:     %s\n", s.Impacto)
	fmt.Printf("  Reversible:  %s\n", reversibleStr)
	if len(s.VistaPrev) > 0 {
		fmt.Printf("  Muestra:     %s\n", strings.Join(s.VistaPrev, ", "))
	}
	fmt.Printf("  Contexto:    %s\n", s.Contexto)
	etiquetas := make([]string, len(s.Opciones))
	for i, o := range s.Opciones {
		etiquetas[i] = "[" + o.Etiqueta + "]"
	}
	fmt.Printf("  Opciones:    %s\n", strings.Join(etiquetas, " | "))
}

// ── Parámetros ─────────────────────────────────────────────────────────────

type accionParams struct {
	Herramienta string
	Tabla       string
	Condicion   string
	NRegistros  int
	IDs         []string
	Reversible  bool
	Ejemplos    []string
	To          []string
	Asunto      string
}

type contextoAgente struct {
	Razon        string
	UltimosPasos []string
}

// ── formatApprovalRequest ──────────────────────────────────────────────────

func formatApprovalRequest(p accionParams, ctx contextoAgente) solicitudAprobacion {
	var nAfectados int
	if p.Herramienta == "send_email" {
		nAfectados = len(p.To)
	} else {
		nAfectados = p.NRegistros
		if nAfectados == 0 {
			nAfectados = len(p.IDs)
		}
	}
	entidad := p.Tabla
	if entidad == "" {
		entidad = "registros"
	}

	var titulo string
	switch p.Herramienta {
	case "db_delete":
		titulo = strings.TrimSpace(fmt.Sprintf("Eliminar %d %s %s", nAfectados, entidad, p.Condicion))
	case "send_email":
		titulo = fmt.Sprintf("Enviar email a %d destinatarios", len(p.To))
	case "db_update":
		titulo = strings.TrimSpace(fmt.Sprintf("Actualizar %d %s %s", nAfectados, entidad, p.Condicion))
	default:
		titulo = "Ejecutar " + p.Herramienta
	}

	razon := ctx.Razon
	if razon == "" {
		razon = "completar la tarea actual"
	}

	var ctxStr string
	if len(ctx.UltimosPasos) > 0 {
		start := 0
		if len(ctx.UltimosPasos) > 3 {
			start = len(ctx.UltimosPasos) - 3
		}
		ctxStr = strings.Join(ctx.UltimosPasos[start:], " → ")
	} else {
		ctxStr = "inicio del flujo"
	}

	preview := p.Ejemplos
	if len(preview) > 5 {
		preview = preview[:5]
	}

	return solicitudAprobacion{
		Titulo:      titulo,
		Descripcion: "El agente propone esto porque: " + razon,
		Impacto:     fmt.Sprintf("Afectará %d %s", nAfectados, entidad),
		Reversible:  p.Reversible,
		VistaPrev:   preview,
		Contexto:    ctxStr,
		Opciones: []opcion{
			{"Aprobar", "aprobar", "Ejecutar la acción con los parámetros actuales"},
			{"Rechazar", "rechazar", "Cancelar la acción y notificar al agente"},
			{"Modificar parámetros", "modificar", "Ajustar el alcance antes de ejecutar"},
			{"Escalar a supervisor", "escalar", "Enviar la decisión a un nivel superior"},
			{"Posponer 24h", "posponer", "Ejecutar mañana a la misma hora"},
		},
	}
}

// ── Main ───────────────────────────────────────────────────────────────────

func main() {
	fmt.Println("=== UX de agentes: formatApprovalRequest ===\n")

	// Caso 1: eliminación destructiva
	fmt.Println("--- Caso 1: Eliminación irreversible ---\n")

	s1 := formatApprovalRequest(
		accionParams{
			Herramienta: "db_delete",
			Tabla:       "usuarios",
			Condicion:   "inactivos desde ene 2024",
			NRegistros:  1247,
			Reversible:  false,
			Ejemplos:    []string{"user@ejemplo.com", "otro@ejemplo.com", "tercero@ejemplo.com"},
		},
		contextoAgente{
			Razon:        "completar la limpieza de cuentas inactivas solicitada",
			UltimosPasos: []string{"analizar tabla usuarios", "filtrar por actividad", "calcular impacto"},
		},
	)

	fmt.Println("SIN FORMAT (lo que el agente produce internamente):")
	fmt.Println("  DELETE FROM usuarios WHERE inactive AND last_login < '2024-01-01'\n")
	fmt.Println("CON FORMAT (lo que el humano ve):")
	s1.mostrar()

	// Caso 2: envío de emails
	fmt.Println("\n--- Caso 2: Envío masivo de emails ---\n")

	destinatarios := make([]string, 843)
	for i := range destinatarios {
		destinatarios[i] = "usuario@empresa.com"
	}

	s2 := formatApprovalRequest(
		accionParams{
			Herramienta: "send_email",
			To:          destinatarios,
			Asunto:      "Actualización de términos de servicio",
			Reversible:  false,
		},
		contextoAgente{
			Razon:        "notificar a todos los usuarios del cambio de ToS antes del 30/06",
			UltimosPasos: []string{"redactar email", "seleccionar destinatarios"},
		},
	)

	fmt.Println("CON FORMAT:")
	s2.mostrar()

	// Indicador de progreso
	fmt.Println("\n--- Indicador de progreso (ejecución larga) ---\n")
	steps := [][3]string{
		{"✓", "Analizar estructura del repositorio", "2.3s"},
		{"✓", "Identificar tests existentes", "1.1s"},
		{"→", "Generando nuevos tests para módulo auth...", "30s estimado"},
		{" ", "Ejecutar suite de tests", "pendiente"},
		{" ", "Generar reporte de cobertura", "pendiente"},
	}
	for _, s := range steps {
		fmt.Printf("  [%s] %s (%s)\n", s[0], s[1], s[2])
	}
}
