package web

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AndresFZV/httpaas-go/internal/instance"
)

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
	}, store, nil, nil)
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

func TestPagesRender(t *testing.T) {
	mux := newTestServer(t)

	tests := []struct {
		path string
		want []string
	}{
		{"/", []string{"Dashboard", "paisajes.cloud.local", "224"}},
		{"/instances", []string{"Mis instancias", "paisajes.cloud.local", "192.168.10.30"}},
		{"/deploy", []string{"Desplegar nueva aplicación", ".cloud.local"}},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			status, body := get(t, mux, tt.path)
			if status != http.StatusOK {
				t.Fatalf("estado = %d, se esperaba 200", status)
			}
			for _, want := range tt.want {
				if !strings.Contains(body, want) {
					t.Errorf("la página no contiene %q", want)
				}
			}
		})
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
			_, body := get(t, mux, tt.path)
			if !strings.Contains(body, tt.want) {
				t.Errorf("la página no contiene %q", tt.want)
			}
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
