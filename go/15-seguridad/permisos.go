// Permisos y capabilities: ToolRegistry con allow/deny lists, scope validation, RBAC

// Cómo ejecutar: make go FILE=go/15-seguridad/permisos.go

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

var modelPermisos = envOr("MODEL", "claude-sonnet-4-6")

// ─── Modelo de herramienta con scope ─────────────────────────────────────────

type Herramienta struct {
	Nombre             string
	Descripcion        string
	Schema             map[string]interface{}
	Funcion            func(params map[string]interface{}) interface{}
	RequiereAprobacion bool
	Scope              map[string]interface{}
}

type ContextoAgente struct {
	UsuarioID               string
	Rol                     string
	HerramientasAutorizadas map[string]bool
	DirectoriosPermitidos   []string
	MaxDescuento            float64
}

// ─── Tool Registry ────────────────────────────────────────────────────────────

type ToolRegistry struct {
	herramientas map[string]*Herramienta
	denyAlways   map[string]bool
}

func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{
		herramientas: make(map[string]*Herramienta),
		denyAlways:   make(map[string]bool),
	}
}

func (r *ToolRegistry) Registrar(h *Herramienta) {
	r.herramientas[h.Nombre] = h
}

func (r *ToolRegistry) DenegarSiempre(nombres ...string) {
	for _, n := range nombres {
		r.denyAlways[n] = true
	}
}

func (r *ToolRegistry) HerramientasParaContexto(ctx *ContextoAgente) []map[string]interface{} {
	var visibles []map[string]interface{}
	for nombre, h := range r.herramientas {
		if r.denyAlways[nombre] {
			continue
		}
		if !ctx.HerramientasAutorizadas[nombre] {
			continue
		}
		visibles = append(visibles, map[string]interface{}{
			"name":         h.Nombre,
			"description":  h.Descripcion,
			"input_schema": h.Schema,
		})
	}
	return visibles
}

func (r *ToolRegistry) Ejecutar(nombre string, params map[string]interface{}, ctx *ContextoAgente) (interface{}, error) {
	if r.denyAlways[nombre] {
		return nil, fmt.Errorf("PermissionError: '%s' bloqueado permanentemente (deny list)", nombre)
	}
	if !ctx.HerramientasAutorizadas[nombre] {
		return nil, fmt.Errorf("PermissionError: '%s' no autorizado para rol '%s'", nombre, ctx.Rol)
	}
	h, ok := r.herramientas[nombre]
	if !ok {
		return nil, fmt.Errorf("ValueError: Herramienta '%s' no registrada", nombre)
	}
	if err := r.validarScope(h, params, ctx); err != nil {
		return nil, err
	}
	if h.Funcion == nil {
		return fmt.Sprintf("[SIMULADO] %s(%v)", nombre, params), nil
	}
	return h.Funcion(params), nil
}

func (r *ToolRegistry) validarScope(h *Herramienta, params map[string]interface{}, ctx *ContextoAgente) error {
	if h.Nombre == "leer_archivo" || h.Nombre == "escribir_archivo" {
		ruta, _ := params["path"].(string)
		if strings.Contains(ruta, "..") {
			return fmt.Errorf("PermissionError: Path traversal detectado: '%s'", ruta)
		}
		if len(ctx.DirectoriosPermitidos) > 0 {
			normalizado := filepath.Clean(ruta)
			permitido := false
			for _, d := range ctx.DirectoriosPermitidos {
				if strings.HasPrefix(normalizado, filepath.Clean(d)) {
					permitido = true
					break
				}
			}
			if !permitido {
				return fmt.Errorf("PermissionError: Ruta '%s' fuera del scope autorizado", ruta)
			}
		}
	}

	if h.Nombre == "aplicar_descuento" {
		porcentaje, _ := params["porcentaje"].(float64)
		if porcentaje > ctx.MaxDescuento {
			return fmt.Errorf("PermissionError: Descuento %.0f%% supera el límite para rol '%s' (%.0f%%)",
				porcentaje, ctx.Rol, ctx.MaxDescuento)
		}
	}

	if h.Scope != nil {
		if solo, ok := h.Scope["solo_usuario_actual"].(bool); ok && solo {
			usuarioParam, _ := params["usuario_id"].(string)
			if usuarioParam == "" {
				usuarioParam = ctx.UsuarioID
			}
			if usuarioParam != ctx.UsuarioID {
				return fmt.Errorf("PermissionError: '%s' solo puede operar sobre el usuario de la sesión", h.Nombre)
			}
		}
	}
	return nil
}

