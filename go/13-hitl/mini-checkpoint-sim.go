// Mini-proyecto: El checkpoint simulator (Go).
//
// Uso:
//
//	go run mini-checkpoint-sim.go
//	go run mini-checkpoint-sim.go -auto
//	go run mini-checkpoint-sim.go -escenario destructivo -auto

// Cómo ejecutar: make go FILE=go/13-hitl/mini-checkpoint-sim.go

package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"
)

// ── tipos ─────────────────────────────────────────────────────────────────────

type RiesgoNivel string

const (
	Bajo    RiesgoNivel = "bajo"
	Medio   RiesgoNivel = "medio"
	Alto    RiesgoNivel = "alto"
	Critico RiesgoNivel = "crítico"
)

type Decision string

const (
	Pendiente  Decision = "pendiente"
	Aprobado   Decision = "aprobado"
	Rechazado  Decision = "rechazado"
	Modificado Decision = "modificado"
)

type Accion struct {
	nombre      string
	descripcion string
	riesgo      RiesgoNivel
	reversible  bool
	requiereHITL bool
	payload     map[string]interface{}
}

type Checkpoint struct {
	accion      Accion
	contexto    string
	decision    Decision
	modificacion map[string]interface{}
	latenciaS   float64
}

type EstadoAgente struct {
	tarea       string
	historial   []string
	checkpoints []Checkpoint
	completado  bool
	abortado    bool
}

func (e *EstadoAgente) log(msg string) {
	e.historial = append(e.historial, msg)
	fmt.Printf("  [agente] %s\n", msg)
}

// ── escenarios ────────────────────────────────────────────────────────────────

var accionesSuaves = []Accion{
	{nombre: "leer_archivos", descripcion: "Leer 47 archivos de configuración", riesgo: Bajo, reversible: true, requiereHITL: false, payload: map[string]interface{}{"archivos": 47}},
	{nombre: "analizar_dependencias", descripcion: "Ejecutar análisis estático de dependencias", riesgo: Bajo, reversible: true, requiereHITL: false, payload: map[string]interface{}{"herramienta": "pip-audit"}},
	{nombre: "generar_reporte", descripcion: "Escribir reporte en reports/audit_2026.md", riesgo: Medio, reversible: true, requiereHITL: true, payload: map[string]interface{}{"archivo": "reports/audit_2026.md", "tamaño_kb": 12}},
	{nombre: "enviar_notificacion", descripcion: "Enviar email al equipo de seguridad (3 destinatarios)", riesgo: Medio, reversible: false, requiereHITL: true, payload: map[string]interface{}{"destinatarios": []string{"security@empresa.com"}, "asunto": "Audit 2026"}},
	{nombre: "cerrar_tarea", descripcion: "Marcar tarea como completada en el tracker", riesgo: Bajo, reversible: true, requiereHITL: false, payload: map[string]interface{}{"ticket": "SEC-1247"}},
}

var accionesDestructivas = []Accion{
	{nombre: "listar_usuarios", descripcion: "Obtener usuarios inactivos hace >90 días", riesgo: Bajo, reversible: true, requiereHITL: false, payload: map[string]interface{}{"filtro": "last_login < 90 días"}},
	{nombre: "revocar_tokens", descripcion: "Revocar tokens de 1,247 usuarios inactivos", riesgo: Alto, reversible: false, requiereHITL: true, payload: map[string]interface{}{"usuarios": 1247, "tokens": "API + OAuth"}},
	{nombre: "archivar_datos", descripcion: "Archivar datos en cold storage", riesgo: Alto, reversible: false, requiereHITL: true, payload: map[string]interface{}{"gb": 23.4, "destino": "s3://cold-archive/users/2026/"}},
	{nombre: "eliminar_cuentas", descripcion: "Eliminar definitivamente 1,247 cuentas de la BD", riesgo: Critico, reversible: false, requiereHITL: true, payload: map[string]interface{}{"usuarios": 1247, "operacion": "DELETE FROM users WHERE ..."}},
	{nombre: "purgar_logs", descripcion: "Purgar logs de usuarios eliminados", riesgo: Alto, reversible: false, requiereHITL: true, payload: map[string]interface{}{"registros": 89432, "tabla": "access_logs"}},
}

type Escenario struct {
	tarea     string
	acciones  []Accion
	decisiones map[string]Decision
}

