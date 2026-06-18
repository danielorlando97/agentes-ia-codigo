// Persistencia de estado: checkpoints en SQLite para reanudar tareas interrumpidas

// Cómo ejecutar: make go FILE=go/17-produccion/persistencia.go

package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"

	_ "github.com/mattn/go-sqlite3"
)

var persistenciaModel = envOr("MODEL", "claude-sonnet-4-6")

type checkpoint struct {
	tareaID     string
	paso        int
	mensajes    []map[string]interface{}
	tokensUsados int
	estado      string
	resultado   string
	errorMsg    string
}

type almacenCheckpoints struct {
	db *sql.DB
}

func nuevoAlmacen(dbPath string) (*almacenCheckpoints, error) {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, err
	}
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS checkpoints (
			tarea_id  TEXT,
			paso      INTEGER,
			mensajes  TEXT,
			tokens    INTEGER,
			estado    TEXT,
			resultado TEXT,
			error     TEXT,
			ts        TEXT DEFAULT (datetime('now')),
			PRIMARY KEY (tarea_id, paso)
		)
	`)
	if err != nil {
		return nil, err
	}
	return &almacenCheckpoints{db: db}, nil
}

func (a *almacenCheckpoints) guardar(cp checkpoint) error {
	mensajesJSON, _ := json.Marshal(cp.mensajes)
	var resultado, errorMsg interface{}
	if cp.resultado != "" {
		resultado = cp.resultado
	}
	if cp.errorMsg != "" {
		errorMsg = cp.errorMsg
	}
	_, err := a.db.Exec(
		"INSERT OR REPLACE INTO checkpoints (tarea_id, paso, mensajes, tokens, estado, resultado, error) VALUES (?, ?, ?, ?, ?, ?, ?)",
		cp.tareaID, cp.paso, string(mensajesJSON), cp.tokensUsados, cp.estado, resultado, errorMsg,
	)
	return err
}

func (a *almacenCheckpoints) cargarUltimo(tareaID string) (*checkpoint, error) {
	row := a.db.QueryRow(
		"SELECT tarea_id, paso, mensajes, tokens, estado, resultado, error FROM checkpoints WHERE tarea_id = ? ORDER BY paso DESC LIMIT 1",
		tareaID,
	)
	var cp checkpoint
	var mensajesJSON string
	var resultado, errorMsg sql.NullString
	err := row.Scan(&cp.tareaID, &cp.paso, &mensajesJSON, &cp.tokensUsados, &cp.estado, &resultado, &errorMsg)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	json.Unmarshal([]byte(mensajesJSON), &cp.mensajes)
	cp.resultado = resultado.String
	cp.errorMsg = errorMsg.String
	return &cp, nil
}

func (a *almacenCheckpoints) listar() ([]map[string]string, error) {
	rows, err := a.db.Query(
		"SELECT tarea_id, MAX(paso) as pasos, estado, ts FROM checkpoints GROUP BY tarea_id ORDER BY ts DESC",
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []map[string]string
	for rows.Next() {
		var tareaID, estado, ts string
		var pasos int
		rows.Scan(&tareaID, &pasos, &estado, &ts)
		result = append(result, map[string]string{
			"tarea_id": tareaID,
			"pasos":    fmt.Sprintf("%d", pasos),
			"estado":   estado,
			"ts":       ts,
		})
	}
	return result, nil
}

type persistenciaRequest struct {
	Model     string                   `json:"model"`
	MaxTokens int                      `json:"max_tokens"`
	Messages  []map[string]interface{} `json:"messages"`
}

type persistenciaResponse struct {
	StopReason string `json:"stop_reason"`
	Content    []struct {
		Text string `json:"text"`
	} `json:"content"`
	Usage struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

func llamarAnthropicPersistencia(mensajes []map[string]interface{}) (persistenciaResponse, error) {
	payload := persistenciaRequest{
		Model:     persistenciaModel,
		MaxTokens: 512,
		Messages:  mensajes,
	}
	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", envBaseURL(), bytes.NewReader(body))
	req.Header.Set("x-api-key", os.Getenv("ANTHROPIC_API_KEY"))
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("content-type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return persistenciaResponse{}, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	var ar persistenciaResponse
	return ar, json.Unmarshal(data, &ar)
}

func ejecutarConCheckpoint(pregunta, tareaID string, almacen *almacenCheckpoints) (map[string]interface{}, error) {
	cp, err := almacen.cargarUltimo(tareaID)
	if err != nil {
		return nil, err
	}
	if cp != nil && cp.estado == "completado" {
		fmt.Printf("[checkpoint] Tarea %s ya completada — devolviendo resultado guardado\n", tareaID)
		return map[string]interface{}{"resultado": cp.resultado, "tareaId": tareaID, "reanudado": false}, nil
	}

	var mensajes []map[string]interface{}
	var tokensTotal, pasoInicio int

	if cp != nil {
		fmt.Printf("[checkpoint] Reanudando tarea %s desde paso %d\n", tareaID, cp.paso)
		mensajes = cp.mensajes
		tokensTotal = cp.tokensUsados
		pasoInicio = cp.paso + 1
	} else {
		mensajes = []map[string]interface{}{{"role": "user", "content": pregunta}}
		fmt.Printf("[checkpoint] Nueva tarea %s\n", tareaID)
	}

	const maxPasos = 10
	for paso := pasoInicio; paso < maxPasos; paso++ {
		respuesta, err := llamarAnthropicPersistencia(mensajes)
		if err != nil {
			return nil, err
		}
		tokensTotal += respuesta.Usage.InputTokens + respuesta.Usage.OutputTokens
		textoRespuesta := respuesta.Content[0].Text
		mensajes = append(mensajes, map[string]interface{}{"role": "assistant", "content": textoRespuesta})

		almacen.guardar(checkpoint{
			tareaID:      tareaID,
			paso:         paso,
			mensajes:     mensajes,
			tokensUsados: tokensTotal,
			estado:       "en_progreso",
		})
		fmt.Printf("[checkpoint] Paso %d guardado (tokens=%d)\n", paso, tokensTotal)

		if respuesta.StopReason == "end_turn" {
			almacen.guardar(checkpoint{
				tareaID:      tareaID,
				paso:         paso,
				mensajes:     mensajes,
				tokensUsados: tokensTotal,
				estado:       "completado",
				resultado:    textoRespuesta,
			})
			return map[string]interface{}{
				"resultado": textoRespuesta,
				"tareaId":   tareaID,
				"pasos":     paso + 1,
			}, nil
		}
	}
	return map[string]interface{}{"error": "Límite de pasos alcanzado", "tareaId": tareaID}, nil
}

func main() {
	almacen, err := nuevoAlmacen(":memory:")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creando almacén: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("=== Primera ejecución ===")
	r, err := ejecutarConCheckpoint(
		"¿Qué ventajas tiene SQLite frente a PostgreSQL para aplicaciones pequeñas?",
		"demo-001",
		almacen,
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	texto := ""
	if t, ok := r["resultado"].(string); ok {
		texto = t
	} else if e, ok := r["error"].(string); ok {
		texto = e
	}
	if len(texto) > 200 {
		texto = texto[:200]
	}
	fmt.Printf("Resultado: %s\n", texto)

	fmt.Println("\n=== Reanudación (simula crash) ===")
	ejecutarConCheckpoint(
		"¿Qué ventajas tiene SQLite frente a PostgreSQL para aplicaciones pequeñas?",
		"demo-001",
		almacen,
	)
	fmt.Println("Reanudado: tarea ya completada, resultado cacheado")

	fmt.Println("\n=== Tareas registradas ===")
	tareas, _ := almacen.listar()
	for _, t := range tareas {
		fmt.Println(t)
	}
}

var _ = io.Discard

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envBaseURL() string {
	if v := os.Getenv("ANTHROPIC_BASE_URL"); v != "" {
		return v + "/v1/messages"
	}
	return "https://api.anthropic.com/v1/messages"
}
