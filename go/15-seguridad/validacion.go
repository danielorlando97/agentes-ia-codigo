// Validación de output de agentes — tres capas independientes.
//
// Demuestra las tres capas de validación:
// 1. Esquema: el output tiene el formato correcto (JSON)
// 2. Contenido: el output no contiene datos sensibles (regex)
// 3. Acción: las tool calls son seguras para ejecutar
//
// Sin API key — las llamadas al LLM son simuladas.
//
// Uso:
//
//	go run validacion.go
//	go run validacion.go -capa esquema
//	go run validacion.go -capa contenido
//	go run validacion.go -capa accion

// Cómo ejecutar: make go FILE=go/15-seguridad/validacion.go


package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"regexp"
	"strings"
)

// ─────────────────────────────────────────────
// Tipos
// ─────────────────────────────────────────────

type toolCall struct {
	Nombre string                 `json:"nombre"`
	Params map[string]interface{} `json:"params"`
}

type outputAgente struct {
	Respuesta   string     `json:"respuesta"`
	Acciones    []toolCall `json:"acciones"`
	Confianza   float64    `json:"confianza"`
	Referencias []string   `json:"referencias"`
}

type resultadoValidacion struct {
	valido      bool
	capaFallida string
	motivo      string
	output      *outputAgente
}

// ─────────────────────────────────────────────
// Capa 1: validación de esquema
// ─────────────────────────────────────────────

func validarEsquema(outputRaw string) (*outputAgente, string) {
	var data map[string]interface{}
	if err := json.Unmarshal([]byte(outputRaw), &data); err != nil {
		return nil, fmt.Sprintf("JSON inválido: %s", err.Error())
	}

	if _, ok := data["respuesta"]; !ok {
		return nil, "Campo 'respuesta' ausente"
	}

	confianza := 1.0
	if c, ok := data["confianza"]; ok {
		v, ok := c.(float64)
		if !ok {
			return nil, "Campo 'confianza' debe ser número"
		}
		confianza = v
	}
	if confianza < 0.0 || confianza > 1.0 {
		return nil, "Campo 'confianza' debe estar entre 0.0 y 1.0"
	}

	var acciones []toolCall
	if raw, ok := data["acciones"]; ok {
		rawList, ok := raw.([]interface{})
		if !ok {
			return nil, "Campo 'acciones' debe ser lista"
		}
		for _, a := range rawList {
			m, ok := a.(map[string]interface{})
			if !ok {
				return nil, "Elemento de 'acciones' no es objeto"
			}
			nombre, ok := m["nombre"].(string)
			if !ok {
				return nil, "Tool call sin campo 'nombre'"
			}
			params, _ := m["params"].(map[string]interface{})
			if params == nil {
				params = map[string]interface{}{}
			}
			acciones = append(acciones, toolCall{Nombre: nombre, Params: params})
		}
	}

	refs := []string{}
	if r, ok := data["referencias"].([]interface{}); ok {
		for _, v := range r {
			if s, ok := v.(string); ok {
				refs = append(refs, s)
			}
		}
	}

	return &outputAgente{
		Respuesta:   data["respuesta"].(string),
		Acciones:    acciones,
		Confianza:   confianza,
		Referencias: refs,
	}, ""
}

// ─────────────────────────────────────────────
// Capa 2: validación de contenido
// ─────────────────────────────────────────────

type patronSensibleV struct {
	re   *regexp.Regexp
	tipo string
}

var patronesSensiblesV = []patronSensibleV{
	{regexp.MustCompile(`\b\d{3}-\d{2}-\d{4}\b`), "SSN"},
	{regexp.MustCompile(`\b\d{4}[\s-]\d{4}[\s-]\d{4}[\s-]\d{4}\b`), "tarjeta de crédito"},
	{regexp.MustCompile(`(?i)password:\s*\S+`), "contraseña"},
	{regexp.MustCompile(`(?i)api[_-]?key:\s*\S+`), "API key"},
	{regexp.MustCompile(`(?i)token:\s*[A-Za-z0-9._-]{20,}`), "token de sesión"},
}

func validarContenido(output *outputAgente) (bool, string) {
	for _, p := range patronesSensiblesV {
		if p.re.MatchString(output.Respuesta) {
			return false, fmt.Sprintf("Dato sensible en respuesta: %s", p.tipo)
		}
	}
	return true, ""
}

// ─────────────────────────────────────────────
// Capa 3: validación de acción
// ─────────────────────────────────────────────

var accionesProhibidas = map[string]bool{
	"delete_database": true,
	"drop_table":      true,
	"rm_rf":           true,
	"send_bulk_email": true,
}

const directorioTrabajo = "/workspace"

func validarAccion(tc toolCall) (bool, string) {
	if accionesProhibidas[tc.Nombre] {
		return false, fmt.Sprintf("Acción prohibida: '%s'", tc.Nombre)
	}

	if tc.Nombre == "write_file" {
		ruta, _ := tc.Params["path"].(string)
		if strings.Contains(ruta, "..") {
			return false, fmt.Sprintf("Path traversal detectado: '%s'", ruta)
		}
		if !strings.HasPrefix(ruta, directorioTrabajo) {
			return false, fmt.Sprintf("Escritura fuera del directorio de trabajo: '%s'", ruta)
		}
	}

	if tc.Nombre == "send_email" {
		var externos []string
		if to, ok := tc.Params["to"].([]interface{}); ok {
			for _, d := range to {
				if s, ok := d.(string); ok && !strings.HasSuffix(s, "@empresa.com") {
					externos = append(externos, s)
				}
			}
		}
		if len(externos) > 0 {
			return false, fmt.Sprintf("Email a destino no autorizado: %s", strings.Join(externos, ", "))
		}
	}

	return true, ""
}

