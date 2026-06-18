// Memoria semántica KV plana sin versionado — para prototipado.
// Última escritura gana, sin tombstones, sin historial de versiones.

// Cómo ejecutar: make go FILE=go/06-memoria/04-semantica/semantic_kv.go

package main

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

type HechoKV struct {
	Clave     string
	Valor     string
	Fuente    string
	Timestamp time.Time
}

type MemoriaSemanticaKV struct {
	hechos map[string]HechoKV
}

func NewMemoriaSemanticaKV() *MemoriaSemanticaKV {
	return &MemoriaSemanticaKV{hechos: make(map[string]HechoKV)}
}

func (m *MemoriaSemanticaKV) SetFact(clave, valor, fuente string) HechoKV {
	h := HechoKV{Clave: clave, Valor: valor, Fuente: fuente, Timestamp: time.Now()}
	m.hechos[clave] = h
	return h
}

func (m *MemoriaSemanticaKV) GetFact(clave string) (string, bool) {
	h, ok := m.hechos[clave]
	if !ok {
		return "", false
	}
	return h.Valor, true
}

func (m *MemoriaSemanticaKV) DeleteFact(clave string) bool {
	_, ok := m.hechos[clave]
	delete(m.hechos, clave)
	return ok
}

func (m *MemoriaSemanticaKV) GetAll() []HechoKV {
	result := make([]HechoKV, 0, len(m.hechos))
	for _, h := range m.hechos {
		result = append(result, h)
	}
	return result
}

func (m *MemoriaSemanticaKV) BuildContextBlock(maxFacts int) string {
	hechos := m.GetAll()
	sort.Slice(hechos, func(i, j int) bool {
		return hechos[i].Timestamp.After(hechos[j].Timestamp)
	})
	if len(hechos) > maxFacts {
		hechos = hechos[:maxFacts]
	}
	if len(hechos) == 0 {
		return ""
	}
	lineas := make([]string, len(hechos))
	for i, h := range hechos {
		lineas[i] = fmt.Sprintf("- %s: %s", h.Clave, h.Valor)
	}
	return "## Perfil del usuario\n" + strings.Join(lineas, "\n")
}

func (m *MemoriaSemanticaKV) Size() int {
	return len(m.hechos)
}

func main() {
	mem := NewMemoriaSemanticaKV()

	mem.SetFact("lenguaje_preferido", "Python", "usuario_directo")
	mem.SetFact("timezone", "Europe/Madrid", "usuario_directo")
	mem.SetFact("estilo_respuesta", "conciso", "auto_extract")
	mem.SetFact("proyecto_actual", "backend de facturación", "auto_extract")

	mem.SetFact("lenguaje_preferido", "TypeScript", "usuario_directo")

	fmt.Printf("Total hechos: %d\n", mem.Size())

	if v, ok := mem.GetFact("lenguaje_preferido"); ok {
		fmt.Printf("lenguaje_preferido: %s\n", v)
	}
	if v, ok := mem.GetFact("timezone"); ok {
		fmt.Printf("timezone: %s\n", v)
	}
	if _, ok := mem.GetFact("no_existe"); !ok {
		fmt.Println("no_existe: <nil>")
	}

	fmt.Printf("\n%s\n", mem.BuildContextBlock(20))
}
