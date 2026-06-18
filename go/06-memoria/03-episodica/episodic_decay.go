// Memoria episódica con decay exponencial, refuerzo y ciclo de vida.
// Usa database/sql con go-sqlite3. El embedding usa un vector ficticio de dimensión 4.

// Cómo ejecutar: make go FILE=go/06-memoria/03-episodica/episodic_decay.go

package main

import (
	"database/sql"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

const createSQL = `
CREATE TABLE IF NOT EXISTS episodios (
    id             TEXT PRIMARY KEY,
    contenido      TEXT NOT NULL,
    timestamp      REAL NOT NULL,
    sesion_id      TEXT,
    fuerza         REAL NOT NULL DEFAULT 1.0,
    half_life_dias REAL NOT NULL DEFAULT 7.0,
    accesos        INTEGER NOT NULL DEFAULT 0,
    ultimo_acceso  REAL NOT NULL,
    estado         TEXT NOT NULL DEFAULT 'activo'
);
CREATE INDEX IF NOT EXISTS idx_episodios_estado    ON episodios(estado);
CREATE INDEX IF NOT EXISTS idx_episodios_fuerza    ON episodios(fuerza);
CREATE INDEX IF NOT EXISTS idx_episodios_timestamp ON episodios(timestamp);
`

const (
	umbralOlvido    = 0.05
	deltaRefuerzo   = 0.15
	umbralConsolidar = 0.85
)

type Episodio struct {
	ID           string
	Contenido    string
	Timestamp    float64
	SesionID     sql.NullString
	Fuerza       float64
	HalfLifeDias float64
	Accesos      int
	UltimoAcceso float64
	Estado       string
}

func embed(texto string) [4]float64 {
	words := strings.Fields(strings.ToLower(texto))
	var vec [4]float64
	for i, w := range words {
		if i >= 4 {
			break
		}
		h := 0
		for _, c := range w {
			h = 31*h + int(c)
		}
		if h < 0 {
			h = -h
		}
		vec[i%4] += float64(h%100) / 100.0
	}
	norm := 0.0
	for _, x := range vec {
		norm += x * x
	}
	norm = math.Sqrt(norm)
	if norm == 0 {
		norm = 1.0
	}
	for i := range vec {
		vec[i] /= norm
	}
	return vec
}

func coseno(a, b [4]float64) float64 {
	dot, na, nb := 0.0, 0.0, 0.0
	for i := range a {
		dot += a[i] * b[i]
		na += a[i] * a[i]
		nb += b[i] * b[i]
	}
	na = math.Sqrt(na)
	if na == 0 {
		na = 1e-9
	}
	nb = math.Sqrt(nb)
	if nb == 0 {
		nb = 1e-9
	}
	return dot / (na * nb)
}

var idCounter int

func generateID() string {
	idCounter++
	return fmt.Sprintf("%d-%d", time.Now().UnixNano(), idCounter)
}

type AlmacenEpisodico struct {
	db         *sql.DB
	embeddings map[string][4]float64
}

func NewAlmacenEpisodico(dbPath string) *AlmacenEpisodico {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		panic(err)
	}
	_, err = db.Exec(createSQL)
	if err != nil {
		panic(err)
	}
	return &AlmacenEpisodico{db: db, embeddings: make(map[string][4]float64)}
}

func (a *AlmacenEpisodico) Record(contenido string, sesionID sql.NullString, halfLifeDias float64) Episodio {
	id := generateID()
	now := float64(time.Now().UnixNano()) / 1e9
	a.db.Exec(
		`INSERT INTO episodios (id, contenido, timestamp, sesion_id, fuerza, half_life_dias, accesos, ultimo_acceso, estado)
         VALUES (?,?,?,?,1.0,?,0,?,'activo')`,
		id, contenido, now, sesionID, halfLifeDias, now,
	)
	a.embeddings[id] = embed(contenido)
	return Episodio{ID: id, Contenido: contenido, Timestamp: now, SesionID: sesionID,
		Fuerza: 1.0, HalfLifeDias: halfLifeDias, Accesos: 0, UltimoAcceso: now, Estado: "activo"}
}

