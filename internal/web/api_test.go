package web

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/AndresFZV/httpaas-go/internal/health"
)

// fakeAPIProvisioner registra las eliminaciones.
type fakeAPIProvisioner struct {
	deleted []string
	err     error
}

func (f *fakeAPIProvisioner) Delete(_ context.Context, hostname string) error {
	if f.err != nil {
		return f.err
	}
	f.deleted = append(f.deleted, hostname)
	return nil
}

// newAPIServer crea las rutas con una instancia (paisajes), los despliegues falsos y la especificación.
func newAPIServer(t *testing.T) (*http.ServeMux, *Handler, *fakeAPIProvisioner) {
	t.Helper()
	h := newTestHandler(t, healthySnapshot(), &fakePower{}, nil)
	h.cfg.OpenAPIFile = filepath.Join("..", "..", "api", "openapi.yaml")
	prov := &fakeAPIProvisioner{}
	h.provisioner = prov

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	return mux, h, prov
}

func doAPI(t *testing.T, mux http.Handler, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

// postForm envía un formulario multipart con los campos dados y, si zipData no es nil, el archivo content.
func postForm(t *testing.T, mux http.Handler, path string, fields map[string]string, zipName string, zipData []byte) *httptest.ResponseRecorder {
	t.Helper()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for k, v := range fields {
		if err := writer.WriteField(k, v); err != nil {
			t.Fatal(err)
		}
	}
	if zipData != nil {
		part, err := writer.CreateFormFile("content", zipName)
		if err != nil {
			t.Fatal(err)
		}
		part.Write(zipData)
	}
	writer.Close()

	req := httptest.NewRequest(http.MethodPost, path, &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func zipWithout(t *testing.T, name string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create(name)
	w.Write([]byte("contenido"))
	zw.Close()
	return buf.Bytes()
}

// errorCode devuelve el código de error de una respuesta de la API.
func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body apiErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("la respuesta no tiene el formato de error: %v\n%s", err, rec.Body.String())
	}
	return body.Error.Codigo
}

func TestAPIListInstances(t *testing.T) {
	mux, _, _ := newAPIServer(t)
	rec := doAPI(t, mux, http.MethodGet, "/api/v1/instances")

	if rec.Code != http.StatusOK {
		t.Fatalf("estado = %d", rec.Code)
	}
	var list []apiInstance
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("JSON inválido: %v", err)
	}
	if len(list) != 1 || list[0].Hostname != "paisajes" || list[0].Estado != "activa" || list[0].IP != "192.168.10.30" {
		t.Errorf("instancias = %+v", list)
	}
}

func TestAPIGetInstance(t *testing.T) {
	mux, _, _ := newAPIServer(t)

	if rec := doAPI(t, mux, http.MethodGet, "/api/v1/instances/paisajes"); rec.Code != http.StatusOK {
		t.Errorf("paisajes: estado = %d", rec.Code)
	}
	rec := doAPI(t, mux, http.MethodGet, "/api/v1/instances/noexiste")
	if rec.Code != http.StatusNotFound || errorCode(t, rec) != "instancia_no_encontrada" {
		t.Errorf("noexiste: estado = %d, cuerpo = %s", rec.Code, rec.Body.String())
	}
}

