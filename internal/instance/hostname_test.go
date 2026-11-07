package instance

import (
	"errors"
	"strings"
	"testing"
)

func TestNormalizeHostname(t *testing.T) {
	got := NormalizeHostname("  Paisajes ")
	if got != "paisajes" {
		t.Errorf("NormalizeHostname() = %q, se esperaba %q", got, "paisajes")
	}
}

func TestValidateHostname(t *testing.T) {
	tests := []struct {
		name     string
		hostname string
		wantErr  error
	}{
		{"nombre simple", "paisajes", nil},
		{"con números y guion", "mi-sitio2", nil},
		{"una sola letra", "a", nil},
		{"63 caracteres", "a" + strings.Repeat("b", 62), nil},
		{"vacío", "", ErrHostnameEmpty},
		{"con espacio", "mi sitio", ErrHostnameInvalid},
		{"con mayúscula", "Paisajes", ErrHostnameInvalid},
		{"empieza por número", "1sitio", ErrHostnameInvalid},
		{"termina en guion", "sitio-", ErrHostnameInvalid},
		{"con punto", "sitio.web", ErrHostnameInvalid},
		{"con guion bajo", "mi_sitio", ErrHostnameInvalid},
		{"64 caracteres", "a" + strings.Repeat("b", 63), ErrHostnameInvalid},
		{"reservado ns", "ns", ErrHostnameReserved},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateHostname(tt.hostname)
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("ValidateHostname(%q) = %v, se esperaba %v", tt.hostname, err, tt.wantErr)
			}
		})
	}
}
