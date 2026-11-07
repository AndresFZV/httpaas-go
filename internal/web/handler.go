package web

import (
	"bytes"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"path/filepath"

	"github.com/AndresFZV/httpaas-go/internal/instance"
)

const pageTitle = "Gestión de páginas y aplicaciones web"

// Handler agrupa las dependencias de los handlers HTTP de la aplicación.
type Handler struct {
	store     *instance.Store
	templates *template.Template
	staticDir string
}

// NewHandler carga las plantillas HTML de templatesDir y prepara el servidor de archivos estáticos.
func NewHandler(store *instance.Store, templatesDir, staticDir string) (*Handler, error) {
	tmpl, err := template.ParseGlob(filepath.Join(templatesDir, "*.html"))
	if err != nil {
		return nil, fmt.Errorf("cargar plantillas: %w", err)
	}

	return &Handler{
		store:     store,
		templates: tmpl,
		staticDir: staticDir,
	}, nil
}

// RegisterRoutes registra las rutas de la aplicación en mux.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	fileServer := http.FileServer(http.Dir(h.staticDir))
	mux.Handle("GET /static/", http.StripPrefix("/static/", fileServer))

	mux.HandleFunc("GET /{$}", h.index)
	mux.HandleFunc("POST /instances", h.createInstance)
	mux.HandleFunc("POST /instances/deploy", h.deployInstance)
	mux.HandleFunc("POST /instances/{hostname}/delete", h.deleteInstance)
}

// indexData son los datos que recibe la plantilla index.html.
type indexData struct {
	Title     string
	Hostname  string
	Error     string
	Success   string
	Instances []instance.Instance
}

// index muestra la página principal; el parámetro host rellena el campo del formulario.
func (h *Handler) index(w http.ResponseWriter, r *http.Request) {
	hostname := instance.NormalizeHostname(r.URL.Query().Get("host"))
	h.renderIndex(w, http.StatusOK, indexData{Hostname: hostname})
}

// renderIndex completa los datos comunes de la página principal y la renderiza.
func (h *Handler) renderIndex(w http.ResponseWriter, status int, data indexData) {
	data.Title = pageTitle
	data.Instances = h.store.List()
	h.render(w, status, "index.html", data)
}

// render ejecuta la plantilla name en un buffer y solo escribe la respuesta si no hubo errores.
func (h *Handler) render(w http.ResponseWriter, status int, name string, data any) {
	var buf bytes.Buffer
	if err := h.templates.ExecuteTemplate(&buf, name, data); err != nil {
		log.Printf("renderizar %s: %v", name, err)
		http.Error(w, "Error interno del servidor", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if _, err := buf.WriteTo(w); err != nil {
		log.Printf("escribir respuesta: %v", err)
	}
}
