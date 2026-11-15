package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/AndresFZV/httpaas-go/internal/health"
	"github.com/AndresFZV/httpaas-go/internal/metrics"
)

func TestHealthz(t *testing.T) {
	mux := newTestServer(t, health.Snapshot{})

	status, body := get(t, mux, "/healthz")
	if status != http.StatusOK || body != "ok\n" {
		t.Errorf("estado = %d, cuerpo = %q", status, body)
	}
}

func TestReadyz(t *testing.T) {
	stale := healthySnapshot()
	stale.CheckedAt = time.Now().Add(-2 * time.Minute)

	noVBox := healthySnapshot()
	noVBox.Components[2].OK = false

	nsDown := healthySnapshot()
	nsDown.Components[0].OK = false
	nsDown.Components[1].OK = false

	tests := []struct {
		name       string
		snap       health.Snapshot
		wantStatus int
		wantState  string
	}{
		{"infraestructura operativa", healthySnapshot(), http.StatusOK, "listo"},
		{"ns caída no impide atender", nsDown, http.StatusOK, "listo"},
		{"sin verificar todavía", health.Snapshot{}, http.StatusServiceUnavailable, "no_listo"},
		{"verificación antigua", stale, http.StatusServiceUnavailable, "no_listo"},
		{"VirtualBox no responde", noVBox, http.StatusServiceUnavailable, "no_listo"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mux := newTestServer(t, tt.snap)
			status, body := get(t, mux, "/readyz")

			var resp readinessResponse
			if err := json.Unmarshal([]byte(body), &resp); err != nil {
				t.Fatalf("respuesta no es JSON: %v\n%s", err, body)
			}
			if status != tt.wantStatus || resp.Estado != tt.wantState {
				t.Errorf("estado HTTP = %d, estado = %q, motivo = %q", status, resp.Estado, resp.Motivo)
			}
		})
	}
}

func TestMetricsEndpoint(t *testing.T) {
	reg := metrics.New()
	h := newTestHandler(t, healthySnapshot(), &fakePower{}, reg)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	if ct := rec.Header().Get("Content-Type"); ct != metrics.ContentType {
		t.Errorf("Content-Type = %q", ct)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`httpaas_instances{estado="activa"} 1`,
		`httpaas_instances{estado="apagada"} 0`,
		`httpaas_component_up{componente="dns"} 1`,
		`httpaas_component_up{componente="virtualbox"} 1`,
		`httpaas_free_ips 224`,
		"# TYPE httpaas_uptime_seconds gauge",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("falta %q en:\n%s", want, body)
		}
	}
}
