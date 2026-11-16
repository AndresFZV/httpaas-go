package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AndresFZV/httpaas-go/internal/auth"
	"github.com/AndresFZV/httpaas-go/internal/metrics"
)

// okHandler responde 200 y permite comprobar si la petición llegó hasta la aplicación.
var okHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
})

func serve(h http.Handler, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestSecureRejectsCrossOriginPost(t *testing.T) {
	h := Secure(okHandler, SecurityConfig{})

	req := httptest.NewRequest(http.MethodPost, "/instances/paisajes/delete", nil)
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	req.Header.Set("Origin", "http://sitio-malicioso.example")

	if rec := serve(h, req); rec.Code != http.StatusForbidden {
		t.Errorf("estado = %d, se esperaba 403", rec.Code)
	}
}

func TestSecureRejectsCrossOriginPostWithoutFetchMetadata(t *testing.T) {
	h := Secure(okHandler, SecurityConfig{})

	// Navegadores antiguos no envían Sec-Fetch-Site; se compara Origin con Host.
	req := httptest.NewRequest(http.MethodPost, "http://localhost:8080/deploy", nil)
	req.Header.Set("Origin", "http://sitio-malicioso.example")

	if rec := serve(h, req); rec.Code != http.StatusForbidden {
		t.Errorf("estado = %d, se esperaba 403", rec.Code)
	}
}

func TestSecureAllowsSameOriginPost(t *testing.T) {
	h := Secure(okHandler, SecurityConfig{})

	req := httptest.NewRequest(http.MethodPost, "http://localhost:8080/deploy", nil)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Origin", "http://localhost:8080")

	if rec := serve(h, req); rec.Code != http.StatusOK {
		t.Errorf("estado = %d, se esperaba 200", rec.Code)
	}
}

func TestSecureAllowsNonBrowserPost(t *testing.T) {
	h := Secure(okHandler, SecurityConfig{})

	// curl y otros clientes no envían Origin ni Sec-Fetch-Site.
	req := httptest.NewRequest(http.MethodPost, "/deploy", nil)

	if rec := serve(h, req); rec.Code != http.StatusOK {
		t.Errorf("estado = %d, se esperaba 200", rec.Code)
	}
}

func TestSecureAllowsCrossOriginGet(t *testing.T) {
	h := Secure(okHandler, SecurityConfig{})

	// Los GET no cambian nada; un enlace desde otro sitio debe seguir funcionando.
	req := httptest.NewRequest(http.MethodGet, "/instances", nil)
	req.Header.Set("Sec-Fetch-Site", "cross-site")

	if rec := serve(h, req); rec.Code != http.StatusOK {
		t.Errorf("estado = %d, se esperaba 200", rec.Code)
	}
}

func TestSecureSetsSecurityHeaders(t *testing.T) {
	h := Secure(okHandler, SecurityConfig{})
	rec := serve(h, httptest.NewRequest(http.MethodGet, "/", nil))

	want := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "same-origin",
	}
	for name, value := range want {
		if got := rec.Header().Get(name); got != value {
			t.Errorf("%s = %q, se esperaba %q", name, got, value)
		}
	}
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'self'") {
		t.Errorf("Content-Security-Policy = %q", csp)
	}
}

func TestSecureSetsHeadersOnRejectedRequests(t *testing.T) {
	h := Secure(okHandler, SecurityConfig{})

	req := httptest.NewRequest(http.MethodPost, "/deploy", nil)
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	rec := serve(h, req)

	if rec.Header().Get("X-Frame-Options") != "DENY" {
		t.Error("la respuesta 403 no trae las cabeceras de seguridad")
	}
}

func TestSecureWithoutAuthDoesNotAskForCredentials(t *testing.T) {
	h := Secure(okHandler, SecurityConfig{})

	if rec := serve(h, httptest.NewRequest(http.MethodGet, "/", nil)); rec.Code != http.StatusOK {
		t.Errorf("estado = %d, se esperaba 200", rec.Code)
	}
}

// newSession inicia sesión con admin/secreta y devuelve el autenticador y la cookie de la sesión.
func newSession(t *testing.T) (*auth.Authenticator, *http.Cookie) {
	t.Helper()
	a := auth.New("admin", "secreta")
	s, _, err := a.Login("admin", "secreta", "192.0.2.1")
	if err != nil {
		t.Fatalf("Login() error: %v", err)
	}
	return a, &http.Cookie{Name: sessionCookie, Value: s.ID}
}

