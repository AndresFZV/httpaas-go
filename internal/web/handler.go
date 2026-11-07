package web

import (
	"bytes"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"path/filepath"
)

const pageTitle = "Gestión de páginas y aplicaciones web"

// Handler agrupa las dependencias de los handlers HTTP de la aplicación.
type Handler struct {
	templates *template.Template
	staticDir string
}

// NewHandler carga las plantillas HTML de templatesDir y prepara el servidor de archivos estáticos.
func NewHandler(templatesDir, staticDir string) (*Handler, error) {
	tmpl, err := template.ParseGlob(filepath.Join(templatesDir, "*.html"))
	if err != nil {
		return nil, fmt.Errorf("cargar plantillas: %w", err)
	}

	return &Handler{
		templates: tmpl,
		staticDir: staticDir,
	}, nil
}

// RegisterRoutes registra las rutas de la aplicación en mux.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	fileServer := http.FileServer(http.Dir(h.staticDir))
	mux.Handle("GET /static/", http.StripPrefix("/static/", fileServer))

	mux.HandleFunc("GET /{$}", h.index)
}

type indexData struct {
	Title string
}

func (h *Handler) index(w http.ResponseWriter, r *http.Request) {
	h.render(w, http.StatusOK, "index.html", indexData{Title: pageTitle})
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