var escenarios = map[string]Escenario{
	"suave": {
		tarea:    "Auditoría de seguridad y notificación al equipo",
		acciones: accionesSuaves,
		decisiones: map[string]Decision{
			"generar_reporte":    Aprobado,
			"enviar_notificacion": Modificado,
		},
	},
	"destructivo": {
		tarea:    "Limpieza de usuarios inactivos en producción",
		acciones: accionesDestructivas,
		decisiones: map[string]Decision{
			"revocar_tokens":  Aprobado,
			"archivar_datos":  Aprobado,
			"eliminar_cuentas": Rechazado,
			"purgar_logs":     Rechazado,
		},
	},
}

var iconosRiesgo = map[RiesgoNivel]string{
	Bajo: "🟢", Medio: "🟡", Alto: "🔴", Critico: "🚨",
}

// ── helpers ───────────────────────────────────────────────────────────────────

func mostrarCheckpoint(cp Checkpoint) {
	a := cp.accion
	payloadJSON, _ := json.Marshal(a.payload)
	fmt.Printf("\n%s\n", strings.Repeat("─", 60))
	fmt.Println("  CHECKPOINT — Aprobación requerida")
	fmt.Println(strings.Repeat("─", 60))
	fmt.Printf("  Acción:      %s\n", a.nombre)
	fmt.Printf("  Descripción: %s\n", a.descripcion)
	fmt.Printf("  Riesgo:      %s %s\n", iconosRiesgo[a.riesgo], strings.ToUpper(string(a.riesgo)))
	if a.reversible {
		fmt.Println("  Reversible:  Sí")
	} else {
		fmt.Println("  Reversible:  NO — irreversible")
	}
	fmt.Printf("  Contexto:    %s\n", cp.contexto)
	fmt.Printf("  Payload:     %s\n", payloadJSON)
	fmt.Println(strings.Repeat("─", 60))
}

var reader = bufio.NewReader(os.Stdin)

func leerLinea() string {
	line, _ := reader.ReadString('\n')
	return strings.TrimSpace(line)
}

func solicitarDecisionInteractiva(cp Checkpoint) Decision {
	mostrarCheckpoint(cp)
	fmt.Println("\n  Opciones: [A] Aprobar   [R] Rechazar   [M] Modificar   [S] Escalar")
	for {
		fmt.Print("\n  Tu decisión > ")
		resp := strings.ToUpper(leerLinea())
		switch resp {
		case "A":
			return Aprobado
		case "R":
			return Rechazado
		case "M":
			fmt.Println("  (Modificado → dry_run: true)")
			return Modificado
		case "S":
			fmt.Println("  [Escalado → aprobado]")
			return Aprobado
		default:
			fmt.Println("  Opción no válida.")
		}
	}
}

func solicitarDecisionAuto(cp Checkpoint, decisiones map[string]Decision) Decision {
	mostrarCheckpoint(cp)
	d, ok := decisiones[cp.accion.nombre]
	if !ok {
		d = Aprobado
	}
	fmt.Printf("\n  [auto] Decisión automática: %s\n", strings.ToUpper(string(d)))
	time.Sleep(300 * time.Millisecond)
	return d
}

// ── simulación ────────────────────────────────────────────────────────────────

func simularAgente(esc Escenario, auto bool) EstadoAgente {
	estado := EstadoAgente{tarea: esc.tarea}
	estado.log("Iniciando tarea: " + esc.tarea)

	for _, accion := range esc.acciones {
		if estado.abortado {
			break
		}
		estado.log("Preparando: " + accion.nombre)

		if accion.requiereHITL {
			cp := Checkpoint{
				accion:   accion,
				contexto: fmt.Sprintf("El agente ha completado %d pasos.", len(estado.historial)),
				decision: Pendiente,
			}

			t0 := time.Now()
			var decision Decision
			if auto {
				decision = solicitarDecisionAuto(cp, esc.decisiones)
			} else {
				decision = solicitarDecisionInteractiva(cp)
			}
			cp.latenciaS = time.Since(t0).Seconds()
			cp.decision = decision

			if decision == Rechazado {
				estado.log("✗ " + accion.nombre + " RECHAZADO — abortando")
				estado.checkpoints = append(estado.checkpoints, cp)
				estado.abortado = true
				break
			}
			if decision == Modificado {
				cp.modificacion = map[string]interface{}{"dry_run": true}
			}
			estado.checkpoints = append(estado.checkpoints, cp)
		}

		sufijo := ""
		if accion.requiereHITL && len(estado.checkpoints) > 0 {
			last := estado.checkpoints[len(estado.checkpoints)-1]
			if last.accion.nombre == accion.nombre && last.decision == Modificado {
				sufijo = " (dry-run)"
			}
		}
		estado.log(fmt.Sprintf("✓ %s ejecutado%s", accion.nombre, sufijo))
	}

	if !estado.abortado {
		estado.completado = true
		estado.log("Tarea completada.")
	}
	return estado
}

