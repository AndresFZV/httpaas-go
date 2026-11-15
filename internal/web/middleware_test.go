package web

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AndresFZV/httpaas-go/internal/metrics"
)

func metricsText(t *testing.T, reg *metrics.Registry) string {
	t.Helper()
	var b strings.Builder
	if err := reg.WriteText(&b); err != nil {
		t.Fatalf("WriteText() error: %v", err)
	}
	return b.String()
}

func TestInstrumentCountsRequestsByRoute(t *testing.T) {
	reg := metrics.New()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /instances/{hostname}/delete", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusSeeOther)
	})
	h := Instrument(mux, reg)

	for _, host := range []string{"paisajes", "negocios"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/instances/"+host+"/delete", nil))
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/no-existe", nil))

	text := metricsText(t, reg)
	// Las dos eliminaciones comparten la serie de la ruta, no una por hostname.
	for _, want := range []string{
		`httpaas_http_requests_total{metodo="POST",ruta="/instances/{hostname}/delete",codigo="303"} 2`,
		`httpaas_http_requests_total{metodo="GET",ruta="sin_ruta",codigo="404"} 1`,
		`httpaas_http_request_duration_seconds_count{metodo="POST",ruta="/instances/{hostname}/delete"} 2`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("falta %q en:\n%s", want, text)
		}
	}
}

func TestInstrumentSetsRequestID(t *testing.T) {
	h := Instrument(okHandler, nil)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if id := rec.Header().Get("X-Request-ID"); len(id) != 16 {
		t.Errorf("X-Request-ID = %q, se esperaba un identificador de 16 caracteres", id)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-ID", "prueba-123")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if id := rec.Header().Get("X-Request-ID"); id != "prueba-123" {
		t.Errorf("X-Request-ID = %q, se esperaba el del cliente", id)
	}

	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-ID", "texto con espacios\ny saltos")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if id := rec.Header().Get("X-Request-ID"); strings.ContainsAny(id, " \n") {
		t.Errorf("se aceptó un identificador inválido: %q", id)
	}
}

func TestRequestLevel(t *testing.T) {
	tests := []struct {
		path   string
		status int
		want   slog.Level
	}{
		{"/instances", http.StatusOK, slog.LevelInfo},
		{"/api/status", http.StatusOK, slog.LevelDebug},
		{"/static/app.js", http.StatusOK, slog.LevelDebug},
		{"/metrics", http.StatusOK, slog.LevelDebug},
		{"/deploy", http.StatusForbidden, slog.LevelWarn},
		{"/api/status", http.StatusInternalServerError, slog.LevelError},
		{"/readyz", http.StatusServiceUnavailable, slog.LevelDebug},
		{"/deploy", http.StatusServiceUnavailable, slog.LevelError},
		{"/metrics", http.StatusUnauthorized, slog.LevelDebug},
	}
	for _, tt := range tests {
		if got := requestLevel(tt.path, tt.status); got != tt.want {
			t.Errorf("requestLevel(%s, %d) = %v, se esperaba %v", tt.path, tt.status, got, tt.want)
		}
	}
}
