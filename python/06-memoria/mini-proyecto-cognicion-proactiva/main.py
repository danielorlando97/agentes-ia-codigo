"""Mini-proyecto: cognición proactiva con think() + instincts + UrgeQueue.

Un agente con un loop de fondo que detecta patrones en el almacén de memoria
y encola intenciones proactivas (UrgeSpec) que el LLM puede incorporar en el
siguiente turno conversacional — aunque el usuario no haya preguntado por ellas.

Cómo ejecutar:
    export ANTHROPIC_API_KEY=...
    make py SCRIPT=python/06-memoria/mini-proyecto-cognicion-proactiva/main.py

Qué observar:
    El terminal muestra [💭 N intención(es)] cuando hay urges activas.
    El agente las incorpora naturalmente (o las ignora si no encajan).

Parámetros ajustables — cambia y observa el comportamiento:
"""

import math
import os
import sqlite3
import threading
import time
import uuid
from dataclasses import dataclass

import anthropic

# ── Parámetros interactivos ────────────────────────────────────────────────

THINK_INTERVAL_SECONDS = 5    # frecuencia del loop de fondo (reduce a 2 para urges más rápidas)
MAX_URGES_POR_TURNO    = 2    # intenciones proactivas por turno (pon 0 para agente puramente reactivo)
COOLDOWN_DEFAULT       = 60   # segundos antes de que una urge pueda repetirse (reduce a 10 para ver repetición)
HALF_LIFE_MEMORIA      = 90   # segundos hasta que una memoria llega al 50% de fuerza (demo: segundos, no días)
UMBRAL_DEBIL           = 0.35 # por debajo de este umbral → candidata a urge de "recuerdo débil"

MODEL = os.environ.get("MODEL", "claude-haiku-4-5-20251001")
DB_PATH = "/tmp/cognicion-proactiva-demo.db"


# ── Estructuras de datos ───────────────────────────────────────────────────

@dataclass
class UrgeSpec:
    cooldown_key: str       # identidad de la urge — evita duplicados en la cola
    priority: float         # 0.0–1.0; las de mayor prioridad salen primero
    message: str            # sugerencia para el LLM (no instrucción obligatoria)
    cooldown_seconds: int = COOLDOWN_DEFAULT


# ── Esquema SQL ────────────────────────────────────────────────────────────

ESQUEMA = """
CREATE TABLE IF NOT EXISTS memorias (
    id       TEXT PRIMARY KEY,
    contenido TEXT NOT NULL,
    tipo     TEXT NOT NULL DEFAULT 'hecho',
    fuerza   REAL NOT NULL DEFAULT 1.0,
    ultimo_uso REAL NOT NULL DEFAULT (unixepoch()),
    creado   REAL NOT NULL DEFAULT (unixepoch())
);
CREATE TABLE IF NOT EXISTS urge_queue (
    cooldown_key TEXT PRIMARY KEY,
    priority     REAL NOT NULL,
    message      TEXT NOT NULL,
    expires_at   REAL NOT NULL
);
CREATE TABLE IF NOT EXISTS conversacion (
    id      INTEGER PRIMARY KEY AUTOINCREMENT,
    role    TEXT NOT NULL,
    content TEXT NOT NULL,
    ts      REAL NOT NULL DEFAULT (unixepoch())
);
"""


def init_db(conn: sqlite3.Connection) -> None:
    conn.executescript(ESQUEMA)
    conn.commit()


# ── Operaciones de memoria ─────────────────────────────────────────────────

def registrar_memoria(conn: sqlite3.Connection, contenido: str, tipo: str = "hecho") -> str:
    mid = str(uuid.uuid4())[:8]
    conn.execute(
        "INSERT INTO memorias (id, contenido, tipo) VALUES (?, ?, ?)",
        (mid, contenido, tipo),
    )
    conn.commit()
    return mid


def recuperar_memorias(conn: sqlite3.Connection, limit: int = 5) -> list[dict]:
    rows = conn.execute(
        "SELECT id, contenido, tipo, fuerza FROM memorias WHERE fuerza > 0.1 ORDER BY fuerza DESC LIMIT ?",
        (limit,),
    ).fetchall()
    return [{"id": r[0], "contenido": r[1], "tipo": r[2], "fuerza": r[3]} for r in rows]