// ─── RBAC: permisos por rol ───────────────────────────────────────────────────

type RolConfig struct {
	Allow        map[string]bool
	MaxDescuento float64
}

var permisosPorRol = map[string]RolConfig{
	"soporte_basico": {
		Allow:        map[string]bool{"obtener_info_usuario": true, "estado_pedido": true, "crear_ticket": true},
		MaxDescuento: 0.0,
	},
	"soporte_premium": {
		Allow:        map[string]bool{"obtener_info_usuario": true, "estado_pedido": true, "crear_ticket": true, "aplicar_descuento": true},
		MaxDescuento: 20.0,
	},
	"soporte_manager": {
		Allow:        map[string]bool{"obtener_info_usuario": true, "estado_pedido": true, "crear_ticket": true, "aplicar_descuento": true, "modificar_usuario": true},
		MaxDescuento: 50.0,
	},
}

var denyAlwaysGlobal = []string{"borrar_usuario", "acceso_admin", "exportar_todos_usuarios"}

func construirContexto(usuarioID, rol string) *ContextoAgente {
	config, ok := permisosPorRol[rol]
	if !ok {
		config = RolConfig{Allow: map[string]bool{}, MaxDescuento: 0.0}
	}
	return &ContextoAgente{
		UsuarioID:               usuarioID,
		Rol:                     rol,
		HerramientasAutorizadas: config.Allow,
		DirectoriosPermitidos:   []string{"/data/" + usuarioID + "/"},
		MaxDescuento:            config.MaxDescuento,
	}
}

// ─── Agente con ToolRegistry ──────────────────────────────────────────────────

func construirRegistry() *ToolRegistry {
	registry := NewToolRegistry()
	registry.DenegarSiempre(denyAlwaysGlobal...)

	herramientas := []*Herramienta{
		{
			Nombre:      "obtener_info_usuario",
			Descripcion: "Obtiene información del usuario de la sesión.",
			Schema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{"usuario_id": map[string]interface{}{"type": "string"}},
				"required":   []string{"usuario_id"},
			},
			Scope: map[string]interface{}{"solo_usuario_actual": true},
		},
		{
			Nombre:      "estado_pedido",
			Descripcion: "Obtiene el estado de un pedido.",
			Schema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{"pedido_id": map[string]interface{}{"type": "string"}},
				"required":   []string{"pedido_id"},
			},
		},
		{
			Nombre:      "crear_ticket",
			Descripcion: "Crea un ticket de soporte.",
			Schema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{"descripcion": map[string]interface{}{"type": "string"}},
				"required":   []string{"descripcion"},
			},
		},
		{
			Nombre:      "aplicar_descuento",
			Descripcion: "Aplica un descuento a un pedido (requiere rol premium o superior).",
			Schema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"pedido_id":  map[string]interface{}{"type": "string"},
					"porcentaje": map[string]interface{}{"type": "number"},
				},
				"required": []string{"pedido_id", "porcentaje"},
			},
		},
		{
			Nombre:      "modificar_usuario",
			Descripcion: "Modifica datos del usuario (solo managers).",
			Schema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"usuario_id": map[string]interface{}{"type": "string"},
					"campo":      map[string]interface{}{"type": "string"},
					"valor":      map[string]interface{}{"type": "string"},
				},
				"required": []string{"usuario_id", "campo", "valor"},
			},
		},
	}

	for _, h := range herramientas {
		registry.Registrar(h)
	}
	return registry
}

// ─── HTTP client para Anthropic ───────────────────────────────────────────────

type MensajePermisos struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"`
}

type AnthropicRequestPermisos struct {
	Model     string            `json:"model"`
	MaxTokens int               `json:"max_tokens"`
	Tools     []interface{}     `json:"tools,omitempty"`
	Messages  []MensajePermisos `json:"messages"`
}

