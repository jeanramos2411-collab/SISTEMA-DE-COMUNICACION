package websocket

import (
	"github.com/google/uuid"
)

// newUUID genera un UUID y devuelve los primeros 8 caracteres como string
// Esto es equivalente a Python: str(uuid.uuid4())[:8]
func newUUID() string {
	return uuid.New().String()
}
