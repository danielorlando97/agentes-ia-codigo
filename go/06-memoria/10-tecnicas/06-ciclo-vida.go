// Ciclo de vida de un recuerdo: decay exponencial, tombstone, consolidación y olvido auditado.
//
// Los cuatro mecanismos que cierran el ciclo de vida de la memoria:
//   1. Decaimiento exponencial por tipo (media vida en días)
//   2. Corrección con tombstone — versiones anteriores se archivan, no se borran
//   3. Consolidación de clusters similares (union-find sobre mock embeddings)
//   4. Olvido auditado — soft delete con trazabilidad completa
//
// Cómo ejecutar: make go FILE=go/06-memoria/10-tecnicas/06-ciclo-vida.go

package main

import (
	"fmt"
	"math"
	"strings"
	"time"
)

var medioVidaDias = map[string]float64{
	"episodio_sesion": 7,
	"preferencia":     30,
	"hecho_usuario":   180,
}

const (
	umbralOlvido = 0.05
)

type Estado = string

const (
	estadoActivo       Estado = "activo"
	estadoTombstone    Estado = "tombstone"
	estadoOlvidado     Estado = "olvidado"
	estadoConsolidado  Estado = "consolidado"
)

type Memoria struct {
	ID              string
	Contenido       string
	Tipo            string
	Estado          Estado
	FuerzaBase      float64
	FuerzaActual    float64
	UltimoUso       float64
	VecesUsado      int
	MedioVidaDias   float64
	ReemplazaA      string
	ReemplazadoPor  string
	RazonCorreccion string
	Creado          float64
	ProcesadoEn     float64
}

var contador int

func genID() string {
	contador++
	return fmt.Sprintf("%08x", contador)
}

func calcularFuerza(fuerzaBase, ultimoUso, medioVida float64) float64 {
	deltaDias := (tiempo() - ultimoUso) / 86400
	return fuerzaBase * math.Exp(-0.693*deltaDias/medioVida)
}

func tiempo() float64 {
	return float64(time.Now().UnixNano()) / 1e9
}

func mockEmbedding(texto string, dim int) []float64 {
	seed := uint32(0)
	for _, c := range texto {
		seed = seed*31 + uint32(c)
	}
	vec := make([]float64, dim)
	for i := range vec {
		seed = seed*1664525 + 1013904223
		vec[i] = float64(seed)/float64(math.MaxUint32)*2 - 1
	}
	norma := 0.0
	for _, v := range vec {
		norma += v * v
	}
	norma = math.Sqrt(norma)
	for i := range vec {
		vec[i] /= norma
	}
	return vec
}

func cosineSim(a, b []float64) float64 {
	dot := 0.0
	for i := range a {
		dot += a[i] * b[i]
	}
	return dot
}

// ── GestorCicloVida ────────────────────────────────────────────────────────

type GestorCicloVida struct {
	almacen map[string]*Memoria
}

func NuevoGestor() *GestorCicloVida {
	return &GestorCicloVida{almacen: map[string]*Memoria{}}
}

func (g *GestorCicloVida) Insertar(contenido, tipo string, fuerzaBase float64) string {
	id := genID()
	ahora := tiempo()
	mv, ok := medioVidaDias[tipo]
	if !ok {
		mv = 90
	}
	g.almacen[id] = &Memoria{
		ID: id, Contenido: contenido, Tipo: tipo,
		Estado: estadoActivo, FuerzaBase: fuerzaBase, FuerzaActual: fuerzaBase,
		UltimoUso: ahora, MedioVidaDias: mv, Creado: ahora,
	}
	return id
}

func (g *GestorCicloVida) Reforzar(id string) {
	m := g.almacen[id]
	if m == nil || m.Estado != estadoActivo {
		return
	}
	m.FuerzaBase = math.Min(1.0, m.FuerzaBase+0.1)
	m.UltimoUso = tiempo()
	m.VecesUsado++
}

func (g *GestorCicloVida) ActualizarDecaimiento() int {
	n := 0
	for _, m := range g.almacen {
		if m.Estado != estadoActivo {
			continue
		}
		m.FuerzaActual = calcularFuerza(m.FuerzaBase, m.UltimoUso, m.MedioVidaDias)
		n++
	}
	return n
}