func TestSecureSessionRequired(t *testing.T) {
	a, cookie := newSession(t)
	h := Secure(okHandler, SecurityConfig{Auth: a, PublicPaths: []string{"/healthz"}})

	tests := []struct {
		name         string
		method, path string
		withCookie   bool
		wantStatus   int
		wantLocation string
	}{
		{"página sin sesión va al login", http.MethodGet, "/instances?q=1", false, http.StatusSeeOther, "/login?next=%2Finstances%3Fq%3D1"},
		{"dashboard sin sesión va al login sin next", http.MethodGet, "/", false, http.StatusSeeOther, "/login"},
		{"POST sin sesión va al login", http.MethodPost, "/deploy", false, http.StatusSeeOther, "/login"},
		{"consulta interna sin sesión responde 401", http.MethodGet, "/api/status", false, http.StatusUnauthorized, ""},
		{"página con sesión", http.MethodGet, "/instances", true, http.StatusOK, ""},
		{"consulta interna con sesión", http.MethodGet, "/api/status", true, http.StatusOK, ""},
		{"formulario de login sin sesión", http.MethodGet, "/login", false, http.StatusOK, ""},
		{"archivos estáticos sin sesión", http.MethodGet, "/static/style.css", false, http.StatusOK, ""},
		{"ruta pública sin sesión", http.MethodGet, "/healthz", false, http.StatusOK, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			if tt.withCookie {
				req.AddCookie(cookie)
			}
			rec := serve(h, req)
			if rec.Code != tt.wantStatus || rec.Header().Get("Location") != tt.wantLocation {
				t.Errorf("estado = %d, Location = %q; se esperaba %d %q",
					rec.Code, rec.Header().Get("Location"), tt.wantStatus, tt.wantLocation)
			}
		})
	}
}

func TestSecureRejectsUnknownOrClosedSession(t *testing.T) {
	a, cookie := newSession(t)
	h := Secure(okHandler, SecurityConfig{Auth: a})

	invented := httptest.NewRequest(http.MethodGet, "/instances", nil)
	invented.AddCookie(&http.Cookie{Name: sessionCookie, Value: "inventada"})
	if rec := serve(h, invented); rec.Code != http.StatusSeeOther {
		t.Errorf("sesión inventada: estado = %d", rec.Code)
	}

	a.Logout(cookie.Value)
	closed := httptest.NewRequest(http.MethodGet, "/instances", nil)
	closed.AddCookie(cookie)
	if rec := serve(h, closed); rec.Code != http.StatusSeeOther {
		t.Errorf("sesión cerrada: estado = %d", rec.Code)
	}
}

func TestSecureMetricsAcceptsBasicAuth(t *testing.T) {
	a, cookie := newSession(t)
	h := Secure(okHandler, SecurityConfig{Auth: a})

	tests := []struct {
		name  string
		setup func(*http.Request)
		want  int
	}{
		{"sin credenciales", func(*http.Request) {}, http.StatusUnauthorized},
		{"contraseña incorrecta", func(r *http.Request) { r.SetBasicAuth("admin", "otra") }, http.StatusUnauthorized},
		{"usuario y contraseña (Prometheus)", func(r *http.Request) { r.SetBasicAuth("admin", "secreta") }, http.StatusOK},
		{"sesión del navegador", func(r *http.Request) { r.AddCookie(cookie) }, http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
			tt.setup(req)
			rec := serve(h, req)
			if rec.Code != tt.want {
				t.Errorf("estado = %d, se esperaba %d", rec.Code, tt.want)
			}
			if tt.want == http.StatusUnauthorized && rec.Header().Get("WWW-Authenticate") == "" {
				t.Error("falta la cabecera WWW-Authenticate")
			}
		})
	}
}

func TestSecureUIDoesNotAcceptBasicAuth(t *testing.T) {
	a, _ := newSession(t)
	h := Secure(okHandler, SecurityConfig{Auth: a})

	req := httptest.NewRequest(http.MethodGet, "/instances", nil)
	req.SetBasicAuth("admin", "secreta")
	if rec := serve(h, req); rec.Code != http.StatusSeeOther {
		t.Errorf("estado = %d; la interfaz solo debe aceptar la sesión", rec.Code)
	}
}

func TestSecureCountsRejections(t *testing.T) {
	reg := metrics.New()
	a, cookie := newSession(t)
	h := Secure(okHandler, SecurityConfig{Auth: a, Metrics: reg})

	serve(h, httptest.NewRequest(http.MethodGet, "/instances", nil))

	bad := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	bad.SetBasicAuth("admin", "otra")
	serve(h, bad)

	cross := httptest.NewRequest(http.MethodPost, "/deploy", nil)
	cross.AddCookie(cookie)
	cross.Header.Set("Sec-Fetch-Site", "cross-site")
	serve(h, cross)

	text := metricsText(t, reg)
	for _, reason := range []string{reasonNoSession, reasonBadCredentials, reasonCrossOrigin} {
		want := `httpaas_security_rejections_total{motivo="` + reason + `"} 1`
		if !strings.Contains(text, want) {
			t.Errorf("falta %q en:\n%s", want, text)
		}
	}
}
