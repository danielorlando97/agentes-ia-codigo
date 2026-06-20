// Mini-proyecto: cognición proactiva con think() + instincts + UrgeQueue.
//
// Un agente con una goroutine de fondo que detecta patrones en el almacén de
// memoria y encola intenciones proactivas (UrgeSpec) que el LLM puede incorporar
// en el siguiente turno — aunque el usuario no haya preguntado por ellas.
//
// Cómo ejecutar:
//
//	export ANTHROPIC_API_KEY=...
//	make go FILE=go/06-memoria/mini-proyecto-cognicion-proactiva/main.go
//
// Qué observar:
//
//	El terminal muestra [💭 N intención(es)] cuando hay urges activas.
//	El agente las incorpora naturalmente (o las ignora si no encajan).
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// ── Parámetros interactivos ────────────────────────────────────────────────

const (
	thinkIntervalSec  = 5             // frecuencia del loop de fondo (reduce a 2 para urges más rápidas)
	maxUrgesPorTurno  = 2             // pon 0 para volver a un agente puramente reactivo
	cooldownDefault   = 60 * time.Second // reduce a 10s para ver repetición de urges
	halfLifeMemoria   = 90 * time.Second // cada 90s una memoria pierde el 50% de fuerza
	umbralDebil       = 0.35
	anthropicModel    = "claude-haiku-4-5-20251001"
	anthropicEndpoint = "https://api.anthropic.com/v1/messages"
)

// ── Tipos ──────────────────────────────────────────────────────────────────

type Memoria struct {
	ID        string
	Contenido string
	Tipo      string
	Fuerza    float64
	UltimoUso time.Time
	Creado    time.Time
}

type UrgeSpec struct {
	CooldownKey string
	Priority    float64 // 0.0–1.0
	Message     string
	Cooldown    time.Duration
}

type UrgeEntry struct {
	Spec      UrgeSpec
	ExpiresAt time.Time
}

// ── Almacén en memoria ─────────────────────────────────────────────────────

type Almacen struct {
	mu          sync.Mutex
	memorias    map[string]*Memoria
	urgeQueue   map[string]*UrgeEntry
	conversacion []struct{ Role, Content string; TS time.Time }
}

func nuevoAlmacen() *Almacen {
	return &Almacen{
		memorias:  make(map[string]*Memoria),
		urgeQueue: make(map[string]*UrgeEntry),
	}
}

func (a *Almacen) RegistrarMemoria(contenido, tipo string) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	id := fmt.Sprintf("%08x", rand.Uint32())
	a.memorias[id] = &Memoria{
		ID: id, Contenido: contenido, Tipo: tipo,
		Fuerza: 1.0, UltimoUso: time.Now(), Creado: time.Now(),
	}
	return id
}

func (a *Almacen) RecuperarMemorias(limit int) []*Memoria {
	a.mu.Lock()
	defer a.mu.Unlock()
	var activas []*Memoria
	for _, m := range a.memorias {
		if m.Fuerza > 0.1 {
			activas = append(activas, m)
		}
	}
	sort.Slice(activas, func(i, j int) bool { return activas[i].Fuerza > activas[j].Fuerza })
	if len(activas) > limit {
		activas = activas[:limit]
	}
	return activas
}

func (a *Almacen) AplicarDecay() {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	hl := halfLifeMemoria.Seconds()
	for _, m := range a.memorias {
		delta := now.Sub(m.UltimoUso).Seconds()
		m.Fuerza = m.Fuerza * math.Exp(-0.693*delta/hl)
	}
}

func (a *Almacen) MemoriasDebiles() []*Memoria {
	a.mu.Lock()
	defer a.mu.Unlock()
	var debiles []*Memoria
	for _, m := range a.memorias {
		if m.Fuerza < umbralDebil && m.Fuerza > 0.05 {
			debiles = append(debiles, m)
		}
	}
	sort.Slice(debiles, func(i, j int) bool { return debiles[i].Fuerza < debiles[j].Fuerza })
	if len(debiles) > 3 {
		debiles = debiles[:3]
	}
	return debiles
}

func (a *Almacen) ContarMemorias() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	n := 0
	for _, m := range a.memorias {
		if m.Fuerza > 0.1 {
			n++
		}
	}
	return n
}

