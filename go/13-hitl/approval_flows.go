// Cómo ejecutar: make go FILE=go/13-hitl/approval_flows.go
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
)

var modelApproval = envOr("MODEL", "claude-sonnet-4-6")

var accionesAltoRiesgo = map[string]bool{
	"borrar_datos": true, "enviar_email_masivo": true, "transferencia_dinero": true,
	"modificar_cuenta_usuario": true, "desplegar_produccion": true, "revocar_accesos": true,
}

var accionesMedioRiesgo = map[string]bool{
	"escribir_produccion": true, "operacion_bulk": true, "cambiar_configuracion": true,
}

const umbralRegistros = 100

func clasificarAccion(nombre string, params map[string]interface{}) string {
	if accionesAltoRiesgo[nombre] {
		return "alto"
	}
	if accionesMedioRiesgo[nombre] {
		return "medio"
	}
	if registros, ok := params["registros_afectados"].(float64); ok && registros > umbralRegistros {
		return "alto"
	}
	if reversible, ok := params["reversible"].(bool); ok && !reversible {
		return "alto"
	}
	return "bajo"
}

func describirImpacto(nombre string, params map[string]interface{}) string {
	registros, _ := params["registros_afectados"].(float64)
	tabla, _ := params["tabla"].(string)
	if tabla == "" {
		tabla = "desconocida"
	}
	switch nombre {
	case "borrar_datos":
		return fmt.Sprintf("Se borrarán %.0f registros de la tabla '%s' en producción. Esta operación es irreversible.", registros, tabla)
	case "enviar_email_masivo":
		dest, _ := params["destinatarios"].(float64)
		return fmt.Sprintf("Se enviará un email a %.0f usuarios. No puede deshacerse una vez enviado.", dest)
	}
	paramsJSON, _ := json.Marshal(params)
	return fmt.Sprintf("Acción '%s' con parámetros: %s", nombre, paramsJSON)
}

type SolicitudAprobacion struct {
	ID                string                 `json:"id"`
	NombreAccion      string                 `json:"nombre_accion"`
	Params            map[string]interface{} `json:"params"`
	Impacto           string                 `json:"impacto"`
	Timestamp         float64                `json:"timestamp"`
	ExpiraEn          float64                `json:"expira_en"`
	Decision          string                 `json:"decision"`
	ParamsModificados map[string]interface{} `json:"params_modificados"`
}

var cola = map[string]*SolicitudAprobacion{}

func solicitarAprobacionSincrona(nombre string, params map[string]interface{}) map[string]interface{} {
	impacto := describirImpacto(nombre, params)
	fmt.Printf("\n[APROBACIÓN REQUERIDA]\n")
	fmt.Printf("Acción: %s\n", nombre)
	fmt.Printf("Impacto: %s\n", impacto)
	fmt.Println("Opciones: [a]probar / [r]echazar / [m]odificar")

	reader := bufio.NewReader(os.Stdin)
	fmt.Print("Tu decisión: ")
	decision, _ := reader.ReadString('\n')
	decision = strings.TrimSpace(strings.ToLower(decision))

	if strings.HasPrefix(decision, "a") {
		return map[string]interface{}{"tipo": "aprobar", "params": params}
	}

	if strings.HasPrefix(decision, "m") {
		fmt.Print("Nuevos parámetros (JSON): ")
		nuevos, _ := reader.ReadString('\n')
		nuevos = strings.TrimSpace(nuevos)
		var paramsModificados map[string]interface{}
		if err := json.Unmarshal([]byte(nuevos), &paramsModificados); err != nil {
			return map[string]interface{}{"tipo": "rechazar", "motivo": "parámetros modificados inválidos"}
		}
		return map[string]interface{}{"tipo": "modificar", "params_modificados": paramsModificados}
	}

	return map[string]interface{}{"tipo": "rechazar", "motivo": "rechazado por el usuario"}
}

func encolarAprobacion(nombre string, params map[string]interface{}, ttlHoras int) string {
	if ttlHoras == 0 {
		ttlHoras = 4
	}
	solID := uuid.New().String()[:8]
	ahora := float64(time.Now().UnixNano()) / 1e9
	cola[solID] = &SolicitudAprobacion{
		ID:           solID,
		NombreAccion: nombre,
		Params:       params,
		Impacto:      describirImpacto(nombre, params),
		Timestamp:    ahora,
		ExpiraEn:     ahora + float64(ttlHoras)*3600,
	}
	fmt.Printf("[COLA] Acción '%s' encolada (id=%s, expira en %dh)\n", nombre, solID, ttlHoras)
	return solID
}

type HerramientaFn func(nombre string, params map[string]interface{}) string

