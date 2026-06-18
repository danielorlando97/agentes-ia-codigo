// Cómo ejecutar: make go FILE=go/07-estado-contexto/persistencia.go
package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	_ "github.com/mattn/go-sqlite3"
)

const schemaVersion = 1

type CheckpointTrigger string

const (
	TriggerPhaseComplete   CheckpointTrigger = "phase_complete"
	TriggerIrreversibleAct CheckpointTrigger = "irreversible_act"
	TriggerUserRequest     CheckpointTrigger = "user_request"
	TriggerBudgetThreshold CheckpointTrigger = "budget_threshold"
	TriggerPeriodic        CheckpointTrigger = "periodic"
)

type Checkpoint struct {
	ID            string                 `json:"id"`
	TaskID        string                 `json:"task_id"`
	Messages      []map[string]interface{} `json:"messages"`
	Trigger       string                 `json:"trigger"`
	SchemaVersion int                    `json:"schema_version"`
	Metadata      map[string]interface{} `json:"metadata"`
	Timestamp     float64                `json:"timestamp"`
}

func NewCheckpoint(taskID string, messages []map[string]interface{}, trigger string, metadata map[string]interface{}) *Checkpoint {
	if trigger == "" {
		trigger = string(TriggerPhaseComplete)
	}
	if metadata == nil {
		metadata = map[string]interface{}{}
	}
	return &Checkpoint{
		ID:            uuid.New().String(),
		TaskID:        taskID,
		Messages:      messages,
		Trigger:       trigger,
		SchemaVersion: schemaVersion,
		Metadata:      metadata,
		Timestamp:     float64(time.Now().UnixNano()) / 1e9,
	}
}

func (c *Checkpoint) ToJSON() (string, error) {
	b, err := json.Marshal(c)
	return string(b), err
}

func CheckpointFromJSON(data string) (*Checkpoint, error) {
	var cp Checkpoint
	err := json.Unmarshal([]byte(data), &cp)
	return &cp, err
}

type SQLiteCheckpointStore struct {
	db *sql.DB
}

