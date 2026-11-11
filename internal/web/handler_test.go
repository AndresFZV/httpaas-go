package web

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AndresFZV/httpaas-go/internal/deployment"
	"github.com/AndresFZV/httpaas-go/internal/instance"
)

type fakeDeployments struct {
	snapshots map[string]deployment.Snapshot
}

func (f fakeDeployments) Start(string, io.Reader, string) (string, error) {
	return "nuevo", nil
}

func (f fakeDeployments) Retry(string) (string, error) {
	return "", deployment.ErrNotRetryable
}

func (f fakeDeployments) Snapshot(id string) (deployment.Snapshot, error) {
	snap, ok := f.snapshots[id]
	if !ok {
		return deployment.Snapshot{}, deployment.ErrNotFound
	}
	return snap, nil
}

func newFakeDeployments() fakeDeployments {
	base := deployment.Snapshot{
		Hostname:   "negocios",
		FQDN:       "negocios.cloud.local",
		URL:        "http://negocios.cloud.local",
		IP:         "192.168.10.31",
		HasContent: true,
	}

	running, succeeded, failed := base, base, base
	running.ID, running.Status, running.Step, running.Percent = "run", deployment.StatusRunning, deployment.StepWaitBoot, 25
	succeeded.ID, succeeded.Status, succeeded.Percent = "ok", deployment.StatusSucceeded, 100
	failed.ID, failed.Status, failed.FailedStep = "fail", deployment.StatusFailed, deployment.StepRegisterDNS
	failed.Error = deployment.Steps[deployment.StepRegisterDNS].FailMessage
	failed.Component = deployment.Steps[deployment.StepRegisterDNS].Component

	return fakeDeployments{snapshots: map[string]deployment.Snapshot{
		"run": running, "ok": succeeded, "fail": failed,
	}}
}

func newTestServer(t *testing.T) *http.ServeMux {
	t.Helper()

	first := netip.MustParseAddr("192.168.10.30")
	last := netip.MustParseAddr("192.168.10.254")
	store, err := instance.NewStore(filepath.Join(t.TempDir(), "instances.json"), first, last)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	if _, err := store.Reserve("paisajes", time.Now()); err != nil {
		t.Fatalf("Reserve() error: %v", err)
	}

	h, err := NewHandler(Config{
		Domain:       "cloud.local",
		DNSServer:    "192.168.10.10",
		FirstIP:      first,
		LastIP:       last,
		TemplatesDir: filepath.Join("..", "..", "templates"),
		StaticDir:    filepath.Join("..", "..", "static"),
	}, store, nil, newFakeDeployments())
	if err != nil {
		t.Fatalf("NewHandler() error: %v", err)
	}

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	return mux
}

func get(t *testing.T, mux *http.ServeMux, path string) (int, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec.Code, rec.Body.String()
}

func assertPage(t *testing.T, mux *http.ServeMux, path string, wantStatus int, want ...string) {
	t.Helper()
	status, body := get(t, mux, path)
	if status != wantStatus {
		t.Fatalf("%s: estado = %d, se esperaba %d", path, status, wantStatus)
	}
	for _, text := range want {
		if !strings.Contains(body, text) {
			t.Errorf("%s: la página no contiene %q", path, text)
		}
	}
}

func TestPagesRender(t *testing.T) {
	mux := newTestServer(t)

	assertPage(t, mux, "/", http.StatusOK, "Dashboard", "paisajes.cloud.local", "224")
	assertPage(t, mux, "/instances", http.StatusOK, "Mis instancias", "paisajes.cloud.local", "192.168.10.30")
	assertPage(t, mux, "/deploy", http.StatusOK, "Desplegar nueva aplicación", ".cloud.local")
}

func TestDeploymentPagesRender(t *testing.T) {
	mux := newTestServer(t)

	assertPage(t, mux, "/deployments/run", http.StatusOK, "Estamos preparando tu aplicación", "/api/deployments/run")
	assertPage(t, mux, "/deployments/ok", http.StatusOK, "¡Tu aplicación está publicada!", "http://negocios.cloud.local")
	assertPage(t, mux, "/deployments/fail", http.StatusOK, "No pudimos completar el despliegue",
		"ERROR EN LA ETAPA 6", "Servidor DNS · Bind9", "/deployments/fail/retry")
	assertPage(t, mux, "/deployments/no-existe", http.StatusNotFound, "No encontramos este despliegue")
}

func TestDeploymentStatusAPI(t *testing.T) {
	mux := newTestServer(t)

	status, body := get(t, mux, "/api/deployments/run")
	if status != http.StatusOK {
		t.Fatalf("estado = %d, se esperaba 200", status)
	}
	var snap deployment.Snapshot
	if err := json.Unmarshal([]byte(body), &snap); err != nil {
		t.Fatalf("respuesta no es JSON válido: %v", err)
	}
	if snap.Status != deployment.StatusRunning || snap.Step != deployment.StepWaitBoot {
		t.Errorf("estado = %s, etapa = %d", snap.Status, snap.Step)
	}

	if status, _ := get(t, mux, "/api/deployments/no-existe"); status != http.StatusNotFound {
		t.Errorf("ID desconocido: estado = %d, se esperaba 404", status)
	}
}

func TestUnknownPathReturnsNotFound(t *testing.T) {
	status, _ := get(t, newTestServer(t), "/no-existe")
	if status != http.StatusNotFound {
		t.Errorf("estado = %d, se esperaba 404", status)
	}
}

func TestFlashMessages(t *testing.T) {
	mux := newTestServer(t)

	tests := []struct {
		path string
		want string
	}{
		{"/instances?created=paisajes", "Instancia paisajes creada"},
		{"/instances?created=paisajes&published=1", "Instancia paisajes creada y contenido publicado"},
		{"/instances?deleted=negocios", "Instancia negocios eliminada"},
		{"/instances?publish_failed=paisajes", "no se pudo publicar el contenido"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			assertPage(t, mux, tt.path, http.StatusOK, tt.want)
		})
	}
}

func TestFlashMessagesIgnoreInvalidHostname(t *testing.T) {
	_, body := get(t, newTestServer(t), "/instances?created=%3Cscript%3E")
	if strings.Contains(body, "creada") {
		t.Error("se mostró un aviso con un hostname inválido")
	}
}

func TestTemplateHelpers(t *testing.T) {
	if got := displayName("paisajes"); got != "Paisajes" {
		t.Errorf("displayName() = %q", got)
	}
	date := time.Date(2026, time.November, 11, 0, 12, 0, 0, time.Local)
	if got := formatDate(date); got != "11 nov 2026, 00:12" {
		t.Errorf("formatDate() = %q", got)
	}
	if got := icon("server\"><script>", 18); got != "" {
		t.Errorf("icon() con nombre inválido = %q, se esperaba vacío", got)
	}
	if got := ipCount(netip.MustParseAddr("192.168.10.30"), netip.MustParseAddr("192.168.10.254")); got != 225 {
		t.Errorf("ipCount() = %d, se esperaba 225", got)
	}
}
