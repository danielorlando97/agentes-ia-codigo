// Cómo ejecutar: make ts SCRIPT=typescript/07-estado-contexto/persistencia.ts
import { Database } from "bun:sqlite";
import { randomUUID } from "crypto";

const SCHEMA_VERSION = 1;

enum CheckpointTrigger {
  PHASE_COMPLETE   = "phase_complete",
  IRREVERSIBLE_ACT = "irreversible_act",
  USER_REQUEST     = "user_request",
  BUDGET_THRESHOLD = "budget_threshold",
  PERIODIC         = "periodic",
}

interface CheckpointData {
  id: string;
  task_id: string;
  messages: object[];
  trigger: string;
  schema_version: number;
  metadata: Record<string, unknown>;
  timestamp: number;
}

class Checkpoint implements CheckpointData {
  id: string;
  task_id: string;
  messages: object[];
  trigger: string;
  schema_version: number;
  metadata: Record<string, unknown>;
  timestamp: number;

  constructor(data: Partial<CheckpointData> & { task_id: string; messages: object[] }) {
    this.id             = data.id             ?? randomUUID();
    this.task_id        = data.task_id;
    this.messages       = data.messages;
    this.trigger        = data.trigger        ?? CheckpointTrigger.PHASE_COMPLETE;
    this.schema_version = data.schema_version ?? SCHEMA_VERSION;
    this.metadata       = data.metadata       ?? {};
    this.timestamp      = data.timestamp      ?? Date.now() / 1000;
  }

  toJSON(): string {
    return JSON.stringify({
      id: this.id, task_id: this.task_id, messages: this.messages,
      trigger: this.trigger, schema_version: this.schema_version,
      metadata: this.metadata, timestamp: this.timestamp,
    });
  }

  static fromJSON(data: string): Checkpoint {
    return new Checkpoint(JSON.parse(data));
  }
}

abstract class CheckpointStore {
  abstract save(checkpoint: Checkpoint): string;
  abstract load(checkpointId: string): Checkpoint | null;
  abstract list(taskId: string): Checkpoint[];
  abstract latest(taskId: string): Checkpoint | null;
}

class SQLiteCheckpointStore extends CheckpointStore {
  private db: Database;

  constructor(dbPath = ":memory:") {
    super();
    this.db = new Database(dbPath);
    this.db.exec(`
      CREATE TABLE IF NOT EXISTS checkpoints (
        id             TEXT PRIMARY KEY,
        task_id        TEXT NOT NULL,
        schema_version INTEGER NOT NULL,
        trigger        TEXT NOT NULL,
        timestamp      REAL NOT NULL,
        data           TEXT NOT NULL
      )
    `);
    this.db.exec(
      "CREATE INDEX IF NOT EXISTS idx_task ON checkpoints(task_id, timestamp)"
    );
  }

  save(checkpoint: Checkpoint): string {
    this.db
      .prepare("INSERT INTO checkpoints VALUES (?,?,?,?,?,?)")
      .run(
        checkpoint.id,
        checkpoint.task_id,
        checkpoint.schema_version,
        checkpoint.trigger,
        checkpoint.timestamp,
        checkpoint.toJSON()
      );
    return checkpoint.id;
  }

  load(checkpointId: string): Checkpoint | null {
    const row = this.db
      .prepare("SELECT data, schema_version FROM checkpoints WHERE id=?")
      .get(checkpointId) as { data: string; schema_version: number } | undefined;
    if (!row) return null;
    let cp = Checkpoint.fromJSON(row.data);
    if (cp.schema_version !== SCHEMA_VERSION) {
      cp = downgradeCheckpoint(cp, SCHEMA_VERSION);
    }
    return cp;
  }

  list(taskId: string): Checkpoint[] {
    const rows = this.db
      .prepare("SELECT data FROM checkpoints WHERE task_id=? ORDER BY timestamp ASC")
      .all(taskId) as { data: string }[];
    return rows.map((r) => Checkpoint.fromJSON(r.data));
  }

  latest(taskId: string): Checkpoint | null {
    const row = this.db
      .prepare("SELECT data FROM checkpoints WHERE task_id=? ORDER BY timestamp DESC LIMIT 1")
      .get(taskId) as { data: string } | undefined;
    return row ? Checkpoint.fromJSON(row.data) : null;
  }
}

function downgradeCheckpoint(checkpoint: Checkpoint, targetVersion: number): Checkpoint {
  if (checkpoint.schema_version === targetVersion) return checkpoint;
  return new Checkpoint({
    task_id:        checkpoint.task_id,
    messages:       checkpoint.messages,
    trigger:        "downgraded_from_v" + checkpoint.schema_version,
    schema_version: targetVersion,
    metadata:       { downgraded: true, original_version: checkpoint.schema_version },
    id:             checkpoint.id,
    timestamp:      checkpoint.timestamp,
  });
}

function shouldCheckpoint(trigger: string, toolCallsSinceLast = 0): boolean {
  const primaryTriggers = new Set([
    CheckpointTrigger.PHASE_COMPLETE,
    CheckpointTrigger.IRREVERSIBLE_ACT,
    CheckpointTrigger.USER_REQUEST,
  ]);
  if (primaryTriggers.has(trigger as CheckpointTrigger)) return true;
  if (trigger === CheckpointTrigger.PERIODIC) return toolCallsSinceLast >= 20;
  return false;
}

const store = new SQLiteCheckpointStore();
const task = "analisis-repo-facturacion";

const msgs = [
  { role: "user",      content: "Analiza el módulo de auth." },
  { role: "assistant", content: "Encontré 2 vulnerabilidades en auth.py." },
];

const cp1 = new Checkpoint({
  task_id:  task,
  messages: msgs,
  trigger:  CheckpointTrigger.PHASE_COMPLETE,
  metadata: { fase: 1, vulnerabilidades: 2 },
});
const id1 = store.save(cp1);
console.log(`Guardado: ${id1.slice(0, 8)}... | trigger=${cp1.trigger}`);

const cargado = store.load(id1)!;
console.log(`Cargado: task=${cargado.task_id} | msgs=${cargado.messages.length} | v=${cargado.schema_version}`);

const cpViejo = new Checkpoint({ task_id: task, messages: msgs, schema_version: 0 });
const cpDegradado = downgradeCheckpoint(cpViejo, SCHEMA_VERSION);
console.log(`Degradado: v${cpViejo.schema_version}→v${cpDegradado.schema_version} | ${JSON.stringify(cpDegradado.metadata)}`);

console.log(`\nshould_checkpoint(phase_complete): ${shouldCheckpoint("phase_complete")}`);
console.log(`should_checkpoint(periodic, 5 tool calls): ${shouldCheckpoint("periodic", 5)}`);
console.log(`should_checkpoint(periodic, 25 tool calls): ${shouldCheckpoint("periodic", 25)}`);

const todos = store.list(task);
console.log(`\nCheckpoints para '${task}': ${todos.length}`);
