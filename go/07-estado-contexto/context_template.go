// Cómo ejecutar: make go FILE=go/07-estado-contexto/context_template.go
package main

import (
	"encoding/json"
	"fmt"
)

type TrustLevel string

const (
	TrustHigh   TrustLevel = "high"
	TrustMedium TrustLevel = "medium"
	TrustLow    TrustLevel = "low"
)

type SlotBudgetError struct {
	Slot   string
	Actual int
	Budget int
}

func (e *SlotBudgetError) Error() string {
	return fmt.Sprintf("Slot '%s': %dt > budget %dt", e.Slot, e.Actual, e.Budget)
}

type SlotDef struct {
	Name     string
	Budget   int
	Trust    TrustLevel
	Required bool
}

func tokensText(text string) int {
	return len([]byte(text)) / 4
}

func tokensList(obj []interface{}) int {
	b, _ := json.Marshal(obj)
	return len(b) / 4
}

type ContextTemplate struct {
	Total int
	Slots []SlotDef
}

func NewContextTemplate(total int) *ContextTemplate {
	if total == 0 {
		total = 128_000
	}
	return &ContextTemplate{
		Total: total,
		Slots: []SlotDef{
			{Name: "system",      Budget: 4_000, Trust: TrustHigh,   Required: true},
			{Name: "constraints", Budget: 2_000, Trust: TrustHigh,   Required: false},
			{Name: "retrieved",   Budget: 3_000, Trust: TrustMedium, Required: false},
			{Name: "tools",       Budget: 2_000, Trust: TrustHigh,   Required: false},
			{Name: "response",    Budget: 8_000, Trust: TrustHigh,   Required: false},
		},
	}
}

func (ct *ContextTemplate) GetSlot(name string) *SlotDef {
	for i := range ct.Slots {
		if ct.Slots[i].Name == name {
			return &ct.Slots[i]
		}
	}
	return nil
}

func (ct *ContextTemplate) HistoryBudget() int {
	sum := 0
	for _, s := range ct.Slots {
		sum += s.Budget
	}
	return ct.Total - sum
}

type ContextValidator struct {
	template *ContextTemplate
}

func (cv *ContextValidator) Validate(assembled map[string]interface{}) []string {
	var errors []string
	for _, slot := range cv.template.Slots {
		content, exists := assembled[slot.Name]
		if !exists {
			if slot.Required {
				errors = append(errors, fmt.Sprintf("Slot requerido '%s' está vacío", slot.Name))
			}
			continue
		}
		if str, ok := content.(string); ok && str != "" {
			actual := tokensText(str)
			if actual > slot.Budget {
				errors = append(errors, fmt.Sprintf("Slot '%s': %dt > budget %dt", slot.Name, actual, slot.Budget))
			}
		}
	}
	return errors
}

type ContextAssembler struct {
	template  *ContextTemplate
	validator *ContextValidator
}

func NewContextAssembler(template *ContextTemplate) *ContextAssembler {
	if template == nil {
		template = NewContextTemplate(0)
	}
	return &ContextAssembler{
		template:  template,
		validator: &ContextValidator{template: template},
	}
}

func (ca *ContextAssembler) clip(text string, budget int) string {
	maxChars := budget * 4
	if len(text) > maxChars {
		return text[:maxChars]
	}
	return text
}

func (ca *ContextAssembler) wrapUntrusted(content, label string) string {
	upper := ""
	for _, c := range label {
		if c >= 'a' && c <= 'z' {
			upper += string(rune(c - 32))
		} else {
			upper += string(c)
		}
	}
	return fmt.Sprintf("[%s]\n%s\n[/%s]", upper, content, upper)
}

type AssembleOpts struct {
	System      string
	History     []interface{}
	Retrieved   string
	Constraints string
	Tools       []interface{}
	Strict      bool
}

