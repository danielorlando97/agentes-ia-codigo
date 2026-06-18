// Cómo ejecutar: make go FILE=go/13-hitl/interrupcion.go
package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/google/uuid"
	_ "github.com/mattn/go-sqlite3"
)

var modelHITL = envOr("MODEL", "claude-sonnet-4-6")

type EstadoAgente struct {
	ThreadID        string                 `json:"thread_id"`
	Tarea           string                 `json:"tarea"`
	Mensajes        []interface{}          `json:"mensajes"`
	PasoActual      int                    `json:"paso_actual"`
	Estado          string                 `json:"estado"`
	AccionPendiente map[string]interface{} `json:"accion_pendiente"`
	ResultadoFinal  string                 `json:"resultado_final"`
	Timestamp       float64                `json:"timestamp"`
	ExpiraEn        float64                `json:"expira_en"`
}

type Checkpointer struct {
	db *sql.DB
}

func NewCheckpointer(dbPath string) (*Checkpointer, error) {
	if dbPath == "" {
		dbPath = ":memory:"
	}
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, err
	}
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS checkpoints (
			thread_id   TEXT PRIMARY KEY,
			estado_json TEXT,
			ts          REAL
		)
	`)
	return &Checkpointer{db: db}, err
}

func (c *Checkpointer) Guardar(estado *EstadoAgente) (string, error) {
	data, err := json.Marshal(estado)
	if err != nil {
		return "", err
	}
	_, err = c.db.Exec(
		"INSERT OR REPLACE INTO checkpoints (thread_id, estado_json, ts) VALUES (?, ?, ?)",
		estado.ThreadID, string(data), float64(time.Now().UnixNano())/1e9,
	)
	return estado.ThreadID, err
}

func (c *Checkpointer) Restaurar(threadID string) (*EstadoAgente, error) {
	var estadoJSON string
	err := c.db.QueryRow(
		"SELECT estado_json FROM checkpoints WHERE thread_id = ?", threadID,
	).Scan(&estadoJSON)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var estado EstadoAgente
	return &estado, json.Unmarshal([]byte(estadoJSON), &estado)
}

func (c *Checkpointer) ListarPendientes() ([]map[string]interface{}, error) {
	rows, err := c.db.Query(
		"SELECT thread_id, ts FROM checkpoints WHERE estado_json LIKE '%esperando_aprobacion%'",
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []map[string]interface{}
	for rows.Next() {
		var threadID string
		var ts float64
		if err := rows.Scan(&threadID, &ts); err != nil {
			return nil, err
		}
		result = append(result, map[string]interface{}{"thread_id": threadID, "ts": ts})
	}
	return result, nil
}

var herramientas = []map[string]interface{}{
	{
		"name":        "buscar_datos",
		"description": "Busca datos. Seguro, no requiere aprobación.",
		"input_schema": map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{"query": map[string]interface{}{"type": "string"}},
			"required":   []string{"query"},
		},
	},
	{
		"name":        "borrar_registros",
		"description": "Borra registros de producción. REQUIERE APROBACIÓN HUMANA.",
		"input_schema": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"tabla":               map[string]interface{}{"type": "string"},
				"condicion":           map[string]interface{}{"type": "string"},
				"registros_afectados": map[string]interface{}{"type": "number"},
			},
			"required": []string{"tabla", "condicion", "registros_afectados"},
		},
	},
}

var herramientasAltoRiesgo = map[string]bool{"borrar_registros": true}

func ejecutarHerramientaReal(nombre string, params map[string]interface{}) string {
	switch nombre {
	case "buscar_datos":
		return fmt.Sprintf("Datos encontrados para '%v': 42 registros activos.", params["query"])
	case "borrar_registros":
		return fmt.Sprintf("[SIMULADO] %v registros borrados de '%v'.", params["registros_afectados"], params["tabla"])
	}
	return fmt.Sprintf("Herramienta '%s' no reconocida.", nombre)
}

func llamarAPI(mensajes []interface{}, maxTokens int) (map[string]interface{}, error) {
	payload := map[string]interface{}{
		"model":      modelHITL,
		"max_tokens": maxTokens,
		"tools":      herramientas,
		"messages":   mensajes,
	}
	body, _ := json.Marshal(payload)

	req, _ := http.NewRequest("POST", envBaseURL(), bytes.NewReader(body))
	req.Header.Set("x-api-key", os.Getenv("ANTHROPIC_API_KEY"))
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("content-type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)
	var result map[string]interface{}
	return result, json.Unmarshal(data, &result)
}

func ejecutarOInterrumpir(tarea string, threadID string, cp *Checkpointer) (*EstadoAgente, error) {
	var estado *EstadoAgente

	if threadID != "" {
		restaurado, err := cp.Restaurar(threadID)
		if err != nil {
			return nil, err
		}
		if restaurado != nil && restaurado.Estado == "esperando_aprobacion" {
			return restaurado, nil
		}
		estado = restaurado
	} else {
		newID := uuid.New().String()[:8]
		estado = &EstadoAgente{
			ThreadID:   newID,
			Tarea:      tarea,
			Mensajes:   []interface{}{map[string]interface{}{"role": "user", "content": tarea}},
			PasoActual: 0,
			Estado:     "en_progreso",
			Timestamp:  float64(time.Now().UnixNano()) / 1e9,
		}
	}

	for i := 0; i < 15; i++ {
		respuesta, err := llamarAPI(estado.Mensajes, 512)
		if err != nil {
			return nil, err
		}

		estado.Mensajes = append(estado.Mensajes, map[string]interface{}{
			"role":    "assistant",
			"content": respuesta["content"],
		})
		estado.PasoActual++

		stopReason, _ := respuesta["stop_reason"].(string)

		if stopReason == "end_turn" {
			content, _ := respuesta["content"].([]interface{})
			texto := ""
			for _, block := range content {
				b, ok := block.(map[string]interface{})
				if ok && b["type"] == "text" {
					texto, _ = b["text"].(string)
					break
				}
			}
			estado.Estado          = "completado"
			estado.ResultadoFinal  = texto
			cp.Guardar(estado)
			return estado, nil
		}

		if stopReason == "tool_use" {
			content, _ := respuesta["content"].([]interface{})
			var toolResults []interface{}

			for _, block := range content {
				b, ok := block.(map[string]interface{})
				if !ok || b["type"] != "tool_use" {
					continue
				}

				nombre, _ := b["name"].(string)
				toolUseID, _ := b["id"].(string)
				params, _ := b["input"].(map[string]interface{})

				if herramientasAltoRiesgo[nombre] {
					estado.Estado = "esperando_aprobacion"
					estado.AccionPendiente = map[string]interface{}{
						"tool_use_id": toolUseID,
						"nombre":      nombre,
						"params":      params,
					}
					estado.ExpiraEn = float64(time.Now().UnixNano())/1e9 + 72*3600
					cp.Guardar(estado)
					fmt.Printf("\n[INTERRUPCION] Acción de alto riesgo detectada:\n")
					fmt.Printf("  Herramienta: %s\n", nombre)
					paramsJSON, _ := json.Marshal(params)
					fmt.Printf("  Parámetros: %s\n", paramsJSON)
					fmt.Printf("  Thread ID para reanudar: %s\n", estado.ThreadID)
					return estado, nil
				}

				resultado := ejecutarHerramientaReal(nombre, params)
				toolResults = append(toolResults, map[string]interface{}{
					"type":        "tool_result",
					"tool_use_id": toolUseID,
					"content":     resultado,
				})
			}

			if len(toolResults) > 0 {
				estado.Mensajes = append(estado.Mensajes, map[string]interface{}{
					"role":    "user",
					"content": toolResults,
				})
			}
		}
	}

	estado.Estado         = "completado"
	estado.ResultadoFinal = "[max iteraciones]"
	cp.Guardar(estado)
	return estado, nil
}

func reanudar(threadID, decision string, cp *Checkpointer, motivo string) (*EstadoAgente, error) {
	estado, err := cp.Restaurar(threadID)
	if err != nil {
		return nil, err
	}
	if estado == nil {
		return nil, fmt.Errorf("checkpoint %s no encontrado o expirado", threadID)
	}
	if estado.Estado != "esperando_aprobacion" {
		return nil, fmt.Errorf("thread %s no está esperando aprobación (estado=%s)", threadID, estado.Estado)
	}

	accion := estado.AccionPendiente
	estado.AccionPendiente = nil

	var resultadoDecision string
	if decision == "rechazar" {
		if motivo == "" {
			motivo = "no especificado"
		}
		resultadoDecision = "Acción rechazada por el usuario. Motivo: " + motivo
	} else {
		params, _ := accion["params"].(map[string]interface{})
		nombre, _ := accion["nombre"].(string)
		resultadoDecision = ejecutarHerramientaReal(nombre, params)
	}

	toolUseID, _ := accion["tool_use_id"].(string)
	estado.Mensajes = append(estado.Mensajes, map[string]interface{}{
		"role": "user",
		"content": []interface{}{
			map[string]interface{}{
				"type":        "tool_result",
				"tool_use_id": toolUseID,
				"content":     resultadoDecision,
			},
		},
	})
	estado.Estado = "en_progreso"
	fmt.Printf("\n[REANUDACION] Thread %s | decisión=%s\n", threadID, decision)

	return ejecutarOInterrumpir(estado.Tarea, threadID, cp)
}

func main() {
	cp, err := NewCheckpointer("")
	if err != nil {
		panic(err)
	}

	fmt.Println("=== Ejecución con interrupción ===")
	estado, err := ejecutarOInterrumpir(
		"Primero busca los usuarios inactivos, luego borra los que llevan más de 2 años sin actividad.",
		"",
		cp,
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("\nEstado: %s\n", estado.Estado)

	if estado.Estado == "esperando_aprobacion" {
		fmt.Println("\n=== Simulando aprobación humana ===")
		estadoFinal, err := reanudar(estado.ThreadID, "aprobar", cp, "")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("\nEstado final: %s\n", estadoFinal.Estado)
		fmt.Printf("Resultado: %s\n", estadoFinal.ResultadoFinal)
	}
}

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
