// Recuperación ante fallos: retry con backoff, circuit breaker, context overflow, JSON inválido

// Cómo ejecutar: make go FILE=go/17-produccion/recuperacion.go

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net/http"
	"os"
	"strings"
	"time"
)

var recuperacionModel = envOr("MODEL", "claude-sonnet-4-6")
var recuperacionModelHaiku = envOr("SMALL_MODEL", "claude-haiku-4-5-20251001")

// ─── Retry con backoff exponencial ───────────────────────────────────────────

func conRetry(fn func() (interface{}, error), maxIntentos int, backoffBase float64) (interface{}, error) {
	var ultimoError error
	for intento := 0; intento < maxIntentos; intento++ {
		result, err := fn()
		if err == nil {
			return result, nil
		}
		ultimoError = err
		if intento == maxIntentos-1 {
			break
		}
		espera := backoffBase*math.Pow(2, float64(intento)) + rand.Float64()*0.5
		fmt.Printf("[retry] Intento %d fallido (%T). Reintentando en %.1fs...\n", intento+1, err, espera)
		time.Sleep(time.Duration(espera * float64(time.Second)))
	}
	return nil, fmt.Errorf("agotados %d intentos: %w", maxIntentos, ultimoError)
}

// ─── Circuit breaker para herramientas externas ───────────────────────────────

type circuitBreaker struct {
	nombre          string
	umbralFallos    int
	ventanaResetMin int
	fallos          []time.Time
	abierto         bool
	abiertoDesde    time.Time
}

func nuevoCircuitBreaker(nombre string) *circuitBreaker {
	return &circuitBreaker{nombre: nombre, umbralFallos: 5, ventanaResetMin: 2}
}

func (cb *circuitBreaker) limpiarFallosAntiguos() {
	corte := time.Now().Add(-time.Duration(cb.ventanaResetMin) * time.Minute)
	var recientes []time.Time
	for _, t := range cb.fallos {
		if t.After(corte) {
			recientes = append(recientes, t)
		}
	}
	cb.fallos = recientes
}

func (cb *circuitBreaker) registrarExito() {
	cb.fallos = nil
	cb.abierto = false
}

func (cb *circuitBreaker) registrarFallo() {
	cb.fallos = append(cb.fallos, time.Now())
	cb.limpiarFallosAntiguos()
	if len(cb.fallos) >= cb.umbralFallos {
		cb.abierto = true
		cb.abiertoDesde = time.Now()
		fmt.Printf("[circuit] %s: circuito ABIERTO tras %d fallos\n", cb.nombre, cb.umbralFallos)
	}
}

func (cb *circuitBreaker) puedeIntentar() bool {
	if !cb.abierto {
		return true
	}
	if time.Since(cb.abiertoDesde) > time.Duration(cb.ventanaResetMin)*time.Minute {
		cb.abierto = false
		cb.fallos = nil
		fmt.Printf("[circuit] %s: circuito CERRADO (reset automático)\n", cb.nombre)
		return true
	}
	return false
}

func (cb *circuitBreaker) ejecutar(fn func() (string, error)) (string, error) {
	if !cb.puedeIntentar() {
		return "", fmt.Errorf("circuito abierto para %s — servicio no disponible", cb.nombre)
	}
	result, err := fn()
	if err != nil {
		cb.registrarFallo()
		return "", err
	}
	cb.registrarExito()
	return result, nil
}

var breakers = map[string]*circuitBreaker{}

func obtenerBreaker(nombre string) *circuitBreaker {
	if _, ok := breakers[nombre]; !ok {
		breakers[nombre] = nuevoCircuitBreaker(nombre)
	}
	return breakers[nombre]
}

func herramientaStub(nombre string, params map[string]interface{}) (string, error) {
	if rand.Float64() < 0.4 {
		return "", fmt.Errorf("servicio %s no disponible", nombre)
	}
	return fmt.Sprintf("Resultado de %s(%v)", nombre, params), nil
}

func ejecutarHerramientaSegura(nombre string, params map[string]interface{}) string {
	breaker := obtenerBreaker(nombre)
	result, err := breaker.ejecutar(func() (string, error) {
		return herramientaStub(nombre, params)
	})
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}
	return result
}

// ─── Compresión de contexto ───────────────────────────────────────────────────

const ventanaTokens = 200_000
const umbralCompresion = 0.75

type recuperMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type recuperResponse struct {
	Content []struct {
		Text string `json:"text"`
	} `json:"content"`
}