// ── reporte ───────────────────────────────────────────────────────────────────

func imprimirReporte(estado EstadoAgente) {
	fmt.Printf("\n%s\n", strings.Repeat("=", 60))
	fmt.Println("  REPORTE FINAL — CHECKPOINT SIMULATOR")
	fmt.Println(strings.Repeat("=", 60))
	fmt.Printf("\n  Tarea: %s\n", estado.tarea)
	estadoStr := "ABORTADA"
	if estado.completado {
		estadoStr = "COMPLETADA"
	}
	fmt.Printf("  Estado: %s\n", estadoStr)
	fmt.Printf("  Pasos ejecutados: %d\n", len(estado.historial))
	fmt.Printf("\n  Checkpoints (%d total):\n", len(estado.checkpoints))
	fmt.Printf("  %s\n", strings.Repeat("─", 56))

	totalLatencia := 0.0
	aprobados, rechazados, modificados := 0, 0, 0
	for _, cp := range estado.checkpoints {
		icon := map[Decision]string{Aprobado: "✓", Rechazado: "✗", Modificado: "~", Pendiente: "?"}[cp.decision]
		fmt.Printf("  %s %-30s [%-10s]  %s %s\n",
			icon, cp.accion.nombre, string(cp.decision), iconosRiesgo[cp.accion.riesgo], string(cp.accion.riesgo))
		if cp.latenciaS > 0 {
			fmt.Printf("    Latencia: %.1fs\n", cp.latenciaS)
			totalLatencia += cp.latenciaS
		}
		switch cp.decision {
		case Aprobado:
			aprobados++
		case Rechazado:
			rechazados++
		case Modificado:
			modificados++
		}
	}

	total := len(estado.checkpoints)
	if total > 0 {
		tasa := float64(aprobados+modificados) / float64(total) * 100
		fmt.Printf("\n  Tasa de aprobación: %.0f%% (%d aprobados, %d modificados, %d rechazados)\n",
			tasa, aprobados, modificados, rechazados)
		if tasa > 95 {
			fmt.Println("  ⚠️  Approval fatigue — revisar umbrales de riesgo")
		}
		if totalLatencia > 0 {
			fmt.Printf("  Latencia total HITL: %.1fs\n", totalLatencia)
		}
	}

	fmt.Printf("\n%s\n", strings.Repeat("=", 60))
	fmt.Println("  Lecciones:")
	fmt.Println("  • Los checkpoints bloquean — más checkpoints = mayor latencia")
	fmt.Println("  • Un rechazo puede abortar todo el pipeline")
	fmt.Println("  • 'Modificar' reduce abortos sin aprobar incondicionalmente")
	fmt.Println("  • Tasa > 95% = umbrales mal calibrados")
	fmt.Println(strings.Repeat("=", 60))
}

// ── main ──────────────────────────────────────────────────────────────────────

func main() {
	auto := flag.Bool("auto", false, "Modo automático con decisiones simuladas")
	escenarioKey := flag.String("escenario", "suave", "Escenario: suave|destructivo")
	flag.Parse()

	esc, ok := escenarios[*escenarioKey]
	if !ok {
		esc = escenarios["suave"]
	}

	fmt.Printf("\n%s\n", strings.Repeat("=", 60))
	fmt.Println("  CHECKPOINT SIMULATOR")
	modoStr := "interactivo"
	if *auto {
		modoStr = "automático"
	}
	fmt.Printf("  Escenario: %s  |  Modo: %s\n", *escenarioKey, modoStr)
	fmt.Println(strings.Repeat("=", 60))
	fmt.Printf("\n  Tarea: %s\n", esc.tarea)
	n := 0
	for _, a := range esc.acciones {
		if a.requiereHITL {
			n++
		}
	}
	fmt.Printf("  Checkpoints HITL: %d\n", n)

	estado := simularAgente(esc, *auto)
	imprimirReporte(estado)
}