func TestAPICreateDeployment(t *testing.T) {
	mux, _, _ := newAPIServer(t)

	rec := postForm(t, mux, "/api/v1/deployments", map[string]string{"hostname": "catalogo"}, "sitio.zip", validZip(t))

	if rec.Code != http.StatusAccepted {
		t.Fatalf("estado = %d, cuerpo = %s", rec.Code, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != "/api/v1/deployments/nuevo" {
		t.Errorf("Location = %q", loc)
	}
}

func TestAPICreateDeploymentWithoutContent(t *testing.T) {
	mux, _, _ := newAPIServer(t)

	rec := postForm(t, mux, "/api/v1/deployments", map[string]string{"hostname": "catalogo"}, "", nil)
	if rec.Code != http.StatusAccepted {
		t.Errorf("estado = %d, cuerpo = %s", rec.Code, rec.Body.String())
	}
}

func TestAPICreateDeploymentErrors(t *testing.T) {
	tests := []struct {
		name       string
		hostname   string
		zipName    string
		zipData    []byte
		wantStatus int
		wantCode   string
	}{
		{"nombre inválido", "Mal_Nombre", "", nil, http.StatusUnprocessableEntity, "hostname_invalido"},
		{"nombre vacío", "", "", nil, http.StatusUnprocessableEntity, "hostname_vacio"},
		{"nombre en uso", "paisajes", "", nil, http.StatusConflict, "hostname_en_uso"},
		{"zip sin index", "catalogo", "sitio.zip", zipWithout(t, "otra.html"), http.StatusUnprocessableEntity, "sin_index"},
		{"no es zip", "catalogo", "sitio.rar", []byte("x"), http.StatusUnprocessableEntity, "extension_invalida"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mux, _, _ := newAPIServer(t)
			rec := postForm(t, mux, "/api/v1/deployments", map[string]string{"hostname": tt.hostname}, tt.zipName, tt.zipData)

			if rec.Code != tt.wantStatus || errorCode(t, rec) != tt.wantCode {
				t.Errorf("estado = %d, cuerpo = %s; se esperaba %d %s", rec.Code, rec.Body.String(), tt.wantStatus, tt.wantCode)
			}
		})
	}
}

func TestAPIGetDeployment(t *testing.T) {
	mux, _, _ := newAPIServer(t)

	tests := []struct {
		id         string
		wantEstado string
		wantEtapa  string
	}{
		{"run", "en_curso", "esperar_arranque"},
		{"ok", "exito", ""},
		{"fail", "error", "registrar_dns"},
	}
	for _, tt := range tests {
		rec := doAPI(t, mux, http.MethodGet, "/api/v1/deployments/"+tt.id)
		var d apiDeployment
		if err := json.Unmarshal(rec.Body.Bytes(), &d); err != nil {
			t.Fatalf("%s: JSON inválido: %v", tt.id, err)
		}
		if rec.Code != http.StatusOK || d.Estado != tt.wantEstado || d.Etapa != tt.wantEtapa {
			t.Errorf("%s: estado HTTP = %d, despliegue = %+v", tt.id, rec.Code, d)
		}
	}

	rec := doAPI(t, mux, http.MethodGet, "/api/v1/deployments/noexiste")
	if rec.Code != http.StatusNotFound || errorCode(t, rec) != "despliegue_no_encontrado" {
		t.Errorf("noexiste: estado = %d, cuerpo = %s", rec.Code, rec.Body.String())
	}
}

func TestAPIRetryNotRetryable(t *testing.T) {
	mux, _, _ := newAPIServer(t)

	rec := doAPI(t, mux, http.MethodPost, "/api/v1/deployments/ok/retry")
	if rec.Code != http.StatusConflict || errorCode(t, rec) != "despliegue_no_reintentable" {
		t.Errorf("estado = %d, cuerpo = %s", rec.Code, rec.Body.String())
	}
}

func TestAPIPublish(t *testing.T) {
	mux, _, _ := newAPIServer(t)

	rec := postForm(t, mux, "/api/v1/instances/paisajes/deployments", nil, "sitio.zip", validZip(t))
	if rec.Code != http.StatusAccepted || rec.Header().Get("Location") != "/api/v1/deployments/nuevo" {
		t.Errorf("estado = %d, Location = %q", rec.Code, rec.Header().Get("Location"))
	}

	rec = postForm(t, mux, "/api/v1/instances/paisajes/deployments", nil, "", nil)
	if rec.Code != http.StatusUnprocessableEntity || errorCode(t, rec) != "archivo_faltante" {
		t.Errorf("sin archivo: estado = %d, cuerpo = %s", rec.Code, rec.Body.String())
	}
}

func TestAPIDeleteInstance(t *testing.T) {
	mux, _, prov := newAPIServer(t)

	rec := doAPI(t, mux, http.MethodDelete, "/api/v1/instances/paisajes")
	if rec.Code != http.StatusNoContent || rec.Body.Len() != 0 {
		t.Errorf("estado = %d, cuerpo = %q", rec.Code, rec.Body.String())
	}
	if !slices.Equal(prov.deleted, []string{"paisajes"}) {
		t.Errorf("eliminadas = %v", prov.deleted)
	}
}

func TestAPIDeleteBusyInstance(t *testing.T) {
	mux, h, prov := newAPIServer(t)
	h.deployments.(fakeDeployments).active["paisajes"] = true

	rec := doAPI(t, mux, http.MethodDelete, "/api/v1/instances/paisajes")
	if rec.Code != http.StatusConflict || errorCode(t, rec) != "instancia_ocupada" {
		t.Errorf("estado = %d, cuerpo = %s", rec.Code, rec.Body.String())
	}
	if len(prov.deleted) != 0 {
		t.Error("no debe eliminar una instancia con un despliegue en curso")
	}
}

func TestAPIDeleteFailureHidesDetails(t *testing.T) {
	mux, _, prov := newAPIServer(t)
	prov.err = errors.New("VBoxManage: detalle interno")

	rec := doAPI(t, mux, http.MethodDelete, "/api/v1/instances/paisajes")
	if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "VBoxManage") {
		t.Errorf("estado = %d, cuerpo = %s", rec.Code, rec.Body.String())
	}
}

