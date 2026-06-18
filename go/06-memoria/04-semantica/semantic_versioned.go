// Memoria semántica con tombstone+versionado y detección de conflictos.

// Cómo ejecutar: make go FILE=go/06-memoria/04-semantica/semantic_versioned.go

package main

import (
	"database/sql"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	_ "github.com/mattn/go-sqlite3"
)

const createSQL = `
CREATE TABLE IF NOT EXISTS hechos (
    id           TEXT PRIMARY KEY,
    sujeto       TEXT NOT NULL,
    predicado    TEXT NOT NULL,
    objeto       TEXT NOT NULL,
    certeza      REAL NOT NULL DEFAULT 1.0,
    fuente       TEXT NOT NULL DEFAULT 'usuario_directo',
    creado       REAL NOT NULL,
    estado       TEXT NOT NULL DEFAULT 'activo',
    version      INTEGER NOT NULL DEFAULT 1,
    reemplaza_a  TEXT
);
CREATE TABLE IF NOT EXISTS conflictos (
    id           TEXT PRIMARY KEY,
    hecho_a_id   TEXT,
    hecho_b_id   TEXT,
    creado       REAL NOT NULL,
    resuelto     INTEGER NOT NULL DEFAULT 0,
    resolucion   TEXT
);
CREATE INDEX IF NOT EXISTS idx_hechos_sujeto    ON hechos(sujeto);
CREATE INDEX IF NOT EXISTS idx_hechos_estado    ON hechos(estado);
`

const deltaCerteza = 0.20

var certezaPorFuente = map[string]float64{
	"usuario_directo": 0.97,
	"tool_resultado":  0.85,
	"auto_extract":    0.60,
	"inferencia":      0.50,
}

type Hecho struct {
	ID          string
	Sujeto      string
	Predicado   string
	Objeto      string
	Certeza     float64
	Fuente      string
	Creado      float64
	Estado      string
	Version     int
	ReemplazaA  sql.NullString
}

type Conflicto struct {
	ID        string
	HechoAID  string
	HechoBID  string
	Creado    float64
	Resuelto  bool
	Resolucion sql.NullString
}

type AlmacenSemantico struct {
	db *sql.DB
}

func NewAlmacenSemantico(dbPath string) (*AlmacenSemantico, error) {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(createSQL); err != nil {
		return nil, err
	}
	return &AlmacenSemantico{db: db}, nil
}

func (a *AlmacenSemantico) AssertFact(sujeto, predicado, objeto, fuente string, certeza float64) (*Hecho, *Conflicto, error) {
	if certeza <= 0 {
		certeza = certezaPorFuente[fuente]
	}

	existente, err := a.buscarActivo(sujeto, predicado)
	if err != nil {
		return nil, nil, err
	}

	if existente == nil {
		h, err := a.insertar(sujeto, predicado, objeto, fuente, certeza, 1, "")
		return h, nil, err
	}

	if existente.Objeto == objeto {
		if certeza > existente.Certeza {
			a.db.Exec("UPDATE hechos SET certeza=?, fuente=? WHERE id=?", certeza, fuente, existente.ID)
		}
		return existente, nil, nil
	}

	delta := math.Abs(certeza - existente.Certeza)
	if delta > deltaCerteza {
		if certeza > existente.Certeza {
			h, err := a.corregir(existente, objeto, fuente, certeza)
			return h, nil, err
		}
		return existente, nil, nil
	}

	nuevo, err := a.insertar(sujeto, predicado, objeto, fuente, certeza, 1, "")
	if err != nil {
		return nil, nil, err
	}
	conflicto, err := a.crearConflicto(existente.ID, nuevo.ID)
	return nuevo, conflicto, err
}

