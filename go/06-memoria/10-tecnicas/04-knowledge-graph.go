// Knowledge graph en memoria: entidades, relaciones tipadas y recuperación por BFS.
// Indexación incremental de episodios con extracción mock de entidades y relaciones.
//
// Cómo ejecutar: make go FILE=go/06-memoria/10-tecnicas/04-knowledge-graph.go

package main

import (
	"fmt"
	"strings"
)

type Entidad struct {
	ID     string
	Nombre string
	Tipo   string
}

type Relacion struct {
	DesdeID    string
	HastaID    string
	Tipo       string
	EpisodioID string
}

type Episodio struct {
	ID    string
	Texto string
}

var entidadesMapa = map[string]string{
	"ana":      "persona",
	"acme":     "organización",
	"pegasus":  "proyecto",
	"q3":       "periodo",
	"stripe":   "servicio",
	"q4":       "periodo",
}

var nombreCanonicoMapa = map[string]string{
	"ana": "Ana", "acme": "Acme Corp", "pegasus": "Pegasus",
	"q3": "Q3", "stripe": "Stripe", "q4": "Q4",
}

func entidadID(nombre string) string {
	return strings.ToLower(strings.ReplaceAll(nombre, " ", "_"))
}

func mockNER(texto string) []Entidad {
	textoLow := strings.ToLower(texto)
	var entidades []Entidad
	for clave, tipo := range entidadesMapa {
		if strings.Contains(textoLow, clave) {
			entidades = append(entidades, Entidad{
				ID:     entidadID(clave),
				Nombre: nombreCanonicoMapa[clave],
				Tipo:   tipo,
			})
		}
	}
	return entidades
}

func mockRelaciones(texto string, entidades []Entidad) [][3]string {
	presentes := map[string]bool{}
	for _, e := range entidades {
		presentes[e.ID] = true
	}
	textoLow := strings.ToLower(texto)
	var rels [][3]string
	if presentes["ana"] && presentes["acme"] {
		rels = append(rels, [3]string{"ana", "trabaja_en", "acme"})
	}
	if presentes["ana"] && presentes["q3"] && strings.Contains(textoLow, "aprobó") {
		rels = append(rels, [3]string{"ana", "aprobó", "q3"})
	}
	if presentes["pegasus"] && presentes["stripe"] {
		rels = append(rels, [3]string{"pegasus", "incluye", "stripe"})
	}
	if presentes["q3"] && presentes["pegasus"] {
		rels = append(rels, [3]string{"q3", "financia", "pegasus"})
	}
	if presentes["ana"] && presentes["q4"] {
		rels = append(rels, [3]string{"ana", "aprobó", "q4"})
	}
	return rels
}

type KnowledgeGraph struct {
	entidades         map[string]Entidad
	relaciones        []Relacion
	episodios         map[string]Episodio
	episodioEntidades map[string]map[string]bool
	contador          int
}

func NuevoKG() *KnowledgeGraph {
	return &KnowledgeGraph{
		entidades:         map[string]Entidad{},
		episodios:         map[string]Episodio{},
		episodioEntidades: map[string]map[string]bool{},
	}
}

func (kg *KnowledgeGraph) IndexarEpisodio(texto string) string {
	kg.contador++
	epID := fmt.Sprintf("ep_%d", kg.contador)
	kg.episodios[epID] = Episodio{ID: epID, Texto: texto}
	kg.episodioEntidades[epID] = map[string]bool{}

	entidades := mockNER(texto)
	for _, e := range entidades {
		if _, ok := kg.entidades[e.ID]; !ok {
			kg.entidades[e.ID] = e
		}
		kg.episodioEntidades[epID][e.ID] = true
	}
	for _, rel := range mockRelaciones(texto, entidades) {
		if _, ok := kg.entidades[rel[0]]; !ok {
			continue
		}
		if _, ok := kg.entidades[rel[2]]; !ok {
			continue
		}
		kg.relaciones = append(kg.relaciones, Relacion{
			DesdeID: rel[0], HastaID: rel[2], Tipo: rel[1], EpisodioID: epID,
		})
	}
	return epID
}

func (kg *KnowledgeGraph) RecallPorGrafo(seeds []string, hops int) []Episodio {
	visitados := map[string]bool{}
	for _, s := range seeds {
		id := entidadID(s)
		if _, ok := kg.entidades[id]; ok {
			visitados[id] = true
		}
	}
	frontera := map[string]bool{}
	for id := range visitados {
		frontera[id] = true
	}

	for h := 0; h < hops && len(frontera) > 0; h++ {
		nuevos := map[string]bool{}
		for _, rel := range kg.relaciones {
			if frontera[rel.DesdeID] && !visitados[rel.HastaID] {
				nuevos[rel.HastaID] = true
			}
			if frontera[rel.HastaID] && !visitados[rel.DesdeID] {
				nuevos[rel.DesdeID] = true
			}
		}
		for id := range nuevos {
			visitados[id] = true
		}
		frontera = nuevos
	}

	epIDs := map[string]bool{}
	for epID, entIDs := range kg.episodioEntidades {
		for entID := range entIDs {
			if visitados[entID] {
				epIDs[epID] = true
				break
			}
		}
	}

	var resultados []Episodio
	for epID := range epIDs {
		resultados = append(resultados, kg.episodios[epID])
	}
	return resultados
}

func main() {
	kg := NuevoKG()

	episodios := []string{
		"Ana es la directora de producto de Acme Corp",
		"El proyecto Pegasus tiene deadline en junio",
		"El presupuesto del Q3 fue aprobado por Ana",
		"La integración con Stripe es parte de Pegasus y financia el Q3 del proyecto",
		"Ana aprobó el roadmap del Q4 en la reunión del viernes",
	}

	fmt.Println("Indexando episodios...")
	for _, ep := range episodios {
		id := kg.IndexarEpisodio(ep)
		preview := ep
		if len(preview) > 60 {
			preview = preview[:60]
		}
		fmt.Printf("  [%s] %s\n", id, preview)
	}

	fmt.Printf("\nGrafo: %d entidades, %d relaciones, %d episodios\n\n",
		len(kg.entidades), len(kg.relaciones), len(kg.episodios))

	fmt.Println("Consulta relacional: Seeds=[Q3, Pegasus], hops=2")
	resultados := kg.RecallPorGrafo([]string{"Q3", "Pegasus"}, 2)
	fmt.Printf("Episodios recuperados: %d\n", len(resultados))
	for _, r := range resultados {
		fmt.Printf("  %s\n", r.Texto[:min(len(r.Texto), 80)])
	}

	fmt.Println("\nConsulta: ¿qué sabe el sistema sobre Ana?  Seeds=[Ana], hops=1")
	sobreAna := kg.RecallPorGrafo([]string{"Ana"}, 1)
	for _, r := range sobreAna {
		fmt.Printf("  %s\n", r.Texto[:min(len(r.Texto), 80)])
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