func TestAPIPower(t *testing.T) {
	mux, h, _ := newAPIServer(t)

	rec := doAPI(t, mux, http.MethodPost, "/api/v1/instances/paisajes/power/stop")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("estado = %d, cuerpo = %s", rec.Code, rec.Body.String())
	}
	if pw := h.power.(*fakePower); !slices.Equal(pw.actions, []string{"paisajes stop"}) {
		t.Errorf("acciones = %v", pw.actions)
	}

	rec = doAPI(t, mux, http.MethodPost, "/api/v1/instances/paisajes/power/explotar")
	if rec.Code != http.StatusBadRequest || errorCode(t, rec) != "accion_invalida" {
		t.Errorf("acción inválida: estado = %d, cuerpo = %s", rec.Code, rec.Body.String())
	}
}

func TestAPIUnknownRoute(t *testing.T) {
	mux, _, _ := newAPIServer(t)

	rec := doAPI(t, mux, http.MethodGet, "/api/v1/no-existe")
	if rec.Code != http.StatusNotFound || errorCode(t, rec) != "ruta_no_encontrada" {
		t.Errorf("estado = %d, cuerpo = %s", rec.Code, rec.Body.String())
	}
}

func TestAPISpec(t *testing.T) {
	mux, _, _ := newAPIServer(t)

	rec := doAPI(t, mux, http.MethodGet, "/api/v1/openapi.yaml")
	if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Body.String(), "openapi: 3.0.3") {
		t.Errorf("estado = %d, inicio = %.40q", rec.Code, rec.Body.String())
	}
}

func TestAPIAuthentication(t *testing.T) {
	cfg := SecurityConfig{AuthUser: "admin", AuthPassword: "secreta", APIToken: "token-de-prueba"}
	h := Secure(okHandler, cfg)

	tests := []struct {
		name   string
		path   string
		header string
		basic  bool
		want   int
	}{
		{"API sin token", "/api/v1/instances", "", false, http.StatusUnauthorized},
		{"API con token incorrecto", "/api/v1/instances", "Bearer otro", false, http.StatusUnauthorized},
		{"API con token correcto", "/api/v1/instances", "Bearer token-de-prueba", false, http.StatusOK},
		{"API no acepta usuario y contraseña", "/api/v1/instances", "", true, http.StatusUnauthorized},
		{"especificación sin token", "/api/v1/openapi.yaml", "", false, http.StatusOK},
		{"interfaz sigue pidiendo usuario y contraseña", "/instances", "Bearer token-de-prueba", false, http.StatusUnauthorized},
		{"interfaz con usuario y contraseña", "/instances", "", true, http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			if tt.header != "" {
				req.Header.Set("Authorization", tt.header)
			}
			if tt.basic {
				req.SetBasicAuth("admin", "secreta")
			}
			rec := serve(h, req)
			if rec.Code != tt.want {
				t.Errorf("estado = %d, se esperaba %d", rec.Code, tt.want)
			}
		})
	}
}

func TestAPIDisabledWithoutToken(t *testing.T) {
	h := Secure(okHandler, SecurityConfig{})

	rec := serve(h, httptest.NewRequest(http.MethodGet, "/api/v1/instances", nil))
	if rec.Code != http.StatusServiceUnavailable || errorCode(t, rec) != "api_deshabilitada" {
		t.Errorf("estado = %d, cuerpo = %s", rec.Code, rec.Body.String())
	}
}

func TestAPIStatusIncludesUnreachable(t *testing.T) {
	snap := healthySnapshot()
	snap.Instances["paisajes"] = health.InstanceUnreachable
	h := newTestHandler(t, snap, &fakePower{}, nil)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := doAPI(t, mux, http.MethodGet, "/api/v1/instances/paisajes")
	var inst apiInstance
	json.Unmarshal(rec.Body.Bytes(), &inst)
	if inst.Estado != "sin_respuesta" {
		t.Errorf("estado = %q, se esperaba sin_respuesta", inst.Estado)
	}
}
