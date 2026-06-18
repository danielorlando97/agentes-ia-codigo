// Memoria procedural: reglas de comportamiento extraídas de feedback.

// Cómo ejecutar: make go FILE=go/06-memoria/05-procedural/procedural_rules.go

package main

import (
	"database/sql"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"
	_ "github.com/mattn/go-sqlite3"
)

const ruleSchemaBudget = 800
const penalizacionConflicto = 0.3
const refuerzoUso = 0.1

const ruleCreateSQL = `
CREATE TABLE IF NOT EXISTS reglas (
    id           TEXT PRIMARY KEY,
    condicion    TEXT NOT NULL,
    accion       TEXT NOT NULL,
    alcance      TEXT NOT NULL DEFAULT 'global',
    fuerza       REAL NOT NULL DEFAULT 1.0,
    origen       TEXT NOT NULL DEFAULT 'feedback_explicito',
    estado       TEXT NOT NULL DEFAULT 'activa',
    conflicta_con TEXT,
    creado       REAL NOT NULL,
    ultimo_uso   REAL,
    usos         INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_reglas_alcance ON reglas(alcance);
CREATE INDEX IF NOT EXISTS idx_reglas_estado  ON reglas(estado);
`

type Regla struct {
	ID           string
	Condicion    string
	Accion       string
	Alcance      string
	Fuerza       float64
	Origen       string
	Estado       string
	ConflictaCon sql.NullString
	Creado       float64
	UltimoUso    sql.NullFloat64
	Usos         int
}

type AlmacenProcedural struct {
	db *sql.DB
}

func NewAlmacenProcedural(dbPath string) (*AlmacenProcedural, error) {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(ruleCreateSQL); err != nil {
		return nil, err
	}
	return &AlmacenProcedural{db: db}, nil
}

func (a *AlmacenProcedural) AddRule(condicion, accion, alcance string, fuerzaInicial float64, origen string) (*Regla, error) {
	existente, err := a.buscarExistente(condicion, alcance)
	if err != nil {
		return nil, err
	}
	if existente != nil {
		nuevaFuerza := existente.Fuerza + refuerzoUso
		a.db.Exec("UPDATE reglas SET fuerza=?, usos=usos+1 WHERE id=?", nuevaFuerza, existente.ID)
		existente.Fuerza = nuevaFuerza
		return existente, nil
	}

	r := &Regla{
		ID:        uuid.New().String(),
		Condicion: condicion,
		Accion:    accion,
		Alcance:   alcance,
		Fuerza:    fuerzaInicial,
		Origen:    origen,
		Estado:    "activa",
		Creado:    float64(time.Now().UnixNano()) / 1e9,
	}
	_, err = a.db.Exec(
		`INSERT INTO reglas (id, condicion, accion, alcance, fuerza, origen, estado, creado, usos) VALUES (?,?,?,?,?,?,?,?,0)`,
		r.ID, r.Condicion, r.Accion, r.Alcance, r.Fuerza, r.Origen, r.Estado, r.Creado,
	)
	if err != nil {
		return nil, err
	}
	a.detectarConflictos(r)
	return r, nil
}

func (a *AlmacenProcedural) OnFeedback(feedback, contexto, tipo, alcance string) (*Regla, error) {
	if tipo == "negativo" {
		if len(contexto) > 80 {
			contexto = contexto[:80]
		}
		if len(feedback) > 120 {
			feedback = feedback[:120]
		}
		return a.AddRule("cuando el contexto incluya: "+contexto, "evitar: "+feedback, alcance, 1.2, "feedback_explicito")
	}
	if tipo == "positivo" {
		if len(contexto) > 80 {
			contexto = contexto[:80]
		}
		if len(feedback) > 120 {
			feedback = feedback[:120]
		}
		return a.AddRule("cuando el contexto incluya: "+contexto, "mantener: "+feedback, alcance, 0.8, "feedback_implicito")
	}
	return nil, nil
}

func (a *AlmacenProcedural) BuildSystemPrompt(alcances []string, budgetTokens int, tokensPorCaracter float64) string {
	query := "SELECT id, condicion, accion FROM reglas WHERE estado='activa'"
	var args []interface{}
	if len(alcances) > 0 {
		query += " AND alcance IN (" + strings.Repeat("?,", len(alcances)-1) + "?)"
		for _, al := range alcances {
			args = append(args, al)
		}
	}
	query += " ORDER BY fuerza DESC"

	rows, err := a.db.Query(query, args...)
	if err != nil {
		return ""
	}
	defer rows.Close()

	var lineas []string
	tokensUsados := 0
	for rows.Next() {
		var id, condicion, accion string
		rows.Scan(&id, &condicion, &accion)
		linea := fmt.Sprintf("- Si %s: %s", condicion, accion)
		tokensLinea := int(float64(len(linea)) * tokensPorCaracter)
		if tokensUsados+tokensLinea > budgetTokens {
			break
		}
		lineas = append(lineas, linea)
		tokensUsados += tokensLinea
		a.db.Exec("UPDATE reglas SET ultimo_uso=?, usos=usos+1 WHERE id=?", float64(time.Now().UnixNano())/1e9, id)
	}

	if len(lineas) == 0 {
		return ""
	}
	return "## Reglas de comportamiento\n\n" + strings.Join(lineas, "\n")
}

