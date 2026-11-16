package auth

import (
	"errors"
	"testing"
	"time"
)

// newTestAuth crea un Authenticator con un reloj que la prueba puede adelantar.
func newTestAuth(t *testing.T) (*Authenticator, *time.Time) {
	t.Helper()
	now := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	a := New("admin", "secreta")
	a.now = func() time.Time { return now }
	return a, &now
}

func TestNewWithoutCredentialsDisablesLogin(t *testing.T) {
	if New("", "secreta") != nil || New("admin", "") != nil {
		t.Error("sin usuario o sin contraseña, New debe devolver nil")
	}
}

func TestCheck(t *testing.T) {
	a, _ := newTestAuth(t)

	if !a.Check("admin", "secreta") {
		t.Error("credenciales correctas rechazadas")
	}
	for _, c := range [][2]string{{"admin", "otra"}, {"root", "secreta"}, {"", ""}} {
		if a.Check(c[0], c[1]) {
			t.Errorf("Check(%q, %q) aceptó credenciales incorrectas", c[0], c[1])
		}
	}
}

func TestLoginCreatesSession(t *testing.T) {
	a, _ := newTestAuth(t)

	s, _, err := a.Login("admin", "secreta", "192.168.10.1")
	if err != nil {
		t.Fatalf("Login() error: %v", err)
	}
	if len(s.ID) < 40 || s.User != "admin" {
		t.Errorf("sesión = %+v", s)
	}
	if got, ok := a.Session(s.ID); !ok || got.User != "admin" {
		t.Errorf("Session() = %+v, %v", got, ok)
	}
}

func TestSessionIDsAreUnique(t *testing.T) {
	a, _ := newTestAuth(t)

	first, _, _ := a.Login("admin", "secreta", "c")
	second, _, _ := a.Login("admin", "secreta", "c")
	if first.ID == second.ID {
		t.Error("dos inicios de sesión generaron el mismo identificador")
	}
}

func TestSessionExpires(t *testing.T) {
	a, now := newTestAuth(t)
	s, _, _ := a.Login("admin", "secreta", "c")

	*now = now.Add(DefaultSessionTTL - time.Second)
	if _, ok := a.Session(s.ID); !ok {
		t.Error("la sesión venció antes de tiempo")
	}
	*now = now.Add(time.Second)
	if _, ok := a.Session(s.ID); ok {
		t.Error("la sesión sigue activa después de vencer")
	}
}

func TestLogout(t *testing.T) {
	a, _ := newTestAuth(t)
	s, _, _ := a.Login("admin", "secreta", "c")

	a.Logout(s.ID)
	if _, ok := a.Session(s.ID); ok {
		t.Error("la sesión sigue activa después de cerrarla")
	}
}

func TestUnknownSession(t *testing.T) {
	a, _ := newTestAuth(t)
	if _, ok := a.Session("inventada"); ok {
		t.Error("se aceptó una sesión que no existe")
	}
	if _, ok := a.Session(""); ok {
		t.Error("se aceptó una sesión vacía")
	}
}

func TestLoginBlocksAfterRepeatedFailures(t *testing.T) {
	a, now := newTestAuth(t)
	client := "192.168.10.1"

	for i := 1; i <= DefaultMaxFailures; i++ {
		if _, _, err := a.Login("admin", "mala", client); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("intento %d: error = %v", i, err)
		}
	}

	// Bloqueado: ni siquiera la contraseña correcta funciona.
	_, retry, err := a.Login("admin", "secreta", client)
	if !errors.Is(err, ErrTooManyAttempts) || retry != DefaultBlockTime {
		t.Fatalf("error = %v, espera = %v", err, retry)
	}

	// Otro cliente no se ve afectado.
	if _, _, err := a.Login("admin", "secreta", "otro"); err != nil {
		t.Errorf("otro cliente: %v", err)
	}

	*now = now.Add(DefaultBlockTime)
	if _, _, err := a.Login("admin", "secreta", client); err != nil {
		t.Errorf("después del bloqueo: %v", err)
	}
}

func TestSuccessfulLoginResetsFailures(t *testing.T) {
	a, _ := newTestAuth(t)
	client := "c"

	for range DefaultMaxFailures - 1 {
		a.Login("admin", "mala", client)
	}
	if _, _, err := a.Login("admin", "secreta", client); err != nil {
		t.Fatalf("Login() error: %v", err)
	}
	// Los intentos fallidos se reinician: uno más no bloquea.
	a.Login("admin", "mala", client)
	if _, _, err := a.Login("admin", "secreta", client); err != nil {
		t.Errorf("se bloqueó sin llegar al límite: %v", err)
	}
}