func (a *Almacen) TemasRecientes(desde time.Duration) []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	cutoff := time.Now().Add(-desde)
	var temas []string
	for i := len(a.conversacion) - 1; i >= 0; i-- {
		c := a.conversacion[i]
		if c.TS.Before(cutoff) {
			break
		}
		if c.Role == "user" {
			temas = append(temas, c.Content)
		}
		if len(temas) >= 5 {
			break
		}
	}
	return temas
}

func (a *Almacen) RegistrarTurno(role, content string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.conversacion = append(a.conversacion, struct{ Role, Content string; TS time.Time }{role, content, time.Now()})
}

// ── UrgeQueue ──────────────────────────────────────────────────────────────

func (a *Almacen) EncolarUrge(spec UrgeSpec) {
	a.mu.Lock()
	defer a.mu.Unlock()
	existing, ok := a.urgeQueue[spec.CooldownKey]
	if !ok || spec.Priority > existing.Spec.Priority {
		a.urgeQueue[spec.CooldownKey] = &UrgeEntry{
			Spec:      spec,
			ExpiresAt: time.Now().Add(spec.Cooldown),
		}
	}
}

func (a *Almacen) ExtraerUrges(limit int) []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	type kv struct {
		key   string
		entry *UrgeEntry
	}
	var activas []kv
	for k, e := range a.urgeQueue {
		if e.ExpiresAt.After(now) {
			activas = append(activas, kv{k, e})
		}
	}
	sort.Slice(activas, func(i, j int) bool {
		return activas[i].entry.Spec.Priority > activas[j].entry.Spec.Priority
	})
	if len(activas) > limit {
		activas = activas[:limit]
	}
	var mensajes []string
	for _, kv := range activas {
		mensajes = append(mensajes, kv.entry.Spec.Message)
		delete(a.urgeQueue, kv.key)
	}
	return mensajes
}

// ── Instincts ──────────────────────────────────────────────────────────────

func instinctMemoriaDebil(a *Almacen) []UrgeSpec {
	debiles := a.MemoriasDebiles()
	if len(debiles) == 0 {
		return nil
	}
	m := debiles[0]
	contenido := m.Contenido
	if len(contenido) > 60 {
		contenido = contenido[:60]
	}
	return []UrgeSpec{{
		CooldownKey: "memoria_debil",
		Priority:    0.7,
		Message:     fmt.Sprintf("[PROACTIVO] El recuerdo '%s' está perdiendo relevancia (fuerza: %.2f). Menciónalo si el contexto lo permite.", contenido, m.Fuerza),
		Cooldown:    cooldownDefault,
	}}
}

func instinctTemasPendientes(a *Almacen) []UrgeSpec {
	temas := a.TemasRecientes(5 * time.Minute)
	if len(temas) < 3 {
		return nil
	}
	return []UrgeSpec{{
		CooldownKey: "temas_pendientes",
		Priority:    0.5,
		Message:     fmt.Sprintf("[PROACTIVO] El usuario ha mencionado %d temas distintos en esta sesión. ¿Hay algún hilo que quedó sin resolver?", len(temas)),
		Cooldown:    cooldownDefault * 2,
	}}
}

func instinctCargaAlta(a *Almacen) []UrgeSpec {
	n := a.ContarMemorias()
	if n < 4 {
		return nil
	}
	return []UrgeSpec{{
		CooldownKey: "carga_alta",
		Priority:    0.3,
		Message:     fmt.Sprintf("[PROACTIVO] Tengo %d recuerdos activos sobre este usuario. Si pregunta sobre el pasado, tengo contexto relevante disponible.", n),
		Cooldown:    cooldownDefault * 3,
	}}
}

// Comenta un instinct para desactivar ese tipo de proactividad
type instinctFn func(*Almacen) []UrgeSpec

var instincts = []instinctFn{
	instinctMemoriaDebil,
	instinctTemasPendientes,
	instinctCargaAlta,
}

// ── BackgroundCognition ────────────────────────────────────────────────────

func iniciarBackgroundCognition(a *Almacen, done <-chan struct{}) {
	go func() {
		ticker := time.NewTicker(thinkIntervalSec * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				a.AplicarDecay()
				for _, fn := range instincts {
					for _, spec := range fn(a) {
						a.EncolarUrge(spec)
					}
				}
			}
		}
	}()
}