func (a *AlmacenProcedural) Listar(soloActivas bool) ([]Regla, error) {
	q := "SELECT id, condicion, accion, alcance, fuerza, origen, estado, conflicta_con, creado, ultimo_uso, usos FROM reglas"
	if soloActivas {
		q += " WHERE estado='activa'"
	}
	q += " ORDER BY fuerza DESC"
	rows, err := a.db.Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var reglas []Regla
	for rows.Next() {
		var r Regla
		rows.Scan(&r.ID, &r.Condicion, &r.Accion, &r.Alcance, &r.Fuerza, &r.Origen, &r.Estado, &r.ConflictaCon, &r.Creado, &r.UltimoUso, &r.Usos)
		reglas = append(reglas, r)
	}
	return reglas, nil
}

func (a *AlmacenProcedural) buscarExistente(condicion, alcance string) (*Regla, error) {
	row := a.db.QueryRow(
		"SELECT id, condicion, accion, alcance, fuerza, origen, estado, conflicta_con, creado, ultimo_uso, usos FROM reglas WHERE condicion=? AND alcance=? AND estado='activa' LIMIT 1",
		condicion, alcance,
	)
	var r Regla
	err := row.Scan(&r.ID, &r.Condicion, &r.Accion, &r.Alcance, &r.Fuerza, &r.Origen, &r.Estado, &r.ConflictaCon, &r.Creado, &r.UltimoUso, &r.Usos)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &r, err
}

func (a *AlmacenProcedural) detectarConflictos(nueva *Regla) {
	rows, err := a.db.Query(
		"SELECT id, accion FROM reglas WHERE condicion=? AND id != ? AND estado='activa'",
		nueva.Condicion, nueva.ID,
	)
	if err != nil {
		return
	}
	defer rows.Close()

	for rows.Next() {
		var rowID, rowAccion string
		rows.Scan(&rowID, &rowAccion)
		accionA := strings.ToLower(nueva.Accion)
		accionB := strings.ToLower(rowAccion)
		if (strings.Contains(accionA, "evitar") && strings.Contains(accionB, "mantener")) ||
			(strings.Contains(accionA, "mantener") && strings.Contains(accionB, "evitar")) {
			newFuerza := math.Max(0.1, nueva.Fuerza-penalizacionConflicto)
			a.db.Exec("UPDATE reglas SET conflicta_con=?, fuerza=? WHERE id=?", rowID, newFuerza, nueva.ID)
			rowFuerza := 0.0
			a.db.QueryRow("SELECT fuerza FROM reglas WHERE id=?", rowID).Scan(&rowFuerza)
			rowFuerza = math.Max(0.1, rowFuerza-penalizacionConflicto)
			a.db.Exec("UPDATE reglas SET conflicta_con=?, fuerza=? WHERE id=?", nueva.ID, rowFuerza, rowID)
		}
	}
}

func main() {
	store, err := NewAlmacenProcedural(":memory:")
	if err != nil {
		panic(err)
	}

	store.AddRule("siempre", "responder en el idioma del usuario", "global", 2.0, "instruccion_sistema")
	store.AddRule("siempre", "usar formato Markdown para código", "global", 1.8, "instruccion_sistema")
	store.OnFeedback("no uses listas con viñetas para respuestas cortas", "respuestas de menos de 3 puntos", "negativo", "global")
	store.OnFeedback("incluye siempre ejemplos ejecutables en Python", "explicaciones técnicas con código", "positivo", "dominio:codigo")

	fmt.Println("--- Reglas activas (por fuerza) ---")
	reglas, _ := store.Listar(true)
	for _, r := range reglas {
		conflicto := ""
		if r.ConflictaCon.Valid {
			conflicto = fmt.Sprintf(" ⚠ conflicta con %s…", r.ConflictaCon.String[:8])
		}
		accion := r.Accion
		if len(accion) > 60 {
			accion = accion[:60]
		}
		fmt.Printf("  [%.2f] %s: %s%s\n", r.Fuerza, r.Alcance, accion, conflicto)
	}

	fmt.Println("\n--- System prompt generado ---")
	fmt.Println(store.BuildSystemPrompt(nil, 400, 0.25))

	fmt.Println("\n--- Solo alcance global ---")
	fmt.Println(store.BuildSystemPrompt([]string{"global"}, 300, 0.25))
}
