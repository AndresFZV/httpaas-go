package instance

import (
	"errors"
	"regexp"
	"strings"
)

// hostnamePattern valida una etiqueta DNS según RFC 1123, restringida a minúsculas.
var hostnamePattern = regexp.MustCompile(`^[a-z]([a-z0-9-]{0,61}[a-z0-9])?$`)

// reservedHostnames son nombres que ya usa la infraestructura del dominio.
var reservedHostnames = map[string]bool{
	"ns":        true,
	"localhost": true,
}

var (
	ErrHostnameEmpty    = errors.New("el nombre del host es obligatorio")
	ErrHostnameInvalid  = errors.New("el nombre del host solo admite letras minúsculas, números y guiones; debe empezar por una letra, no terminar en guion y tener máximo 63 caracteres")
	ErrHostnameReserved = errors.New("ese nombre del host está reservado por la infraestructura")
)

// NormalizeHostname quita los espacios de los extremos y pasa el nombre a minúsculas.
func NormalizeHostname(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

// ValidateHostname verifica que name pueda usarse como nombre de host dentro del dominio.
// Espera un nombre ya normalizado con NormalizeHostname.
func ValidateHostname(name string) error {
	if name == "" {
		return ErrHostnameEmpty
	}
	if !hostnamePattern.MatchString(name) {
		return ErrHostnameInvalid
	}
	if reservedHostnames[name] {
		return ErrHostnameReserved
	}
	return nil
}