func (a *AlmacenSemantico) Query(sujeto, predicado string, certezaMinima float64) ([]Hecho, error) {
	sql := "SELECT id, sujeto, predicado, objeto, certeza, fuente, creado, estado, version, reemplaza_a FROM hechos WHERE estado='activo' AND certeza >= ?"
	args := []interface{}{certezaMinima}
	if sujeto != "" {
		sql += " AND sujeto = ?"
		args = append(args, sujeto)
	}
	if predicado != "" {
		sql += " AND predicado = ?"
		args = append(args, predicado)
	}
	sql += " ORDER BY certeza DESC, creado DESC"

	rows, err := a.db.Query(sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var hechos []Hecho
	for rows.Next() {
		var h Hecho
		if err := rows.Scan(&h.ID, &h.Sujeto, &h.Predicado, &h.Objeto, &h.Certeza, &h.Fuente, &h.Creado, &h.Estado, &h.Version, &h.ReemplazaA); err != nil {
			return nil, err
		}
		hechos = append(hechos, h)
	}
	return hechos, nil
}

func (a *AlmacenSemantico) ConflictosPendientes() ([]Conflicto, error) {
	rows, err := a.db.Query("SELECT id, hecho_a_id, hecho_b_id, creado, resuelto, resolucion FROM conflictos WHERE resuelto=0")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var conflictos []Conflicto
	for rows.Next() {
		var c Conflicto
		var resuelto int
		if err := rows.Scan(&c.ID, &c.HechoAID, &c.HechoBID, &c.Creado, &resuelto, &c.Resolucion); err != nil {
			return nil, err
		}
		c.Resuelto = resuelto != 0
		conflictos = append(conflictos, c)
	}
	return conflictos, nil
}

func (a *AlmacenSemantico) ResolverConflicto(conflictoID, resolucion string) error {
	var hAID, hBID string
	row := a.db.QueryRow("SELECT hecho_a_id, hecho_b_id FROM conflictos WHERE id=?", conflictoID)
	if err := row.Scan(&hAID, &hBID); err != nil {
		return err
	}
	if resolucion == "a_gana" {
		a.tombstone(hBID)
	} else if resolucion == "b_gana" {
		a.tombstone(hAID)
	}
	_, err := a.db.Exec("UPDATE conflictos SET resuelto=1, resolucion=? WHERE id=?", resolucion, conflictoID)
	return err
}

func (a *AlmacenSemantico) buscarActivo(sujeto, predicado string) (*Hecho, error) {
	row := a.db.QueryRow(
		"SELECT id, sujeto, predicado, objeto, certeza, fuente, creado, estado, version, reemplaza_a FROM hechos WHERE sujeto=? AND predicado=? AND estado='activo' LIMIT 1",
		sujeto, predicado,
	)
	var h Hecho
	if err := row.Scan(&h.ID, &h.Sujeto, &h.Predicado, &h.Objeto, &h.Certeza, &h.Fuente, &h.Creado, &h.Estado, &h.Version, &h.ReemplazaA); err == sql.ErrNoRows {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	return &h, nil
}

func (a *AlmacenSemantico) insertar(sujeto, predicado, objeto, fuente string, certeza float64, version int, reemplazaA string) (*Hecho, error) {
	h := &Hecho{
		ID:        uuid.New().String(),
		Sujeto:    sujeto,
		Predicado: predicado,
		Objeto:    objeto,
		Certeza:   certeza,
		Fuente:    fuente,
		Creado:    float64(time.Now().UnixNano()) / 1e9,
		Estado:    "activo",
		Version:   version,
	}
	if reemplazaA != "" {
		h.ReemplazaA = sql.NullString{String: reemplazaA, Valid: true}
	}
	_, err := a.db.Exec(
		`INSERT INTO hechos (id, sujeto, predicado, objeto, certeza, fuente, creado, estado, version, reemplaza_a) VALUES (?,?,?,?,?,?,?,?,?,?)`,
		h.ID, h.Sujeto, h.Predicado, h.Objeto, h.Certeza, h.Fuente, h.Creado, h.Estado, h.Version, h.ReemplazaA,
	)
	return h, err
}

func (a *AlmacenSemantico) corregir(existente *Hecho, nuevoObjeto, fuente string, certeza float64) (*Hecho, error) {
	a.tombstone(existente.ID)
	return a.insertar(existente.Sujeto, existente.Predicado, nuevoObjeto, fuente, certeza, existente.Version+1, existente.ID)
}

func (a *AlmacenSemantico) tombstone(hechoID string) {
	a.db.Exec("UPDATE hechos SET estado='tombstone' WHERE id=?", hechoID)
}

func (a *AlmacenSemantico) crearConflicto(hechoAID, hechoBID string) (*Conflicto, error) {
	c := &Conflicto{
		ID:       uuid.New().String(),
		HechoAID: hechoAID,
		HechoBID: hechoBID,
		Creado:   float64(time.Now().UnixNano()) / 1e9,
	}
	_, err := a.db.Exec(
		"INSERT INTO conflictos (id, hecho_a_id, hecho_b_id, creado, resuelto) VALUES (?,?,?,?,0)",
		c.ID, c.HechoAID, c.HechoBID, c.Creado,
	)
	return c, err
}

func main() {
	store, err := NewAlmacenSemantico(":memory:")
	if err != nil {
		panic(err)
	}

	h1, _, _ := store.AssertFact("usuario", "lenguaje_preferido", "Python", "usuario_directo", 0)
	h2, _, _ := store.AssertFact("usuario", "zona_horaria", "Europe/Madrid", "auto_extract", 0.65)
	h3, _, _ := store.AssertFact("proyecto", "base_de_datos", "PostgreSQL", "usuario_directo", 0)
	_ = h1; _ = h2; _ = h3

	fmt.Println("--- Hechos activos ---")
	hechos, _ := store.Query("", "", 0.5)
	for _, h := range hechos {
		fmt.Printf("  (%s, %s, %s) certeza=%.2f v%d\n", h.Sujeto, h.Predicado, h.Objeto, h.Certeza, h.Version)
	}

	fmt.Println("\n--- Corrección: lenguaje Python → Go ---")
	h4, conflicto, _ := store.AssertFact("usuario", "lenguaje_preferido", "Go", "usuario_directo", 0)
	fmt.Printf("  nuevo hecho: %s v%d, reemplaza: %v\n", h4.Objeto, h4.Version, h4.ReemplazaA)
	fmt.Printf("  conflicto: %v\n", conflicto)

	fmt.Println("\n--- Contradicción certeza similar → conflicto ---")
	h5, conflicto2, _ := store.AssertFact("usuario", "zona_horaria", "America/Mexico_City", "auto_extract", 0.62)
	_ = h5
	if conflicto2 != nil {
		fmt.Printf("  Conflicto creado: %s…\n", conflicto2.ID[:8])
		pendientes, _ := store.ConflictosPendientes()
		fmt.Printf("  Pendientes: %d\n", len(pendientes))
	}

	fmt.Println("\n--- Hechos activos finales ---")
	hechos, _ = store.Query("", "", 0)
	for _, h := range hechos {
		fmt.Printf("  (%s, %s, %s) certeza=%.2f v%d\n", h.Sujeto, h.Predicado, h.Objeto, h.Certeza, h.Version)
	}
}