def aplicar_decay(conn: sqlite3.Connection) -> None:
    now = time.time()
    conn.execute(
        """UPDATE memorias
           SET fuerza = fuerza * exp(-0.693 * (? - ultimo_uso) / ?)
           WHERE fuerza > 0.01""",
        (now, HALF_LIFE_MEMORIA),
    )
    conn.commit()


def memorias_debiles(conn: sqlite3.Connection) -> list[dict]:
    rows = conn.execute(
        "SELECT id, contenido, fuerza FROM memorias WHERE fuerza < ? AND fuerza > 0.05 ORDER BY fuerza ASC LIMIT 3",
        (UMBRAL_DEBIL,),
    ).fetchall()
    return [{"id": r[0], "contenido": r[1], "fuerza": r[2]} for r in rows]


def contar_memorias(conn: sqlite3.Connection) -> int:
    return conn.execute("SELECT COUNT(*) FROM memorias WHERE fuerza > 0.1").fetchone()[0]


def temas_recientes(conn: sqlite3.Connection, segundos: int = 300) -> list[str]:
    desde = time.time() - segundos
    rows = conn.execute(
        "SELECT content FROM conversacion WHERE ts > ? AND role = 'user' ORDER BY ts DESC LIMIT 5",
        (desde,),
    ).fetchall()
    return [r[0] for r in rows]


# ── UrgeQueue ──────────────────────────────────────────────────────────────

def encolar_urge(conn: sqlite3.Connection, spec: UrgeSpec) -> None:
    expires_at = time.time() + spec.cooldown_seconds
    conn.execute(
        """INSERT INTO urge_queue (cooldown_key, priority, message, expires_at)
           VALUES (?, ?, ?, ?)
           ON CONFLICT(cooldown_key) DO UPDATE SET
               priority = excluded.priority,
               message  = excluded.message,
               expires_at = excluded.expires_at
           WHERE excluded.priority > urge_queue.priority""",
        (spec.cooldown_key, spec.priority, spec.message, expires_at),
    )
    conn.commit()


def extraer_urges(conn: sqlite3.Connection, limit: int = MAX_URGES_POR_TURNO) -> list[str]:
    now = time.time()
    rows = conn.execute(
        "SELECT cooldown_key, message FROM urge_queue WHERE expires_at > ? ORDER BY priority DESC LIMIT ?",
        (now, limit),
    ).fetchall()
    if not rows:
        return []
    keys = [r[0] for r in rows]
    conn.execute(
        f"DELETE FROM urge_queue WHERE cooldown_key IN ({','.join('?' * len(keys))})",
        keys,
    )
    conn.commit()
    return [r[1] for r in rows]


# ── Instincts ──────────────────────────────────────────────────────────────

def instinct_memoria_debil(conn: sqlite3.Connection) -> list[UrgeSpec]:
    """Surfacea recuerdos próximos a ser olvidados."""
    debiles = memorias_debiles(conn)
    if not debiles:
        return []
    m = debiles[0]
    return [UrgeSpec(
        cooldown_key="memoria_debil",
        priority=0.7,
        message=f"[PROACTIVO] El recuerdo '{m['contenido'][:60]}' está perdiendo relevancia (fuerza: {m['fuerza']:.2f}). Menciónalo si el contexto lo permite.",
        cooldown_seconds=COOLDOWN_DEFAULT,
    )]


def instinct_temas_pendientes(conn: sqlite3.Connection) -> list[UrgeSpec]:
    """Detecta conversaciones recientes con múltiples temas sin seguimiento."""
    temas = temas_recientes(conn, segundos=300)
    if len(temas) < 3:
        return []
    return [UrgeSpec(
        cooldown_key="temas_pendientes",
        priority=0.5,
        message=f"[PROACTIVO] El usuario ha mencionado {len(temas)} temas distintos en esta sesión. ¿Hay algún hilo que quedó sin resolver?",
        cooldown_seconds=COOLDOWN_DEFAULT * 2,
    )]


def instinct_carga_alta(conn: sqlite3.Connection) -> list[UrgeSpec]:
    """Avisa cuando el almacén tiene muchos recuerdos activos."""
    n = contar_memorias(conn)
    if n < 4:
        return []
    return [UrgeSpec(
        cooldown_key="carga_alta",
        priority=0.3,
        message=f"[PROACTIVO] Tengo {n} recuerdos activos sobre este usuario. Si pregunta sobre el pasado, tengo contexto relevante disponible.",
        cooldown_seconds=COOLDOWN_DEFAULT * 3,
    )]