type ContentBlockPermisos struct {
	Type  string                 `json:"type"`
	Text  string                 `json:"text,omitempty"`
	ID    string                 `json:"id,omitempty"`
	Name  string                 `json:"name,omitempty"`
	Input map[string]interface{} `json:"input,omitempty"`
}

type AnthropicResponsePermisos struct {
	Content    []ContentBlockPermisos `json:"content"`
	StopReason string                 `json:"stop_reason"`
}

func llamarAPIPermisos(mensajes []MensajePermisos, tools []interface{}) (*AnthropicResponsePermisos, error) {
	payload := AnthropicRequestPermisos{
		Model:     modelPermisos,
		MaxTokens: 512,
		Tools:     tools,
		Messages:  mensajes,
	}
	body, _ := json.Marshal(payload)

	req, _ := http.NewRequest("POST", envBaseURL(), bytes.NewReader(body))
	req.Header.Set("x-api-key", os.Getenv("ANTHROPIC_API_KEY"))
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("content-type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)
	var ar AnthropicResponsePermisos
	if err := json.Unmarshal(data, &ar); err != nil {
		return nil, fmt.Errorf("respuesta inesperada: %s", data)
	}
	return &ar, nil
}

func agenteConPermisos(tarea, usuarioID, rol string) (string, error) {
	registry := construirRegistry()
	ctx := construirContexto(usuarioID, rol)
	herramientasVisibles := registry.HerramientasParaContexto(ctx)

	tools := make([]interface{}, len(herramientasVisibles))
	for i, h := range herramientasVisibles {
		tools[i] = h
	}

	mensajes := []MensajePermisos{{Role: "user", Content: tarea}}

	for i := 0; i < 10; i++ {
		respuesta, err := llamarAPIPermisos(mensajes, tools)
		if err != nil {
			return "", err
		}

		mensajes = append(mensajes, MensajePermisos{Role: "assistant", Content: respuesta.Content})

		if respuesta.StopReason == "end_turn" {
			for _, b := range respuesta.Content {
				if b.Type == "text" {
					return b.Text, nil
				}
			}
			return "", nil
		}

		if respuesta.StopReason == "tool_use" {
			var toolResults []map[string]interface{}
			for _, bloque := range respuesta.Content {
				if bloque.Type != "tool_use" {
					continue
				}
				resultado, err := registry.Ejecutar(bloque.Name, bloque.Input, ctx)
				var contenido string
				if err != nil {
					contenido = "Error de permisos: " + err.Error()
					fmt.Printf("[PERM DENIED] %s: %v\n", bloque.Name, err)
				} else {
					contenido = fmt.Sprintf("%v", resultado)
				}
				toolResults = append(toolResults, map[string]interface{}{
					"type":        "tool_result",
					"tool_use_id": bloque.ID,
					"content":     contenido,
				})
			}
			mensajes = append(mensajes, MensajePermisos{Role: "user", Content: toolResults})
		}
	}

	return "[max iteraciones]", nil
}

func main() {
	fmt.Println("=== Allow/Deny list ===")
	registry := construirRegistry()
	ctxBasico := construirContexto("user_123", "soporte_basico")

	herramientas := registry.HerramientasParaContexto(ctxBasico)
	nombres := make([]string, len(herramientas))
	for i, h := range herramientas {
		nombres[i] = h["name"].(string)
	}
	fmt.Printf("Herramientas visibles para soporte_basico: %v\n", nombres)

	fmt.Println("\n=== Validación de scope — intento de exceder descuento ===")
	ctxPremium := construirContexto("user_123", "soporte_premium")
	_, err := registry.Ejecutar("aplicar_descuento", map[string]interface{}{"pedido_id": "P001", "porcentaje": 80.0}, ctxPremium)
	if err != nil {
		fmt.Printf("Bloqueado: %v\n", err)
	}

	fmt.Println("\n=== Agente soporte_basico (no puede aplicar descuento) ===")
	resultado, err := agenteConPermisos(
		"Obtén mi información y aplica un descuento del 15% al pedido P001.",
		"user_123",
		"soporte_basico",
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if len(resultado) > 300 {
		resultado = resultado[:300]
	}
	fmt.Printf("Respuesta: %s\n", resultado)
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
