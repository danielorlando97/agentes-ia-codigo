// Ventana deslizante por conteo de turns.
// Invariante: mantiene los últimos maxTurns mensajes,
// preservando siempre el primero (ancla de la tarea).

// Cómo ejecutar: make go FILE=go/06-memoria/02-corto-plazo/nivel-1-minimo.go

package main

import "fmt"

type Mensaje struct {
	Role    string
	Content string
}

func buildContext(messages []Mensaje, maxTurns int) []Mensaje {
	if len(messages) <= maxTurns {
		return messages
	}
	result := make([]Mensaje, 0, maxTurns)
	result = append(result, messages[0])
	result = append(result, messages[len(messages)-(maxTurns-1):]...)
	return result
}

func main() {
	msgs := make([]Mensaje, 40)
	for i := range msgs {
		role := "user"
		if i%2 != 0 {
			role = "assistant"
		}
		msgs[i] = Mensaje{Role: role, Content: fmt.Sprintf("mensaje %d", i)}
	}

	result := buildContext(msgs, 10)
	fmt.Printf("Entrada: %d mensajes\n", len(msgs))
	fmt.Printf("Salida:  %d mensajes\n", len(result))
	fmt.Printf("Primero: %s\n", result[0].Content)
	fmt.Printf("Último:  %s\n", result[len(result)-1].Content)
}