type EpisodioScore struct {
	Ep    Episodio
	Score float64
}

func (a *AlmacenEpisodico) Recall(query string, topK int, skipReinforce bool) []EpisodioScore {
	qVec := embed(query)
	ahora := float64(time.Now().UnixNano()) / 1e9

	rows, _ := a.db.Query("SELECT id, contenido, timestamp, sesion_id, fuerza, half_life_dias, accesos, ultimo_acceso, estado FROM episodios WHERE estado = 'activo'")
	defer rows.Close()

	var episodios []Episodio
	for rows.Next() {
		var ep Episodio
		rows.Scan(&ep.ID, &ep.Contenido, &ep.Timestamp, &ep.SesionID, &ep.Fuerza,
			&ep.HalfLifeDias, &ep.Accesos, &ep.UltimoAcceso, &ep.Estado)
		episodios = append(episodios, ep)
	}
	if len(episodios) == 0 {
		return nil
	}

	tMin, tMax := episodios[0].Timestamp, episodios[0].Timestamp
	for _, ep := range episodios {
		if ep.Timestamp < tMin {
			tMin = ep.Timestamp
		}
		if ep.Timestamp > tMax {
			tMax = ep.Timestamp
		}
	}
	tRango := tMax - tMin
	if tRango == 0 {
		tRango = 1.0
	}

	var candidatos []EpisodioScore
	for _, ep := range episodios {
		epVec, ok := a.embeddings[ep.ID]
		if !ok {
			epVec = embed(ep.Contenido)
		}
		sim := math.Max(0, coseno(qVec, epVec))
		recencia := (ep.Timestamp - tMin) / tRango
		fuerzaN := math.Min(1.0, ep.Fuerza)
		score := 0.5*sim + 0.3*recencia + 0.2*fuerzaN
		candidatos = append(candidatos, EpisodioScore{Ep: ep, Score: score})
	}

	sort.Slice(candidatos, func(i, j int) bool { return candidatos[i].Score > candidatos[j].Score })
	if len(candidatos) > topK {
		candidatos = candidatos[:topK]
	}

	if !skipReinforce {
		for _, c := range candidatos {
			a.db.Exec(
				`UPDATE episodios SET fuerza = MIN(fuerza + ?, 2.0), accesos = accesos + 1, ultimo_acceso = ? WHERE id = ?`,
				deltaRefuerzo, ahora, c.Ep.ID,
			)
		}
	}
	return candidatos
}