# Lista de instincts activos — comenta uno para desactivar ese tipo de proactividad
INSTINCTS = [
    instinct_memoria_debil,
    instinct_temas_pendientes,
    instinct_carga_alta,
]


# ── BackgroundCognition ────────────────────────────────────────────────────

class BackgroundCognition(threading.Thread):
    def __init__(self, db_path: str, interval: float = THINK_INTERVAL_SECONDS) -> None:
        super().__init__(daemon=True)
        self.db_path = db_path
        self.interval = interval
        self._stop = threading.Event()

    def stop(self) -> None:
        self._stop.set()

    def run(self) -> None:
        while not self._stop.is_set():
            try:
                conn = sqlite3.connect(self.db_path)
                aplicar_decay(conn)
                for instinct in INSTINCTS:
                    for spec in instinct(conn):
                        encolar_urge(conn, spec)
                conn.close()
            except Exception as e:
                print(f"  [BackgroundCognition error: {e}]")
            self._stop.wait(self.interval)


# ── Loop de chat ───────────────────────────────────────────────────────────

def construir_system(urges: list[str], memorias: list[dict]) -> str:
    partes = ["Eres un asistente con memoria persistente y cognición proactiva."]

    if memorias:
        mem_txt = "\n".join(f"- {m['contenido']} (fuerza: {m['fuerza']:.2f})" for m in memorias)
        partes.append(f"\n## Memoria activa\n{mem_txt}")

    if urges:
        urge_txt = "\n".join(f"- {u}" for u in urges)
        partes.append(
            f"\n## Intenciones proactivas\n{urge_txt}\n\n"
            "Incorpora estas intenciones de forma natural si el contexto lo permite. "
            "Si no encajan con la pregunta del usuario, ignóralas."
        )

    return "\n".join(partes)


def chat(db_path: str) -> None:
    client = anthropic.Anthropic()
    conn = sqlite3.connect(db_path)
    historial: list[dict] = []

    print(f"\nAgente con cognición proactiva listo.")
    print(f"  Loop de fondo: cada {THINK_INTERVAL_SECONDS}s | Máx {MAX_URGES_POR_TURNO} urges/turno | Cooldown: {COOLDOWN_DEFAULT}s")
    print("  Escribe 'salir' para terminar.\n")

    while True:
        try:
            entrada = input("Tú: ").strip()
        except (EOFError, KeyboardInterrupt):
            break
        if entrada.lower() in ("salir", "exit", "quit"):
            break
        if not entrada:
            continue

        conn.execute(
            "INSERT INTO conversacion (role, content, ts) VALUES ('user', ?, ?)",
            (entrada, time.time()),
        )
        conn.commit()

        urges = extraer_urges(conn)
        if urges:
            print(f"\n  [💭 {len(urges)} intención(es) proactiva(s) activa(s)]")

        memorias = recuperar_memorias(conn)
        system = construir_system(urges, memorias)

        historial.append({"role": "user", "content": entrada})

        respuesta = client.messages.create(
            model=MODEL,
            max_tokens=1024,
            system=system,
            messages=historial,
        )
        texto = respuesta.content[0].text
        historial.append({"role": "assistant", "content": texto})

        conn.execute(
            "INSERT INTO conversacion (role, content, ts) VALUES ('assistant', ?, ?)",
            (texto, time.time()),
        )
        if len(entrada) > 15:
            registrar_memoria(conn, f"El usuario dijo: {entrada[:80]}", tipo="episodio")

        print(f"\nAgente: {texto}\n")

    conn.close()


def main() -> None:
    if os.path.exists(DB_PATH):
        os.remove(DB_PATH)

    conn = sqlite3.connect(DB_PATH)
    init_db(conn)

    # Semilla de memorias para hacer el demo inmediatamente interesante
    registrar_memoria(conn, "El usuario prefiere respuestas concisas sin relleno", tipo="preferencia")
    registrar_memoria(conn, "Proyecto activo: sistema de agentes con memoria distribuida", tipo="proyecto")
    registrar_memoria(conn, "Tarea pendiente: revisar el diseño del ciclo de vida", tipo="tarea")
    registrar_memoria(conn, "Nota de hace tiempo: el usuario mencionó que usa Python en producción", tipo="episodio")
    conn.close()

    bg = BackgroundCognition(DB_PATH, interval=THINK_INTERVAL_SECONDS)
    bg.start()

    try:
        chat(DB_PATH)
    finally:
        bg.stop()
        print("[BackgroundCognition detenida]")


if __name__ == "__main__":
    main()
