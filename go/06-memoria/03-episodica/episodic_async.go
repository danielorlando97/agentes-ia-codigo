// Pipeline post-turno con canal Go (equivalente a asyncio.Queue).
// El turno del agente responde sin bloquear; el aprendizaje episódico
// ocurre en una goroutine. El canal desacopla producción de consumo.

// Cómo ejecutar: make go FILE=go/06-memoria/03-episodica/episodic_async.go

package main

import (
	"fmt"
	"sync"
	"time"
)

type TareaAprendizaje struct {
	RawText  string
	SesionID string
	Timestamp time.Time
}

type Almacen interface {
	Append(contenido, sesionID string)
}

type LogSimple struct {
	mu       sync.Mutex
	Entradas []struct{ SesionID, Contenido string }
}

func (l *LogSimple) Append(contenido, sesionID string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.Entradas = append(l.Entradas, struct{ SesionID, Contenido string }{sesionID, contenido})
}

type ExtractorFn func(tarea TareaAprendizaje) ([]string, error)

func extractorBasico(tarea TareaAprendizaje) ([]string, error) {
	if len(tarea.RawText) <= 20 {
		return nil, nil
	}
	return []string{tarea.RawText}, nil
}

type PipelineEpisodico struct {
	queue     chan TareaAprendizaje
	almacen   Almacen
	extractor ExtractorFn
	wg        sync.WaitGroup
	Processed int
	Dropped   int
	mu        sync.Mutex
}

func NewPipelineEpisodico(almacen Almacen, extractor ExtractorFn, maxsize int) *PipelineEpisodico {
	if extractor == nil {
		extractor = extractorBasico
	}
	return &PipelineEpisodico{
		queue:     make(chan TareaAprendizaje, maxsize),
		almacen:   almacen,
		extractor: extractor,
	}
}

func (p *PipelineEpisodico) Start() {
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		for tarea := range p.queue {
			episodios, err := p.extractor(tarea)
			if err != nil {
				continue
			}
			for _, ep := range episodios {
				p.almacen.Append(ep, tarea.SesionID)
				p.mu.Lock()
				p.Processed++
				p.mu.Unlock()
			}
		}
	}()
}

func (p *PipelineEpisodico) Submit(rawText, sesionID string) bool {
	select {
	case p.queue <- TareaAprendizaje{RawText: rawText, SesionID: sesionID, Timestamp: time.Now()}:
		return true
	default:
		p.mu.Lock()
		p.Dropped++
		p.mu.Unlock()
		return false
	}
}

func (p *PipelineEpisodico) Stop() {
	close(p.queue)
	p.wg.Wait()
}

func turnoAgente(pipeline *PipelineEpisodico, mensaje, sesionID string) string {
	n := len(mensaje)
	if n > 40 {
		n = 40
	}
	respuesta := "Entendido: '" + mensaje[:n] + "'"
	pipeline.Submit(mensaje, sesionID)
	return respuesta
}

func main() {
	almacen := &LogSimple{}
	pipeline := NewPipelineEpisodico(almacen, nil, 100)
	pipeline.Start()

	sesion := "demo"
	mensajes := []string{
		"El usuario usa Python 3.12 en producción",
		"Bug en auth.py línea 247: condición invertida",
		"ok",
		"Decidimos usar PostgreSQL para producción",
		"El módulo de billing tiene deuda técnica",
	}

	t0 := time.Now()
	for _, msg := range mensajes {
		resp := turnoAgente(pipeline, msg, sesion)
		fmt.Printf("  turno: %s\n", resp)
	}

	pipeline.Stop()
	elapsed := time.Since(t0)

	fmt.Printf("\nEpisodios guardados: %d | descartados: %d\n", pipeline.Processed, pipeline.Dropped)
	fmt.Printf("Tiempo total del loop: %.3fs\n\n", elapsed.Seconds())

	almacen.mu.Lock()
	defer almacen.mu.Unlock()
	for _, e := range almacen.Entradas {
		contenido := e.Contenido
		if len(contenido) > 60 {
			contenido = contenido[:60]
		}
		fmt.Printf("  [%s] %s\n", e.SesionID, contenido)
	}
}