func llamarAnthropicRecup(model string, mensajes []recuperMsg, maxTokens int) (recuperResponse, error) {
	payload := map[string]interface{}{
		"model":      model,
		"max_tokens": maxTokens,
		"messages":   mensajes,
	}
	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", envBaseURL(), bytes.NewReader(body))
	req.Header.Set("x-api-key", os.Getenv("ANTHROPIC_API_KEY"))
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("content-type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return recuperResponse{}, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	var ar recuperResponse
	return ar, json.Unmarshal(data, &ar)
}

func comprimirSiNecesario(mensajes []recuperMsg, tokensUsados int) ([]recuperMsg, error) {
	if float64(tokensUsados) < ventanaTokens*umbralCompresion {
		return mensajes, nil
	}
	fmt.Printf("[context] Comprimiendo historial (%d tokens > %.0f umbral)\n",
		tokensUsados, ventanaTokens*umbralCompresion)

	antiguos := mensajes[1 : max1(len(mensajes)-4, 1)]
	antiguosJSON, _ := json.Marshal(antiguos[:min1(len(antiguos), 5)])

	resumenResp, err := llamarAnthropicRecup(recuperacionModelHaiku, []recuperMsg{
		{Role: "user", Content: "Resume este historial en 3-5 bullets:\n" + string(antiguosJSON)},
	}, 512)
	if err != nil {
		return mensajes, err
	}
	resumen := resumenResp.Content[0].Text

	resultado := []recuperMsg{mensajes[0]}
	resultado = append(resultado, recuperMsg{Role: "assistant", Content: "[Resumen de pasos anteriores: " + resumen + "]"})
	if len(mensajes) >= 4 {
		resultado = append(resultado, mensajes[len(mensajes)-4:]...)
	}
	return resultado, nil
}

func max1(a, b int) int {
	if a > b {
		return a
	}
	return b
}
func min1(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ─── Retry para output JSON mal formado ──────────────────────────────────────

func obtenerJsonValido(prompt, schemaDesc string, maxIntentos int) (map[string]interface{}, error) {
	mensajes := []recuperMsg{
		{Role: "user", Content: prompt + "\n\nDevuelve JSON con: " + schemaDesc},
	}

	for intento := 0; intento < maxIntentos; intento++ {
		ar, err := llamarAnthropicRecup(recuperacionModel, mensajes, 512)
		if err != nil {
			return nil, err
		}
		texto := ar.Content[0].Text

		textoLimpio := strings.TrimSpace(texto)
		if strings.Contains(textoLimpio, "```") {
			inicio := strings.Index(textoLimpio, "{")
			fin := strings.LastIndex(textoLimpio, "}") + 1
			if inicio >= 0 && fin > inicio {
				textoLimpio = textoLimpio[inicio:fin]
			}
		}

		var result map[string]interface{}
		if err := json.Unmarshal([]byte(textoLimpio), &result); err == nil {
			return result, nil
		} else {
			if intento == maxIntentos-1 {
				return nil, fmt.Errorf("el modelo no produjo JSON válido en %d intentos", maxIntentos)
			}
			mensajes = append(mensajes,
				recuperMsg{Role: "assistant", Content: texto},
				recuperMsg{Role: "user", Content: fmt.Sprintf(
					"Tu respuesta no es JSON válido. Error: %v. Devuelve exactamente el JSON especificado, sin texto adicional.", err,
				)},
			)
			fmt.Printf("[json_retry] Intento %d fallido — retrying con feedback\n", intento+1)
		}
	}
	return nil, fmt.Errorf("loop sin resultado")
}

func main() {
	fmt.Println("=== Retry con backoff ===")
	fmt.Println("(retry demo comentado para no gastar tokens)")

	fmt.Println("=== Circuit breaker ===")
	for i := 0; i < 8; i++ {
		resultado := ejecutarHerramientaSegura("search_docs", map[string]interface{}{"q": "test"})
		if len(resultado) > 60 {
			resultado = resultado[:60]
		}
		fmt.Printf("  Intento %d: %s\n", i+1, resultado)
	}

	fmt.Println("\n=== JSON con retry ===")
	datos, err := obtenerJsonValido(
		"Describe en JSON un agente simple",
		`{"nombre": string, "herramientas": list, "pasos_max": int}`,
		3,
	)
	if err != nil {
		fmt.Printf("Error tras retries: %v\n", err)
	} else {
		datosJSON, _ := json.Marshal(datos)
		fmt.Printf("JSON válido recibido: %s\n", datosJSON)
	}
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
