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
	"github.com/AndresFZV/httpaas-go/internal/deployment"
	"github.com/AndresFZV/httpaas-go/internal/health"
	"github.com/AndresFZV/httpaas-go/internal/instance"
	"github.com/AndresFZV/httpaas-go/internal/power"
)

const recentLimit = 5

// stepIndex expone a las plantillas los índices de las etapas que necesitan por nombre.
var stepIndex = map[string]int{
	"validate":    deployment.StepValidate,
	"waitBoot":    deployment.StepWaitBoot,
	"waitFinalIP": deployment.StepWaitFinalIP,
	"registerDNS": deployment.StepRegisterDNS,
	"publish":     deployment.StepPublish,
}

// Provisioner elimina instancias completas (VM, red y DNS).
type Provisioner interface {
	Delete(ctx context.Context, hostname string) error
}

// Deployments ejecuta despliegues en segundo plano y permite consultar su estado.
type Deployments interface {
	Start(hostname string, archive io.Reader, archiveName string) (string, error)
	Publish(hostname string, ip netip.Addr, archive io.Reader, archiveName string) (string, error)
	Retry(id string) (string, error)
	Snapshot(id string) (deployment.Snapshot, error)
	ActiveHostnames() map[string]bool
}

// HealthMonitor entrega el resultado de la última verificación de la infraestructura.
type HealthMonitor interface {
	Snapshot() health.Snapshot
}

// PowerController enciende, apaga y reinicia VMs, y entrega las acciones en curso.
type PowerController interface {
	Do(ctx context.Context, name string, action power.Action) error
	Pending() map[string]power.Pending
}

// Config son los datos de la infraestructura que muestra la interfaz y las rutas de sus archivos.
type Config struct {
	Domain       string
	DNSServer    string
	NSVMName     string
	FirstIP      netip.Addr
	LastIP       netip.Addr
	TemplatesDir string
	StaticDir    string
}

// Deps son los servicios que usa la interfaz.
type Deps struct {
	Store       *instance.Store
	Provisioner Provisioner
	Deployments Deployments
	Monitor     HealthMonitor
	Power       PowerController
}

// Handler agrupa las dependencias de los handlers HTTP de la aplicación.
type Handler struct {
	cfg         Config
	store       *instance.Store
	provisioner Provisioner
	deployments Deployments
	monitor     HealthMonitor
	power       PowerController
	pages       map[string]*template.Template
}

// NewHandler carga las páginas de la interfaz y prepara los handlers.
func NewHandler(cfg Config, deps Deps) (*Handler, error) {
	pages, err := loadPages(cfg.TemplatesDir)
	if err != nil {
		return nil, err
	}

	return &Handler{
		cfg:         cfg,
		store:       deps.Store,
		provisioner: deps.Provisioner,
		deployments: deps.Deployments,
		monitor:     deps.Monitor,
		power:       deps.Power,
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
	mux.HandleFunc("GET /deployments/{id}", h.deploymentPage)
	mux.HandleFunc("POST /deployments/{id}/retry", h.retryDeployment)
	mux.HandleFunc("POST /instances/{hostname}/delete", h.deleteInstance)
	mux.HandleFunc("POST /instances/{hostname}/publish", h.publishInstance)
	mux.HandleFunc("POST /instances/{hostname}/power/{action}", h.instancePower)
	mux.HandleFunc("POST /infrastructure/ns/power/{action}", h.nsPower)
	mux.HandleFunc("GET /api/deployments/{id}", h.deploymentStatus)
	mux.HandleFunc("GET /api/status", h.statusAPI)
}

// stats son los números de las tarjetas del dashboard.
type stats struct {
	Total         int
	Active        int
	ActivePercent int
	FreeIPs       int
	IPCapacity    int
	IPRange       string
}

// pageData son los datos que reciben todas las páginas.
type pageData struct {
	Page       string // nombre del archivo en templates/pages, también marca el menú activo
	Breadcrumb string

	Domain    string
	DNSServer string
	Instances []instanceView
	Recent    []instanceView
	Stats     stats
	Health    healthView
	NS        nsView

	Error string // error general, se muestra arriba de la página
	Toast string // aviso flotante

	Hostname       string // valor del formulario de despliegue
	FormError      string // error del formulario de despliegue
	MaxUploadMB    int64
	MaxUploadBytes int64

	Deployment *deployment.Snapshot // despliegue que muestra la página de progreso
	Steps      []deployment.Step
	StepIndex  map[string]int
}

// currentState combina la última verificación con los despliegues y las acciones en curso.
func (h *Handler) currentState() (health.Snapshot, []instanceView, nsView) {
	snap := h.monitor.Snapshot()
	pending := h.power.Pending()
	views := instanceViews(h.store.List(), snap, h.deployments.ActiveHostnames(), pending)
	return snap, views, newNSView(h.cfg.NSVMName, snap, pending)
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
	snap, views, ns := h.currentState()
	capacity := ipCount(h.cfg.FirstIP, h.cfg.LastIP)
	toast, errMsg := flashMessages(r.URL.Query())

	active := countActive(views)
	activePercent := 0
	if len(views) > 0 {
		activePercent = active * 100 / len(views)
	}

	return pageData{
		Page:       page,
		Breadcrumb: breadcrumb,
		Domain:     h.cfg.Domain,
		DNSServer:  h.cfg.DNSServer,
		Instances:  views,
		Health:     newHealthView(snap),
		NS:         ns,
		Stats: stats{
			Total:         len(views),
			Active:        active,
			ActivePercent: activePercent,
			FreeIPs:       capacity - len(views),
			IPCapacity:    capacity,
			IPRange:       fmt.Sprintf("%s – %s", h.cfg.FirstIP, h.cfg.LastIP),
		},
		Error:          errMsg,
		Toast:          toast,
		MaxUploadMB:    deploy.MaxArchiveSize >> 20,
		MaxUploadBytes: deploy.MaxArchiveSize,
		Steps:          deployment.Steps,
		StepIndex:      stepIndex,
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
// Solo acepta hostnames válidos y valores conocidos, para que un enlace no pueda mostrar texto arbitrario.
func flashMessages(query url.Values) (toast, errMsg string) {
	hostname := func(key string) string {
		name := instance.NormalizeHostname(query.Get(key))
		if instance.ValidateHostname(name) != nil {
			return ""
		}
		return name
	}

	messages := []struct{ key, format string }{
		{"created", "Instancia %s creada"},
		{"deleted", "Instancia %s eliminada"},
		{"start", "Encendiendo %s; responderá en aproximadamente un minuto"},
		{"stop", "Apagando %s"},
		{"restart", "Reiniciando %s; responderá en aproximadamente un minuto"},
	}
	for _, m := range messages {
		if name := hostname(m.key); name != "" {
			toast = fmt.Sprintf(m.format, name)
			if m.key == "created" && query.Get("published") == "1" {
				toast += " y contenido publicado"
			}
			break
		}
	}

	switch query.Get("ns") {
	case "start":
		toast = "Encendiendo el servidor DNS; estará listo en aproximadamente un minuto"
	case "restart":
		toast = "Reiniciando el servidor DNS; estará listo en aproximadamente un minuto"
	}

	if name := hostname("publish_failed"); name != "" {
		errMsg = fmt.Sprintf("La instancia %s se creó, pero no se pudo publicar el contenido; revise el log del servidor.", name)
	}
	return toast, errMsg
}

// newestFirst devuelve hasta limit elementos, del más reciente al más antiguo.
func newestFirst[T any](list []T, limit int) []T {
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
