package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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

func TestSecureBasicAuth(t *testing.T) {
	h := Secure(okHandler, SecurityConfig{AuthUser: "admin", AuthPassword: "secreta"})

	tests := []struct {
		name     string
		user     string
		password string
		send     bool
		want     int
	}{
		{"sin credenciales", "", "", false, http.StatusUnauthorized},
		{"contraseña incorrecta", "admin", "otra", true, http.StatusUnauthorized},
		{"usuario incorrecto", "root", "secreta", true, http.StatusUnauthorized},
		{"credenciales correctas", "admin", "secreta", true, http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.send {
				req.SetBasicAuth(tt.user, tt.password)
			}
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

func TestAuthEnabledRequiresUserAndPassword(t *testing.T) {
	tests := []struct {
		cfg  SecurityConfig
		want bool
	}{
		{SecurityConfig{}, false},
		{SecurityConfig{AuthUser: "admin"}, false},
		{SecurityConfig{AuthPassword: "secreta"}, false},
		{SecurityConfig{AuthUser: "admin", AuthPassword: "secreta"}, true},
	}
	for _, tt := range tests {
		if got := tt.cfg.AuthEnabled(); got != tt.want {
			t.Errorf("AuthEnabled(%+v) = %v, se esperaba %v", tt.cfg, got, tt.want)
		}
	}
}

func TestSecurePublicPathsSkipAuth(t *testing.T) {
	h := Secure(okHandler, SecurityConfig{AuthUser: "admin", AuthPassword: "secreta", PublicPaths: []string{"/healthz"}})

	if rec := serve(h, httptest.NewRequest(http.MethodGet, "/healthz", nil)); rec.Code != http.StatusOK {
		t.Errorf("/healthz: estado = %d, se esperaba 200 sin credenciales", rec.Code)
	}
	if rec := serve(h, httptest.NewRequest(http.MethodGet, "/metrics", nil)); rec.Code != http.StatusUnauthorized {
		t.Errorf("/metrics: estado = %d, se esperaba 401", rec.Code)
	}
}

func TestSecureCountsRejections(t *testing.T) {
	reg := metrics.New()
	h := Secure(okHandler, SecurityConfig{AuthUser: "admin", AuthPassword: "secreta", Metrics: reg})

	serve(h, httptest.NewRequest(http.MethodGet, "/", nil))

	bad := httptest.NewRequest(http.MethodGet, "/", nil)
	bad.SetBasicAuth("admin", "otra")
	serve(h, bad)

	cross := httptest.NewRequest(http.MethodPost, "/deploy", nil)
	cross.SetBasicAuth("admin", "secreta")
	cross.Header.Set("Sec-Fetch-Site", "cross-site")
	serve(h, cross)

	text := metricsText(t, reg)
	for _, reason := range []string{reasonNoCredentials, reasonBadCredentials, reasonCrossOrigin} {
		want := `httpaas_security_rejections_total{motivo="` + reason + `"} 1`
		if !strings.Contains(text, want) {
			t.Errorf("falta %q en:\n%s", want, text)
		}
	}
}
