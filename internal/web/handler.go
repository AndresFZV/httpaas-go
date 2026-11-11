package web

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"net/netip"
	"net/url"
	"slices"

	"github.com/AndresFZV/httpaas-go/internal/deploy"
	"github.com/AndresFZV/httpaas-go/internal/instance"
	"github.com/AndresFZV/httpaas-go/internal/provision"
)

const recentLimit = 5

// Provisioner crea y elimina instancias completas (VM, red y DNS).
type Provisioner interface {
	Provision(ctx context.Context, hostname string, report provision.ReportFunc) (instance.Instance, error)
	Delete(ctx context.Context, hostname string) error
}

// Deployer publica el contenido de un .zip en una instancia.
type Deployer interface {
	Deploy(ctx context.Context, host string, archive io.Reader) error
}

// Config son los datos de la infraestructura que muestra la interfaz y las rutas de sus archivos.
type Config struct {
	Domain       string
	DNSServer    string
	FirstIP      netip.Addr
	LastIP       netip.Addr
	TemplatesDir string
	StaticDir    string
}

// Handler agrupa las dependencias de los handlers HTTP de la aplicación.
type Handler struct {
	cfg         Config
	store       *instance.Store
	provisioner Provisioner
	deployer    Deployer
	pages       map[string]*template.Template
}

// NewHandler carga las páginas de la interfaz y prepara los handlers.
func NewHandler(cfg Config, store *instance.Store, provisioner Provisioner, deployer Deployer) (*Handler, error) {
	pages, err := loadPages(cfg.TemplatesDir)
	if err != nil {
		return nil, err
	}

	return &Handler{
		cfg:         cfg,
		store:       store,
		provisioner: provisioner,
		deployer:    deployer,
		pages:       pages,
	}, nil
}

// RegisterRoutes registra las rutas de la aplicación en mux.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	fileServer := http.FileServer(http.Dir(h.cfg.StaticDir))
	mux.Handle("GET /static/", http.StripPrefix("/static/", fileServer))

	mux.HandleFunc("GET /{$}", h.dashboard)
	mux.HandleFunc("GET /instances", h.instances)
	mux.HandleFunc("GET /deploy", h.deployForm)
	mux.HandleFunc("POST /deploy", h.deploy)
	mux.HandleFunc("POST /instances/{hostname}/delete", h.deleteInstance)
}

// stats son los números de las tarjetas del dashboard.
type stats struct {
	Total      int
	FreeIPs    int
	IPCapacity int
	IPRange    string
}

// pageData son los datos que reciben todas las páginas.
type pageData struct {
	Page       string // nombre del archivo en templates/pages, también marca el menú activo
	Breadcrumb string

	Domain    string
	DNSServer string
	Instances []instance.Instance
	Recent    []instance.Instance
	Stats     stats

	Error string // error general, se muestra arriba de la página
	Toast string // aviso flotante

	Hostname       string // valor del formulario de despliegue
	FormError      string // error del formulario de despliegue
	MaxUploadMB    int64
	MaxUploadBytes int64
}

func (h *Handler) dashboard(w http.ResponseWriter, r *http.Request) {
	data := h.newPage(r, "dashboard", "Dashboard")
	data.Recent = newestFirst(data.Instances, recentLimit)
	h.render(w, http.StatusOK, data)
}

func (h *Handler) instances(w http.ResponseWriter, r *http.Request) {
	h.render(w, http.StatusOK, h.newPage(r, "instances", "Instancias"))
}

func (h *Handler) deployForm(w http.ResponseWriter, r *http.Request) {
	h.render(w, http.StatusOK, h.newPage(r, "deploy", "Nuevo despliegue"))
}

// newPage arma los datos comunes de una página, incluidos los avisos que llegan por la URL.
func (h *Handler) newPage(r *http.Request, page, breadcrumb string) pageData {
	instances := h.store.List()
	capacity := ipCount(h.cfg.FirstIP, h.cfg.LastIP)
	toast, errMsg := flashMessages(r.URL.Query())

	return pageData{
		Page:       page,
		Breadcrumb: breadcrumb,
		Domain:     h.cfg.Domain,
		DNSServer:  h.cfg.DNSServer,
		Instances:  instances,
		Stats: stats{
			Total:      len(instances),
			FreeIPs:    capacity - len(instances),
			IPCapacity: capacity,
			IPRange:    fmt.Sprintf("%s – %s", h.cfg.FirstIP, h.cfg.LastIP),
		},
		Error:          errMsg,
		Toast:          toast,
		MaxUploadMB:    deploy.MaxArchiveSize >> 20,
		MaxUploadBytes: deploy.MaxArchiveSize,
	}
}

// render ejecuta la página en un buffer y solo escribe la respuesta si no hubo errores.
func (h *Handler) render(w http.ResponseWriter, status int, data pageData) {
	tmpl, ok := h.pages[data.Page]
	if !ok {
		log.Printf("renderizar: no existe la página %q", data.Page)
		http.Error(w, "Error interno del servidor", http.StatusInternalServerError)
		return
	}

	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "layout", data); err != nil {
		log.Printf("renderizar %s: %v", data.Page, err)
		http.Error(w, "Error interno del servidor", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if _, err := buf.WriteTo(w); err != nil {
		log.Printf("escribir respuesta: %v", err)
	}
}

// flashMessages traduce los parámetros de la URL que dejan las redirecciones en avisos.
// Solo acepta hostnames válidos, para que un enlace no pueda mostrar texto arbitrario.
func flashMessages(query url.Values) (toast, errMsg string) {
	hostname := func(key string) string {
		name := instance.NormalizeHostname(query.Get(key))
		if instance.ValidateHostname(name) != nil {
			return ""
		}
		return name
	}

	if name := hostname("created"); name != "" {
		toast = fmt.Sprintf("Instancia %s creada", name)
		if query.Get("published") == "1" {
			toast += " y contenido publicado"
		}
	} else if name := hostname("deleted"); name != "" {
		toast = fmt.Sprintf("Instancia %s eliminada", name)
	}

	if name := hostname("publish_failed"); name != "" {
		errMsg = fmt.Sprintf("La instancia %s se creó, pero no se pudo publicar el contenido; revise el log del servidor.", name)
	}
	return toast, errMsg
}

// newestFirst devuelve hasta limit instancias, de la más reciente a la más antigua.
func newestFirst(list []instance.Instance, limit int) []instance.Instance {
	recent := slices.Clone(list)
	slices.Reverse(recent)
	if len(recent) > limit {
		recent = recent[:limit]
	}
	return recent
}

// ipCount devuelve cuántas IPv4 hay en el rango [first, last].
func ipCount(first, last netip.Addr) int {
	if !first.Is4() || !last.Is4() || last.Less(first) {
		return 0
	}
	a, b := first.As4(), last.As4()
	return int(binary.BigEndian.Uint32(b[:])-binary.BigEndian.Uint32(a[:])) + 1
}