func NewSQLiteCheckpointStore(dbPath string) (*SQLiteCheckpointStore, error) {
	if dbPath == "" {
		dbPath = ":memory:"
	}
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, err
	}
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS checkpoints (
			id             TEXT PRIMARY KEY,
			task_id        TEXT NOT NULL,
			schema_version INTEGER NOT NULL,
			trigger        TEXT NOT NULL,
			timestamp      REAL NOT NULL,
			data           TEXT NOT NULL
		)
	`)
	if err != nil {
		return nil, err
	}
	_, err = db.Exec("CREATE INDEX IF NOT EXISTS idx_task ON checkpoints(task_id, timestamp)")
	if err != nil {
		return nil, err
	}
	return &SQLiteCheckpointStore{db: db}, nil
}

func (s *SQLiteCheckpointStore) Save(cp *Checkpoint) (string, error) {
	data, err := cp.ToJSON()
	if err != nil {
		return "", err
	}
	_, err = s.db.Exec(
		"INSERT INTO checkpoints VALUES (?,?,?,?,?,?)",
		cp.ID, cp.TaskID, cp.SchemaVersion, cp.Trigger, cp.Timestamp, data,
	)
	return cp.ID, err
}

func (s *SQLiteCheckpointStore) Load(checkpointID string) (*Checkpoint, error) {
	var data string
	var storedVersion int
	err := s.db.QueryRow(
		"SELECT data, schema_version FROM checkpoints WHERE id=?",
		checkpointID,
	).Scan(&data, &storedVersion)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	cp, err := CheckpointFromJSON(data)
	if err != nil {
		return nil, err
	}
	if cp.SchemaVersion != schemaVersion {
		cp = downgradeCheckpoint(cp, schemaVersion)
	}
	return cp, nil
}

func (s *SQLiteCheckpointStore) List(taskID string) ([]*Checkpoint, error) {
	rows, err := s.db.Query(
		"SELECT data FROM checkpoints WHERE task_id=? ORDER BY timestamp ASC",
		taskID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []*Checkpoint
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		cp, err := CheckpointFromJSON(data)
		if err != nil {
			return nil, err
		}
		result = append(result, cp)
	}
	return result, nil
}

func (s *SQLiteCheckpointStore) Latest(taskID string) (*Checkpoint, error) {
	var data string
	err := s.db.QueryRow(
		"SELECT data FROM checkpoints WHERE task_id=? ORDER BY timestamp DESC LIMIT 1",
		taskID,
	).Scan(&data)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return CheckpointFromJSON(data)
}

func downgradeCheckpoint(cp *Checkpoint, targetVersion int) *Checkpoint {
	if cp.SchemaVersion == targetVersion {
		return cp
	}
	return &Checkpoint{
		ID:            cp.ID,
		TaskID:        cp.TaskID,
		Messages:      cp.Messages,
		Trigger:       fmt.Sprintf("downgraded_from_v%d", cp.SchemaVersion),
		SchemaVersion: targetVersion,
		Metadata: map[string]interface{}{
			"downgraded":       true,
			"original_version": cp.SchemaVersion,
		},
		Timestamp: cp.Timestamp,
	}
}

func shouldCheckpoint(trigger string, toolCallsSinceLast int) bool {
	primaryTriggers := map[string]bool{
		string(TriggerPhaseComplete):   true,
		string(TriggerIrreversibleAct): true,
		string(TriggerUserRequest):     true,
	}
	if primaryTriggers[trigger] {
		return true
	}
	if trigger == string(TriggerPeriodic) {
		return toolCallsSinceLast >= 20
	}
	return false
}

func main() {
	store, err := NewSQLiteCheckpointStore("")
	if err != nil {
		panic(err)
	}
	task := "analisis-repo-facturacion"

	msgs := []map[string]interface{}{
		{"role": "user",      "content": "Analiza el módulo de auth."},
		{"role": "assistant", "content": "Encontré 2 vulnerabilidades en auth.py."},
	}

	cp1 := NewCheckpoint(task, msgs, string(TriggerPhaseComplete), map[string]interface{}{
		"fase": 1, "vulnerabilidades": 2,
	})
	id1, err := store.Save(cp1)
	if err != nil {
		panic(err)
	}
	fmt.Printf("Guardado: %s... | trigger=%s\n", id1[:8], cp1.Trigger)

	cargado, err := store.Load(id1)
	if err != nil {
		panic(err)
	}
	fmt.Printf("Cargado: task=%s | msgs=%d | v=%d\n", cargado.TaskID, len(cargado.Messages), cargado.SchemaVersion)

	cpViejo := &Checkpoint{
		ID:            uuid.New().String(),
		TaskID:        task,
		Messages:      msgs,
		Trigger:       string(TriggerPhaseComplete),
		SchemaVersion: 0,
		Metadata:      map[string]interface{}{},
		Timestamp:     float64(time.Now().UnixNano()) / 1e9,
	}
	cpDegradado := downgradeCheckpoint(cpViejo, schemaVersion)
	metaBytes, _ := json.Marshal(cpDegradado.Metadata)
	fmt.Printf("Degradado: v%d→v%d | %s\n", cpViejo.SchemaVersion, cpDegradado.SchemaVersion, metaBytes)

	fmt.Printf("\nshould_checkpoint(phase_complete): %v\n", shouldCheckpoint("phase_complete", 0))
	fmt.Printf("should_checkpoint(periodic, 5 tool calls): %v\n", shouldCheckpoint("periodic", 5))
	fmt.Printf("should_checkpoint(periodic, 25 tool calls): %v\n", shouldCheckpoint("periodic", 25))

	todos, _ := store.List(task)
	fmt.Printf("\nCheckpoints para '%s': %d\n", task, len(todos))
}