func (a *AlmacenEpisodico) TickLifecycle() map[string]int {
	ahora := float64(time.Now().UnixNano()) / 1e9
	stats := map[string]int{"olvidados": 0, "consolidados": 0}

	rows, _ := a.db.Query("SELECT id, fuerza, half_life_dias, ultimo_acceso FROM episodios WHERE estado = 'activo'")
	type rowData struct {
		ID           string
		Fuerza       float64
		HalfLifeDias float64
		UltimoAcceso float64
	}
	var allRows []rowData
	for rows.Next() {
		var r rowData
		rows.Scan(&r.ID, &r.Fuerza, &r.HalfLifeDias, &r.UltimoAcceso)
		allRows = append(allRows, r)
	}
	rows.Close()

	for _, r := range allRows {
		elapsedDias := (ahora - r.UltimoAcceso) / 86400.0
		nuevaFuerza := r.Fuerza * math.Exp(-math.Ln2*elapsedDias/r.HalfLifeDias)
		if nuevaFuerza < umbralOlvido {
			a.db.Exec("UPDATE episodios SET fuerza=?, estado='olvidado' WHERE id=?", nuevaFuerza, r.ID)
			stats["olvidados"]++
		} else {
			a.db.Exec("UPDATE episodios SET fuerza=? WHERE id=?", nuevaFuerza, r.ID)
		}
	}

	ventanaSegundos := float64(24 * 3600)
	recientesRows, _ := a.db.Query(
		"SELECT id, contenido, fuerza, half_life_dias FROM episodios WHERE estado='activo' AND timestamp > ?",
		ahora-ventanaSegundos,
	)
	var recientes []clusterRow
	for recientesRows.Next() {
		var r clusterRow
		recientesRows.Scan(&r.ID, &r.Contenido, &r.Fuerza, &r.HalfLifeDias)
		recientes = append(recientes, r)
	}
	recientesRows.Close()

	visitados := make(map[string]bool)
	for _, r := range recientes {
		if visitados[r.ID] {
			continue
		}
		cluster := []clusterRow{r}
		vecR := a.embeddings[r.ID]
		for _, other := range recientes {
			if other.ID == r.ID || visitados[other.ID] {
				continue
			}
			vecO := a.embeddings[other.ID]
			if coseno(vecR, vecO) >= umbralConsolidar {
				cluster = append(cluster, other)
				visitados[other.ID] = true
			}
		}
		visitados[r.ID] = true
		if len(cluster) >= 3 {
			a.consolidarCluster(cluster)
			stats["consolidados"] += len(cluster)
		}
	}
	return stats
}

type clusterRow struct {
	ID           string
	Contenido    string
	Fuerza       float64
	HalfLifeDias float64
}

func (a *AlmacenEpisodico) consolidarCluster(cluster []clusterRow) {
	textos := make([]string, len(cluster))
	maxFuerza := 0.0
	for i, r := range cluster {
		textos[i] = r.Contenido
		if r.Fuerza > maxFuerza {
			maxFuerza = r.Fuerza
		}
	}
	limit := 3
	if len(textos) < 3 {
		limit = len(textos)
	}
	resumen := fmt.Sprintf("[Consolidado de %d episodios]\n%s", len(cluster), strings.Join(textos[:limit], " | "))
	nuevo := a.Record(resumen, sql.NullString{}, cluster[0].HalfLifeDias)
	a.db.Exec("UPDATE episodios SET fuerza=? WHERE id=?", maxFuerza, nuevo.ID)
	ids := make([]string, len(cluster))
	for i, r := range cluster {
		ids[i] = r.ID
	}
	placeholders := strings.Repeat("?,", len(ids))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]interface{}, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	a.db.Exec(fmt.Sprintf("UPDATE episodios SET estado='consolidado' WHERE id IN (%s)", placeholders), args...)
}

func main() {
	store := NewAlmacenEpisodico(":memory:")

	store.Record("El usuario prefiere respuestas concisas", sql.NullString{}, 30)
	store.Record("Bug en auth.py línea 247: condición invertida", sql.NullString{}, 3)
	store.Record("Decidimos usar PostgreSQL en lugar de SQLite para producción", sql.NullString{}, 90)
	store.Record("El módulo de billing tiene deuda técnica: lógica duplicada", sql.NullString{}, 7)

	fmt.Println("--- recall: 'base de datos producción' ---")
	for _, es := range store.Recall("base de datos producción", 3, false) {
		contenido := es.Ep.Contenido
		if len(contenido) > 60 {
			contenido = contenido[:60]
		}
		fmt.Printf("  [%.3f] %s\n", es.Score, contenido)
	}

	fmt.Println("\n--- recall exploratorio (skipReinforce) ---")
	for _, es := range store.Recall("preferencias usuario", 2, true) {
		contenido := es.Ep.Contenido
		if len(contenido) > 60 {
			contenido = contenido[:60]
		}
		fmt.Printf("  [%.3f] %s\n", es.Score, contenido)
	}

	fmt.Println("\n--- tick_lifecycle ---")
	stats := store.TickLifecycle()
	fmt.Printf("  olvidados: %d, consolidados: %d\n", stats["olvidados"], stats["consolidados"])
}