func (g *GestorCicloVida) Corregir(idAnterior, nuevoContenido, razon string) string {
	anterior, ok := g.almacen[idAnterior]
	if !ok {
		panic("no existe memoria: " + idAnterior)
	}

	nuevoID := genID()
	ahora := tiempo()

	// Insertar sucesor primero (tombstone necesita referencia válida)
	g.almacen[nuevoID] = &Memoria{
		ID: nuevoID, Contenido: nuevoContenido, Tipo: anterior.Tipo,
		Estado: estadoActivo, FuerzaBase: anterior.FuerzaBase, FuerzaActual: anterior.FuerzaActual,
		UltimoUso: ahora, MedioVidaDias: anterior.MedioVidaDias,
		ReemplazaA: idAnterior, Creado: ahora,
	}
	anterior.Estado = estadoTombstone
	anterior.ReemplazadoPor = nuevoID
	anterior.RazonCorreccion = razon

	return nuevoID
}

func (g *GestorCicloVida) ConsolidarClusters(umbral float64) int {
	var activos []*Memoria
	for _, m := range g.almacen {
		if m.Estado == estadoActivo {
			activos = append(activos, m)
		}
	}
	if len(activos) < 2 {
		return 0
	}

	embs := map[string][]float64{}
	for _, m := range activos {
		embs[m.ID] = mockEmbedding(m.Contenido, 32)
	}

	// Union-Find
	parent := map[string]string{}
	for _, m := range activos {
		parent[m.ID] = m.ID
	}
	var find func(string) string
	find = func(x string) string {
		for parent[x] != x {
			parent[x] = parent[parent[x]]
			x = parent[x]
		}
		return x
	}
	union := func(x, y string) { parent[find(x)] = find(y) }

	for i := 0; i < len(activos); i++ {
		for j := i + 1; j < len(activos); j++ {
			sim := cosineSim(embs[activos[i].ID], embs[activos[j].ID])
			if sim >= umbral {
				union(activos[i].ID, activos[j].ID)
			}
		}
	}

	clusters := map[string][]*Memoria{}
	for _, m := range activos {
		raiz := find(m.ID)
		clusters[raiz] = append(clusters[raiz], m)
	}

	consolidaciones := 0
	ahora := tiempo()
	for _, miembros := range clusters {
		if len(miembros) < 2 {
			continue
		}
		cid := genID()
		fuerzaMax := 0.0
		for _, m := range miembros {
			if m.FuerzaBase > fuerzaMax {
				fuerzaMax = m.FuerzaBase
			}
		}
		resumen := fmt.Sprintf("[Consolidado de %d] %s", len(miembros), miembros[0].Contenido)
		g.almacen[cid] = &Memoria{
			ID: cid, Contenido: resumen, Tipo: "hecho_usuario",
			Estado: estadoActivo, FuerzaBase: fuerzaMax, FuerzaActual: fuerzaMax,
			UltimoUso: ahora, MedioVidaDias: 90, Creado: ahora,
		}
		for _, m := range miembros {
			m.Estado = estadoConsolidado
			m.ReemplazadoPor = cid
			m.RazonCorreccion = "consolidación de cluster"
		}
		consolidaciones++
	}
	return consolidaciones
}

func (g *GestorCicloVida) OlvidarDebiles(umbral float64) int {
	n := 0
	ahora := tiempo()
	for _, m := range g.almacen {
		if m.Estado == estadoActivo && m.FuerzaActual < umbral {
			m.Estado = estadoOlvidado
			m.ProcesadoEn = ahora
			n++
		}
	}
	return n
}

func (g *GestorCicloVida) Buscar(k int) []*Memoria {
	var activos []*Memoria
	for _, m := range g.almacen {
		if m.Estado == estadoActivo {
			activos = append(activos, m)
		}
	}
	// Ordenar por fuerza descendente (selection sort simple)
	for i := 0; i < len(activos)-1; i++ {
		for j := i + 1; j < len(activos); j++ {
			if activos[j].FuerzaActual > activos[i].FuerzaActual {
				activos[i], activos[j] = activos[j], activos[i]
			}
		}
	}
	if k > len(activos) {
		k = len(activos)
	}
	return activos[:k]
}