func ejecutarHerramientaConApproval(
	nombre string,
	params map[string]interface{},
	fnHerramienta HerramientaFn,
	modo string,
) map[string]interface{} {
	nivel := clasificarAccion(nombre, params)

	if nivel == "bajo" || modo == "auto" {
		resultado := fnHerramienta(nombre, params)
		fmt.Printf("[AUTO] %s: %s\n", nombre, resultado)
		return map[string]interface{}{"estado": "ejecutado", "resultado": resultado}
	}

	if nivel == "medio" || modo == "cola" {
		solID := encolarAprobacion(nombre, params, 4)
		return map[string]interface{}{"estado": "pendiente_revision", "id": solID}
	}

	respuesta := solicitarAprobacionSincrona(nombre, params)
	tipo, _ := respuesta["tipo"].(string)

	if tipo == "aprobar" {
		p, _ := respuesta["params"].(map[string]interface{})
		resultado := fnHerramienta(nombre, p)
		return map[string]interface{}{"estado": "ejecutado", "resultado": resultado}
	}
	if tipo == "modificar" {
		p, _ := respuesta["params_modificados"].(map[string]interface{})
		resultado := fnHerramienta(nombre, p)
		return map[string]interface{}{"estado": "ejecutado_modificado", "resultado": resultado}
	}

	motivo, _ := respuesta["motivo"].(string)
	return map[string]interface{}{"estado": "rechazado", "motivo": motivo}
}

var herramientas = []map[string]interface{}{
	{
		"name":        "buscar_info",
		"description": "Busca información. Acción reversible y segura.",
		"input_schema": map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{"query": map[string]interface{}{"type": "string"}},
			"required":   []string{"query"},
		},
	},
	{
		"name":        "borrar_datos",
		"description": "Borra registros de la base de datos. IRREVERSIBLE.",
		"input_schema": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"tabla":               map[string]interface{}{"type": "string"},
				"registros_afectados": map[string]interface{}{"type": "number"},
			},
			"required": []string{"tabla", "registros_afectados"},
		},
	},
}

func ejecutarToolReal(nombre string, params map[string]interface{}) string {
	switch nombre {
	case "buscar_info":
		return fmt.Sprintf("Información encontrada para '%v': resultado simulado.", params["query"])
	case "borrar_datos":
		return fmt.Sprintf("[SIMULADO] Se habrían borrado %v registros de '%v'.", params["registros_afectados"], params["tabla"])
	}
	return fmt.Sprintf("Herramienta '%s' no reconocida.", nombre)
}

func llamarAPIApproval(mensajes []interface{}) (map[string]interface{}, error) {
	payload := map[string]interface{}{
		"model":      modelApproval,
		"max_tokens": 1024,
		"tools":      herramientas,
		"messages":   mensajes,
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
	var result map[string]interface{}
	return result, json.Unmarshal(data, &result)
}

func agenteConApproval(tarea, modoAprobacion string) (string, error) {
	mensajes := []interface{}{map[string]interface{}{"role": "user", "content": tarea}}

	for i := 0; i < 10; i++ {
		respuesta, err := llamarAPIApproval(mensajes)
		if err != nil {
			return "", err
		}

		mensajes = append(mensajes, map[string]interface{}{
			"role":    "assistant",
			"content": respuesta["content"],
		})

		stopReason, _ := respuesta["stop_reason"].(string)

		if stopReason == "end_turn" {
			content, _ := respuesta["content"].([]interface{})
			for _, block := range content {
				b, ok := block.(map[string]interface{})
				if ok && b["type"] == "text" {
					return b["text"].(string), nil
				}
			}
			return "", nil
		}

		if stopReason == "tool_use" {
			content, _ := respuesta["content"].([]interface{})
			var toolResults []interface{}

			for _, block := range content {
				b, ok := block.(map[string]interface{})
				if !ok || b["type"] != "tool_use" {
					continue
				}
				nombre, _ := b["name"].(string)
				toolUseID, _ := b["id"].(string)
				params, _ := b["input"].(map[string]interface{})

				resultado := ejecutarHerramientaConApproval(nombre, params, ejecutarToolReal, modoAprobacion)
				contenidoJSON, _ := json.Marshal(resultado)
				toolResults = append(toolResults, map[string]interface{}{
					"type":        "tool_result",
					"tool_use_id": toolUseID,
					"content":     string(contenidoJSON),
				})
			}
			mensajes = append(mensajes, map[string]interface{}{
				"role":    "user",
				"content": toolResults,
			})
		}
	}

	return "[max iteraciones]", nil
}

func main() {
	fmt.Println("=== Clasificación de riesgo ===")
	tests := []struct {
		nombre string
		params map[string]interface{}
	}{
		{"buscar_info",         map[string]interface{}{"query": "usuarios activos"}},
		{"escribir_produccion", map[string]interface{}{"tabla": "users", "registros_afectados": float64(50)}},
		{"borrar_datos",        map[string]interface{}{"tabla": "users", "registros_afectados": float64(847)}},
	}
	for _, t := range tests {
		nivel := clasificarAccion(t.nombre, t.params)
		fmt.Printf("  %s: %s\n", t.nombre, nivel)
	}

	fmt.Println("\n=== Agente con approval (modo auto — sin interacción) ===")
	resultado, err := agenteConApproval(
		"Busca información sobre usuarios activos en el último mes.",
		"auto",
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if len(resultado) > 200 {
		resultado = resultado[:200]
	}
	fmt.Printf("Resultado: %s\n", resultado)
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