// ── Anthropic API ──────────────────────────────────────────────────────────

type anthropicMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicReq struct {
	Model     string         `json:"model"`
	MaxTokens int            `json:"max_tokens"`
	System    string         `json:"system"`
	Messages  []anthropicMsg `json:"messages"`
}

type anthropicResp struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

func llamarAnthropic(system string, messages []anthropicMsg) (string, error) {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	payload := anthropicReq{
		Model:     anthropicModel,
		MaxTokens: 1024,
		System:    system,
		Messages:  messages,
	}
	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", anthropicEndpoint, bytes.NewReader(body))
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("content-type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)

	var ar anthropicResp
	if err := json.Unmarshal(data, &ar); err != nil || len(ar.Content) == 0 {
		return "", fmt.Errorf("respuesta inesperada: %s", data)
	}
	return ar.Content[0].Text, nil
}

// ── Loop de chat ───────────────────────────────────────────────────────────

func construirSystem(urges []string, mems []*Memoria) string {
	var sb strings.Builder
	sb.WriteString("Eres un asistente con memoria persistente y cognición proactiva.")

	if len(mems) > 0 {
		sb.WriteString("\n\n## Memoria activa\n")
		for _, m := range mems {
			fmt.Fprintf(&sb, "- %s (fuerza: %.2f)\n", m.Contenido, m.Fuerza)
		}
	}

	if len(urges) > 0 {
		sb.WriteString("\n## Intenciones proactivas\n")
		for _, u := range urges {
			fmt.Fprintf(&sb, "- %s\n", u)
		}
		sb.WriteString("\nIncorpora estas intenciones de forma natural si el contexto lo permite. Si no encajan con la pregunta del usuario, ignóralas.")
	}

	return sb.String()
}

func chat(a *Almacen) {
	scanner := bufio.NewScanner(os.Stdin)
	var historial []anthropicMsg

	fmt.Printf("\nAgente con cognición proactiva listo.\n")
	fmt.Printf("  Loop de fondo: cada %ds | Máx %d urges/turno\n", thinkIntervalSec, maxUrgesPorTurno)
	fmt.Println("  Escribe 'salir' para terminar.\n")

	for {
		fmt.Print("Tú: ")
		if !scanner.Scan() {
			break
		}
		entrada := strings.TrimSpace(scanner.Text())
		if entrada == "" || entrada == "salir" || entrada == "exit" {
			break
		}

		a.RegistrarTurno("user", entrada)

		urges := a.ExtraerUrges(maxUrgesPorTurno)
		if len(urges) > 0 {
			fmt.Printf("\n  [💭 %d intención(es) proactiva(s) activa(s)]\n", len(urges))
		}

		mems := a.RecuperarMemorias(5)
		system := construirSystem(urges, mems)

		historial = append(historial, anthropicMsg{Role: "user", Content: entrada})

		texto, err := llamarAnthropic(system, historial)
		if err != nil {
			fmt.Printf("  [error API: %v]\n", err)
			continue
		}

		historial = append(historial, anthropicMsg{Role: "assistant", Content: texto})
		a.RegistrarTurno("assistant", texto)

		if len(entrada) > 15 {
			trunc := entrada
			if len(trunc) > 80 {
				trunc = trunc[:80]
			}
			a.RegistrarMemoria("El usuario dijo: "+trunc, "episodio")
		}

		fmt.Printf("\nAgente: %s\n\n", texto)
	}
}

// ── Main ───────────────────────────────────────────────────────────────────

func main() {
	a := nuevoAlmacen()

	// Semilla de memorias para hacer el demo inmediatamente interesante
	a.RegistrarMemoria("El usuario prefiere respuestas concisas sin relleno", "preferencia")
	a.RegistrarMemoria("Proyecto activo: sistema de agentes con memoria distribuida", "proyecto")
	a.RegistrarMemoria("Tarea pendiente: revisar el diseño del ciclo de vida", "tarea")
	a.RegistrarMemoria("Nota de hace tiempo: el usuario usa Go en producción", "episodio")

	done := make(chan struct{})
	iniciarBackgroundCognition(a, done)

	chat(a)

	close(done)
	fmt.Println("[BackgroundCognition detenida]")
}