func (g *GestorCicloVida) Historial(id string) []*Memoria {
	var cadena []*Memoria
	m := g.almacen[id]
	for m != nil {
		cadena = append([]*Memoria{m}, cadena...)
		if m.ReemplazaA == "" {
			break
		}
		m = g.almacen[m.ReemplazaA]
	}
	return cadena
}

func (g *GestorCicloVida) Stats() map[string]int {
	s := map[string]int{}
	for _, m := range g.almacen {
		s[m.Estado]++
	}
	return s
}

func trunc(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// ── main ──────────────────────────────────────────────────────────────────

func main() {
	g := NuevoGestor()

	fmt.Println("=== Inserción inicial ===")
	datos := [][3]string{
		{"El usuario trabaja en Empresa A", "hecho_usuario", "0.9"},
		{"El usuario prefiere Python sobre Java", "preferencia", "0.8"},
		{"El usuario mencionó dolor de cabeza hoy", "episodio_sesion", "0.7"},
		{"El usuario habla español como idioma nativo", "hecho_usuario", "1.0"},
		{"El usuario prefiere Python como lenguaje principal", "preferencia", "0.75"},
		{"El usuario usa Python en todos sus proyectos", "preferencia", "0.7"},
		{"La reunión de ayer fue sobre el roadmap del Q3", "episodio_sesion", "0.6"},
		{"El usuario es vegetariano", "hecho_usuario", "0.85"},
	}

	for _, d := range datos {
		fuerza := 0.0
		fmt.Sscanf(d[2], "%f", &fuerza)
		id := g.Insertar(d[0], d[1], fuerza)
		fmt.Printf("  [%s] %s\n", id, trunc(d[0], 50))
	}

	fmt.Printf("\nEstado inicial: %v\n", g.Stats())

	// Simular 14 días en episodios de sesión
	fmt.Println("\n=== Simulando paso del tiempo (episodio_sesion → 14 días) ===")
	hace14Dias := tiempo() - 14*86400
	for _, m := range g.almacen {
		if m.Tipo == "episodio_sesion" {
			m.UltimoUso = hace14Dias
		}
	}
	actualizados := g.ActualizarDecaimiento()
	fmt.Printf("Fuerzas recalculadas para %d recuerdos\n", actualizados)

	// Corrección con tombstone
	fmt.Println("\n=== Corrección con tombstone ===")
	var idEmpresaA string
	for _, m := range g.almacen {
		if strings.Contains(m.Contenido, "Empresa A") {
			idEmpresaA = m.ID
			break
		}
	}
	nuevoID := g.Corregir(idEmpresaA, "El usuario trabaja en Empresa B desde enero 2026",
		"el usuario lo comunicó explícitamente")
	fmt.Printf("  Hecho corregido: %s → tombstone\n", idEmpresaA)
	fmt.Printf("  Nuevo hecho: %s\n", nuevoID)
	for _, h := range g.Historial(nuevoID) {
		fmt.Printf("    [%s] %s\n", h.Estado, trunc(h.Contenido, 50))
	}

	// Consolidación
	fmt.Println("\n=== Consolidación de clusters ===")
	nConsolidaciones := g.ConsolidarClusters(0.6)
	fmt.Printf("  Clusters consolidados: %d\n", nConsolidaciones)
	fmt.Printf("  Estado: %v\n", g.Stats())

	// Olvido auditado
	fmt.Println("\n=== Olvido auditado ===")
	nOlvidados := g.OlvidarDebiles(0.4)
	fmt.Printf("  Recuerdos olvidados: %d\n", nOlvidados)
	fmt.Printf("  Estado final: %v\n", g.Stats())

	fmt.Println("\n=== Recuerdos activos (por fuerza) ===")
	for _, r := range g.Buscar(10) {
		fmt.Printf("  [%.3f] %s\n", r.FuerzaActual, trunc(r.Contenido, 60))
	}
}
