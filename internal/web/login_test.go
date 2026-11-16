package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/AndresFZV/httpaas-go/internal/auth"
)

// newLoginServer crea las rutas de la interfaz con inicio de sesión activo (admin/secreta).
func newLoginServer(t *testing.T) (*http.ServeMux, *auth.Authenticator) {
	t.Helper()
	h := newTestHandler(t, healthySnapshot(), &fakePower{}, nil)
	h.auth = auth.New("admin", "secreta")

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	return mux, h.auth
}

func postLogin(t *testing.T, mux http.Handler, user, password, next string) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{"usuario": {user}, "contrasena": {password}, "next": {next}}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = "192.168.10.1:50000"
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func sessionCookieFrom(rec *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookie {
			return c
		}
	}
	return nil
}

func TestLoginPageRenders(t *testing.T) {
	mux, _ := newLoginServer(t)

	status, body := get(t, mux, "/login?next=/instances")
	if status != http.StatusOK {
		t.Fatalf("estado = %d", status)
	}
	for _, want := range []string{"Inicia sesión", `name="usuario"`, `name="contrasena"`, `value="/instances"`, "data-password-toggle"} {
		if !strings.Contains(body, want) {
			t.Errorf("falta %q en la página", want)
		}
	}
}

func TestLoginSuccess(t *testing.T) {
	mux, a := newLoginServer(t)

	rec := postLogin(t, mux, "admin", "secreta", "/instances")

	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/instances" {
		t.Fatalf("estado = %d, Location = %q", rec.Code, rec.Header().Get("Location"))
	}
	cookie := sessionCookieFrom(rec)
	if cookie == nil {
		t.Fatal("no se envió la cookie de sesión")
	}
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode || cookie.Path != "/" {
		t.Errorf("cookie = %+v", cookie)
	}
	if _, ok := a.Session(cookie.Value); !ok {
		t.Error("la cookie no corresponde a una sesión activa")
	}
}

func TestLoginWrongPassword(t *testing.T) {
	mux, _ := newLoginServer(t)

	rec := postLogin(t, mux, "admin", "otra", "/instances")

	if rec.Code != http.StatusUnauthorized || sessionCookieFrom(rec) != nil {
		t.Fatalf("estado = %d, cookie = %v", rec.Code, sessionCookieFrom(rec))
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Usuario o contraseña incorrectos.") || !strings.Contains(body, `value="admin"`) {
		t.Error("la página no muestra el error o no conserva el usuario escrito")
	}
}

func TestLoginTooManyAttempts(t *testing.T) {
	mux, _ := newLoginServer(t)

	for range auth.DefaultMaxFailures {
		postLogin(t, mux, "admin", "otra", "/")
	}
	rec := postLogin(t, mux, "admin", "secreta", "/")

	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Errorf("estado = %d, Retry-After = %q", rec.Code, rec.Header().Get("Retry-After"))
	}
	if !strings.Contains(rec.Body.String(), "Demasiados intentos fallidos") {
		t.Error("la página no explica el bloqueo")
	}
}

func TestLoginRejectsExternalNext(t *testing.T) {
	for _, next := range []string{"https://otro.example", "//otro.example", `/\otro.example`, "/login", ""} {
		if got := safeNext(next); got != "/" {
			t.Errorf("safeNext(%q) = %q, se esperaba /", next, got)
		}
	}
	for _, next := range []string{"/instances", "/deployments/abc?x=1", "/"} {
		if got := safeNext(next); got != next {
			t.Errorf("safeNext(%q) = %q", next, got)
		}
	}
}

func TestLoginPageWithSessionRedirects(t *testing.T) {
	mux, a := newLoginServer(t)
	s, _, _ := a.Login("admin", "secreta", "c")

	req := httptest.NewRequest(http.MethodGet, "/login?next=/instances", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: s.ID})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/instances" {
		t.Errorf("estado = %d, Location = %q", rec.Code, rec.Header().Get("Location"))
	}
}

func TestLogout(t *testing.T) {
	mux, a := newLoginServer(t)
	s, _, _ := a.Login("admin", "secreta", "c")

	req := httptest.NewRequest(http.MethodPost, "/logout", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: s.ID})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login?salida=1" {
		t.Errorf("estado = %d, Location = %q", rec.Code, rec.Header().Get("Location"))
	}
	if c := sessionCookieFrom(rec); c == nil || c.MaxAge >= 0 {
		t.Errorf("la cookie no se borró: %+v", c)
	}
	if _, ok := a.Session(s.ID); ok {
		t.Error("la sesión sigue activa")
	}

	_, body := get(t, mux, "/login?salida=1")
	if !strings.Contains(body, "Cerraste sesión correctamente.") {
		t.Error("el login no confirma el cierre de sesión")
	}
}

func TestLayoutShowsUserAndLogout(t *testing.T) {
	mux, a := newLoginServer(t)
	s, _, _ := a.Login("admin", "secreta", "c")

	req := httptest.NewRequest(http.MethodGet, "/instances", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: s.ID})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, `class="user-name">admin<`) || !strings.Contains(body, `action="/logout"`) {
		t.Error("el layout no muestra el usuario ni el botón para cerrar sesión")
	}
}

func TestLoginDisabledWithoutCredentials(t *testing.T) {
	mux := newTestServer(t, healthySnapshot())

	status, _ := get(t, mux, "/login")
	if status != http.StatusSeeOther {
		t.Errorf("sin inicio de sesión configurado, /login debe redirigir; estado = %d", status)
	}
	_, body := get(t, mux, "/instances")
	if strings.Contains(body, `action="/logout"`) {
		t.Error("sin inicio de sesión no debe aparecer el botón para cerrar sesión")
	}
}