func (ca *ContextAssembler) Assemble(opts AssembleOpts) (map[string]interface{}, error) {
	result := map[string]interface{}{}

	sysSlot := ca.template.GetSlot("system")
	sysTokens := tokensText(opts.System)
	if opts.Strict && sysTokens > sysSlot.Budget {
		return nil, &SlotBudgetError{Slot: "system", Actual: sysTokens, Budget: sysSlot.Budget}
	}
	result["system"] = ca.clip(opts.System, sysSlot.Budget)

	if opts.Constraints != "" {
		cSlot := ca.template.GetSlot("constraints")
		result["constraints"] = ca.clip(opts.Constraints, cSlot.Budget)
	}

	if opts.Retrieved != "" {
		rSlot := ca.template.GetSlot("retrieved")
		wrapped := ca.wrapUntrusted(opts.Retrieved, "retrieved")
		result["retrieved"] = ca.clip(wrapped, rSlot.Budget)
	}

	if opts.Tools == nil {
		opts.Tools = []interface{}{}
	}
	result["tools"] = opts.Tools
	result["messages"] = opts.History
	return result, nil
}

func (ca *ContextAssembler) Validate(assembled map[string]interface{}) []string {
	return ca.validator.Validate(assembled)
}

func progressiveToolLoading(allTools []map[string]interface{}, budget int, priorityField string) []map[string]interface{} {
	if priorityField == "" {
		priorityField = "priority"
	}
	sorted := make([]map[string]interface{}, len(allTools))
	copy(sorted, allTools)
	for i := 0; i < len(sorted)-1; i++ {
		for j := i + 1; j < len(sorted); j++ {
			pi, _ := sorted[i][priorityField].(float64)
			pj, _ := sorted[j][priorityField].(float64)
			if pi == 0 {
				pi = 99
			}
			if pj == 0 {
				pj = 99
			}
			if pi > pj {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}

	selected := []map[string]interface{}{}
	used := 0
	for _, tool := range sorted {
		cost := tokensList([]interface{}{tool})
		if used+cost > budget {
			break
		}
		selected = append(selected, tool)
		used += cost
	}
	return selected
}

func main() {
	template := NewContextTemplate(32_000)
	assembler := NewContextAssembler(template)

	tools := []map[string]interface{}{
		{"name": "read_file",  "description": "Lee un archivo",        "priority": float64(1)},
		{"name": "write_file", "description": "Escribe un archivo",    "priority": float64(2)},
		{"name": "run_tests",  "description": "Ejecuta los tests",     "priority": float64(3)},
		{"name": "deploy",     "description": "Despliega el servicio", "priority": float64(4)},
	}

	selectedTools := progressiveToolLoading(tools, 500, "priority")
	names := []string{}
	for _, t := range selectedTools {
		names = append(names, t["name"].(string))
	}
	fmt.Printf("Tools seleccionadas con budget=500t: %v\n", names)

	toolsIface := make([]interface{}, len(selectedTools))
	for i, t := range selectedTools {
		toolsIface[i] = t
	}

	ctx, err := assembler.Assemble(AssembleOpts{
		System:      "Eres un asistente de código experto en Python.",
		History:     []interface{}{map[string]string{"role": "user", "content": "Analiza este repositorio."}},
		Retrieved:   "Sesión anterior: el usuario trabaja en un proyecto de facturación.",
		Constraints: "No modificar archivos de test. Penalización máxima 15%.",
		Tools:       toolsIface,
		Strict:      false,
	})
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	errors := assembler.Validate(ctx)
	if len(errors) == 0 {
		fmt.Println("Errores de validación: ninguno")
	} else {
		fmt.Printf("Errores de validación: %v\n", errors)
	}

	keys := []string{}
	for k := range ctx {
		keys = append(keys, k)
	}
	fmt.Printf("Slots ensamblados: %v\n", keys)
	fmt.Printf("Budget de historial disponible: %dt\n", template.HistoryBudget())

	_, err = assembler.Assemble(AssembleOpts{
		System:  fmt.Sprintf("%020000d", 0),
		History: []interface{}{},
		Strict:  true,
	})
	if err != nil {
		fmt.Printf("\nSlotBudgetError capturado: %v\n", err)
	}
}
