// LLM Compiler (Kim et al. 2023, arXiv:2312.04511) — ejecución paralela de DAG.
//
// Planner LLM genera un plan con $idx como dependencias implícitas.
// Task Fetching Unit schedula con canales cerrados (broadcast equivalente a asyncio.Event).
// Joiner decide Finish o Replan.
//
// Requiere: ANTHROPIC_API_KEY en el entorno.

// Cómo ejecutar: make go FILE=go/09-planificacion/llm_compiler.go

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
)

const (
	maxRepl  = 3
)

var (
	model = envOr("MODEL", "claude-sonnet-4-6")
	endpoint = envBaseURL()
)

// --- API types ---

type apiMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type apiReq struct {
	Model     string   `json:"model"`
	MaxTokens int      `json:"max_tokens"`
	System    string   `json:"system,omitempty"`
	Messages  []apiMsg `json:"messages"`
}

type apiBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type apiResp struct {
	Content []apiBlock `json:"content"`
}

func llmCall(system string, msgs []apiMsg, maxTokens int) (string, error) {
	body, _ := json.Marshal(apiReq{
		Model:     model,
		MaxTokens: maxTokens,
		System:    system,
		Messages:  msgs,
	})

	req, _ := http.NewRequest("POST", endpoint, bytes.NewReader(body))
	req.Header.Set("x-api-key", os.Getenv("ANTHROPIC_API_KEY"))
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("content-type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	var r apiResp
	if err := json.Unmarshal(raw, &r); err != nil || len(r.Content) == 0 {
		return "", fmt.Errorf("respuesta inválida: %s", raw)
	}
	return strings.TrimSpace(r.Content[0].Text), nil
}

// --- Plan types ---

type Tarea struct {
	idx  int
	tool string
	args string
	deps []int
}

var planLineRe = regexp.MustCompile(`^(\d+)\.\s+(\w+)\(([^)]*)\)`)
var depRe = regexp.MustCompile(`\$(\d+)`)

func parsearPlan(texto string) ([]Tarea, error) {
	var tareas []Tarea
	for _, linea := range strings.Split(texto, "\n") {
		m := planLineRe.FindStringSubmatch(strings.TrimSpace(linea))
		if m == nil {
			continue
		}
		idx, _ := strconv.Atoi(m[1])
		tool := m[2]
		argsStr := m[3]
		if tool == "join" {
			continue
		}
		var deps []int
		for _, d := range depRe.FindAllStringSubmatch(argsStr, -1) {
			n, _ := strconv.Atoi(d[1])
			deps = append(deps, n)
		}
		tareas = append(tareas, Tarea{idx: idx, tool: tool, args: argsStr, deps: deps})
	}
	return tareas, nil
}

func validarPlan(tareas []Tarea) error {
	ids := make(map[int]bool, len(tareas))
	for _, t := range tareas {
		ids[t.idx] = true
	}
	for i := range tareas {
		var validas []int
		for _, dep := range tareas[i].deps {
			if dep < tareas[i].idx && ids[dep] {
				validas = append(validas, dep)
			}
		}
		tareas[i].deps = validas
	}
	return nil
}

func sustituirPlaceholders(args string, resultados map[int]string) string {
	return depRe.ReplaceAllStringFunc(args, func(s string) string {
		n, _ := strconv.Atoi(s[1:])
		if r, ok := resultados[n]; ok {
			return r
		}
		return s
	})
}

// taskSlot modela asyncio.Event: canal cerrado para broadcast + resultado almacenado.
type taskSlot struct {
	done   chan struct{} // se cierra cuando la tarea termina (broadcast)
	result string
	mu     sync.Mutex
}

func newSlot() *taskSlot { return &taskSlot{done: make(chan struct{})} }

func (ts *taskSlot) complete(r string) {
	ts.mu.Lock()
	ts.result = r
	ts.mu.Unlock()
	close(ts.done) // despierta a todos los waiters
}

func (ts *taskSlot) wait() string {
	<-ts.done
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return ts.result
}

type ToolFn func(args string) string

func ejecutarDag(tareas []Tarea, tools map[string]ToolFn) (map[int]string, error) {
	slots := make(map[int]*taskSlot, len(tareas))
	for _, t := range tareas {
		slots[t.idx] = newSlot()
	}

	var wg sync.WaitGroup
	errs := make(chan error, len(tareas))

	for _, t := range tareas {
		wg.Add(1)
		go func(tarea Tarea) {
			defer wg.Done()

			// Esperar dependencias (broadcast via canal cerrado)
			for _, dep := range tarea.deps {
				slots[dep].wait()
			}

			// Resolver placeholders con resultados reales
			resultadosSnap := make(map[int]string, len(slots))
			for idx, slot := range slots {
				select {
				case <-slot.done:
					slot.mu.Lock()
					resultadosSnap[idx] = slot.result
					slot.mu.Unlock()
				default:
				}
			}
			args := sustituirPlaceholders(tarea.args, resultadosSnap)

			// Ejecutar herramienta
			fn, ok := tools[tarea.tool]
			var resultado string
			if ok {
				resultado = fn(args)
			} else {
				resultado = fmt.Sprintf("[tool '%s' no registrada]", tarea.tool)
			}

			slots[tarea.idx].complete(resultado)
			preview := resultado
			if len(preview) > 50 {
				preview = preview[:50]
			}
			fmt.Printf("  T%d %s(%s) → %s\n", tarea.idx, tarea.tool, args[:min(40, len(args))], preview)
		}(t)
	}

	wg.Wait()
	close(errs)

	if err := <-errs; err != nil {
		return nil, err
	}

	resultados := make(map[int]string, len(slots))
	for idx, slot := range slots {
		resultados[idx] = slot.result
	}
	return resultados, nil
}

func parsearJoiner(texto string) (string, string) {
	re := regexp.MustCompile(`(?is)(Finish|Replan)\((.+?)\)$`)
	if m := re.FindStringSubmatch(texto); m != nil {
		accion := strings.Title(strings.ToLower(m[1])) //nolint
		return accion, strings.TrimSpace(m[2])
	}
	return "Finish", texto
}

func llmCompiler(tarea, toolDocs string, tools map[string]ToolFn) (string, error) {
	plannerSystem := fmt.Sprintf(
		"Eres un planificador. Descompón el problema en tool calls que maximicen el paralelismo.\n\n"+
			"Formato estricto (una tarea por línea):\n  <idx>. <tool>(<args>)\n\n"+
			"Reglas:\n- Índices desde 1, estrictamente crecientes\n"+
			"- Para usar el output de la tarea N como argumento: $N\n"+
			"- Tareas sin $N en sus args se ejecutan en paralelo de inmediato\n"+
			"- Última línea siempre: join()\n\nHerramientas disponibles:\n%s", toolDocs,
	)

	var context []apiMsg
	var lastContent string

	for ronda := range maxRepl {
		// 1. PLANNER
		msgs := append(context, apiMsg{Role: "user", Content: tarea})
		planTexto, err := llmCall(plannerSystem, msgs, 600)
		if err != nil {
			return "", fmt.Errorf("planner: %w", err)
		}

		fmt.Printf("\n[ronda %d] Plan generado:\n%s\n", ronda+1, planTexto[:min(300, len(planTexto))])

		tareas, _ := parsearPlan(planTexto)
		if err := validarPlan(tareas); err != nil {
			return "", fmt.Errorf("validación: %w", err)
		}

		// 2. TASK FETCHING UNIT
		fmt.Println("\nEjecutando DAG:")
		resultados, err := ejecutarDag(tareas, tools)
		if err != nil {
			return "", fmt.Errorf("executor: %w", err)
		}

		// 3. JOINER
		var idxs []int
		for idx := range resultados {
			idxs = append(idxs, idx)
		}
		sort.Ints(idxs)
		var resLines []string
		for _, idx := range idxs {
			resLines = append(resLines, fmt.Sprintf("T%d: %s", idx, resultados[idx]))
		}
		tao := planTexto + "\n\nResultados:\n" + strings.Join(resLines, "\n")

		joinerPrompt := fmt.Sprintf(
			"Historial de ejecución del plan:\n%s\n\n"+
				"Decide:\n- Si la información es suficiente: Finish(<respuesta completa>)\n"+
				"- Si falta información: Replan(<qué faltó>)\n\n"+
				"Responde SOLO con una de las dos opciones anteriores.", tao,
		)
		joinerTexto, err := llmCall("", []apiMsg{{Role: "user", Content: joinerPrompt}}, 300)
		if err != nil {
			return "", fmt.Errorf("joiner: %w", err)
		}

		accion, contenido := parsearJoiner(joinerTexto)
		lastContent = contenido
		preview := contenido
		if len(preview) > 80 {
			preview = preview[:80]
		}
		fmt.Printf("\nJoiner: %s → %s\n", accion, preview)

		if accion == "Finish" {
			return contenido, nil
		}

		context = append(context,
			apiMsg{Role: "assistant", Content: planTexto},
			apiMsg{Role: "user", Content: tao},
		)
	}

	return lastContent, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func calcular(expresion string) string {
	// Eliminar placeholders residuales y evaluar con go-math básico
	expr := depRe.ReplaceAllString(expresion, "0")
	expr = strings.TrimSpace(expr)

	// Evaluador numérico simple (solo operaciones básicas)
	result, err := evalExpr(expr)
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}
	return strconv.FormatFloat(result, 'f', -1, 64)
}

// evalExpr evalúa expresiones numéricas simples sin dependencias externas.
func evalExpr(expr string) (float64, error) {
	expr = strings.ReplaceAll(expr, " ", "")

	// Manejar multiplicación y división
	if idx := lastIndexOfAny(expr, "*/"); idx > 0 {
		left, err := evalExpr(expr[:idx])
		if err != nil {
			return 0, err
		}
		right, err := evalExpr(expr[idx+1:])
		if err != nil {
			return 0, err
		}
		if expr[idx] == '*' {
			return left * right, nil
		}
		return left / right, nil
	}
	// Suma y resta
	if idx := lastIndexOfAny(expr, "+-"); idx > 0 {
		left, err := evalExpr(expr[:idx])
		if err != nil {
			return 0, err
		}
		right, err := evalExpr(expr[idx+1:])
		if err != nil {
			return 0, err
		}
		if expr[idx] == '+' {
			return left + right, nil
		}
		return left - right, nil
	}
	return strconv.ParseFloat(expr, 64)
}

func lastIndexOfAny(s, chars string) int {
	for i := len(s) - 1; i >= 0; i-- {
		if strings.ContainsRune(chars, rune(s[i])) {
			return i
		}
	}
	return -1
}

func main() {
	tools := map[string]ToolFn{"calcular": calcular}
	toolDocs := "calcular(expresion): evalúa una expresión matemática y devuelve el resultado numérico."

	tarea := "Calcula el área de un rectángulo de 15×8 metros y el área de un " +
		"círculo de radio 5 metros (π≈3.14159). ¿Cuál es mayor y por cuánto?"

	fmt.Printf("Tarea: %s\n", tarea)

	resultado, err := llmCompiler(tarea, toolDocs, tools)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("\n=== Respuesta final ===\n%s\n", resultado)
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