// ─────────────────────────────────────────────
// Pipeline completo
// ─────────────────────────────────────────────

func validarPipeline(outputRaw string) resultadoValidacion {
	output, errorEsquema := validarEsquema(outputRaw)
	if output == nil {
		return resultadoValidacion{valido: false, capaFallida: "esquema", motivo: errorEsquema}
	}

	ok, motivoContenido := validarContenido(output)
	if !ok {
		return resultadoValidacion{valido: false, capaFallida: "contenido", motivo: motivoContenido}
	}

	for _, accion := range output.Acciones {
		ok, motivoAccion := validarAccion(accion)
		if !ok {
			return resultadoValidacion{
				valido:      false,
				capaFallida: "accion",
				motivo:      fmt.Sprintf("[%s] %s", accion.Nombre, motivoAccion),
			}
		}
	}

	return resultadoValidacion{valido: true, output: output}
}

// ─────────────────────────────────────────────
// Casos de prueba
// ─────────────────────────────────────────────

func mustJSON(v interface{}) string {
	b, _ := json.Marshal(v)
	return string(b)
}

var casos = map[string]string{
	"valido_sin_acciones": mustJSON(map[string]interface{}{
		"respuesta": "Tu pedido llega el jueves.", "confianza": 0.95, "referencias": []string{"pedido_12345"},
	}),
	"valido_con_accion_segura": mustJSON(map[string]interface{}{
		"respuesta": "He creado el ticket de soporte.",
		"acciones":  []map[string]interface{}{{"nombre": "write_file", "params": map[string]interface{}{"path": "/workspace/tickets/t001.txt", "content": "..."}}},
		"confianza": 0.88,
	}),
	"falla_esquema_json":          `{"respuesta": "incompleto"`,
	"falla_esquema_campo":         mustJSON(map[string]interface{}{"texto": "sin campo respuesta"}),
	"falla_contenido_ssn":         mustJSON(map[string]interface{}{"respuesta": "El SSN del usuario es 123-45-6789.", "confianza": 0.5}),
	"falla_contenido_apikey":      mustJSON(map[string]interface{}{"respuesta": "La API key es: api_key: sk-abcdef123456", "confianza": 0.7}),
	"falla_accion_prohibida":      mustJSON(map[string]interface{}{"respuesta": "Limpiando base de datos.", "acciones": []map[string]interface{}{{"nombre": "delete_database", "params": map[string]interface{}{"confirm": true}}}, "confianza": 0.9}),
	"falla_accion_path_traversal": mustJSON(map[string]interface{}{"respuesta": "Archivo escrito.", "acciones": []map[string]interface{}{{"nombre": "write_file", "params": map[string]interface{}{"path": "../../etc/passwd", "content": "..."}}}, "confianza": 0.85}),
	"falla_email_externo":         mustJSON(map[string]interface{}{"respuesta": "Email enviado.", "acciones": []map[string]interface{}{{"nombre": "send_email", "params": map[string]interface{}{"to": []string{"atacante@external.com"}, "body": "datos..."}}}, "confianza": 0.7}),
}

var casosFiltrados = map[string][]string{
	"esquema":   {"falla_esquema_json", "falla_esquema_campo", "valido_sin_acciones"},
	"contenido": {"falla_contenido_ssn", "falla_contenido_apikey", "valido_sin_acciones"},
	"accion":    {"falla_accion_prohibida", "falla_accion_path_traversal", "falla_email_externo", "valido_con_accion_segura"},
}

var ordenCasos = []string{
	"valido_sin_acciones", "valido_con_accion_segura",
	"falla_esquema_json", "falla_esquema_campo",
	"falla_contenido_ssn", "falla_contenido_apikey",
	"falla_accion_prohibida", "falla_accion_path_traversal", "falla_email_externo",
}

func truncarV(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}

func demoCapa(capa string) {
	nombres := ordenCasos
	if capa != "" {
		if filtered, ok := casosFiltrados[capa]; ok {
			nombres = filtered
		}
	}

	titulo := "pipeline completo"
	if capa != "" {
		titulo = "capa " + strings.ToUpper(capa)
	}

	sep := strings.Repeat("=", 64)
	fmt.Printf("\n%s\n", sep)
	fmt.Printf("  VALIDACIÓN DE OUTPUT — %s\n", titulo)
	fmt.Printf("%s\n", sep)
	fmt.Printf("  %-38s %-10s %s\n", "Caso", "Válido", "Detalle")
	fmt.Printf("  %-38s %-10s %s\n", strings.Repeat("-", 38), strings.Repeat("-", 10), strings.Repeat("-", 28))

	for _, nombre := range nombres {
		raw, ok := casos[nombre]
		if !ok {
			continue
		}
		r := validarPipeline(raw)
		validoStr := "✓"
		if !r.valido {
			validoStr = fmt.Sprintf("✗ [%s]", r.capaFallida)
		}
		detalle := "OK"
		if r.motivo != "" {
			detalle = truncarV(r.motivo, 35)
		} else if !r.valido {
			detalle = ""
		}
		fmt.Printf("  %-38s %-10s %s\n", nombre, validoStr, detalle)
	}
	fmt.Printf("%s\n\n", sep)
}

func main() {
	capa := flag.String("capa", "", "mostrar solo casos de una capa: esquema | contenido | accion")
	flag.Parse()
	demoCapa(*capa)
}
