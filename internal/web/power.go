package web

import (
	"errors"
	"log"
	"net/http"
	"net/url"
	"time"

	"github.com/AndresFZV/httpaas-go/internal/instance"
	"github.com/AndresFZV/httpaas-go/internal/power"
)

const powerTimeout = 45 * time.Second

var errPowerFailed = errors.New("no se pudo completar la acción en la máquina virtual; revise el log del servidor")

// instancePower enciende, apaga o reinicia la VM de una instancia.
func (h *Handler) instancePower(w http.ResponseWriter, r *http.Request) {
	hostname := instance.NormalizeHostname(r.PathValue("hostname"))

	if _, err := h.store.Get(hostname); err != nil {
		h.renderInstancesError(w, r, err)
		return
	}
	action, err := power.ParseAction(r.PathValue("action"))
	if err != nil {
		h.renderInstancesError(w, r, err)
		return
	}
	if h.deployments.ActiveHostnames()[hostname] {
		h.renderInstancesError(w, r, deploymentBusy)
		return
	}

	// La solicitud se registra antes de ejecutarla, para que el log siga el orden real de los hechos.
	log.Printf("[web] solicitud: %s %s", action.Label(), hostname)

	ctx, cancel := backgroundContext(r, powerTimeout)
	defer cancel()

	if err := h.power.Do(ctx, hostname, action); err != nil {
		h.renderInstancesError(w, r, infrastructureError(err, errPowerFailed, string(action)+" "+hostname))
		return
	}

	redirectTo(w, r, "/instances?"+string(action)+"="+url.QueryEscape(hostname))
}

// nsPower enciende o reinicia la VM del servidor DNS. Apagarla desde la interfaz no se ofrece.
func (h *Handler) nsPower(w http.ResponseWriter, r *http.Request) {
	action, err := power.ParseAction(r.PathValue("action"))
	if err == nil && action == power.ActionStop {
		err = power.ErrUnknownAction
	}
	if err != nil {
		h.renderDashboardError(w, r, err)
		return
	}

	// La solicitud se registra antes de ejecutarla, para que el log siga el orden real de los hechos.
	log.Printf("[web] solicitud: %s %s", action.Label(), h.cfg.NSVMName)

	ctx, cancel := backgroundContext(r, powerTimeout)
	defer cancel()

	if err := h.power.Do(ctx, h.cfg.NSVMName, action); err != nil {
		h.renderDashboardError(w, r, infrastructureError(err, errPowerFailed, string(action)+" "+h.cfg.NSVMName))
		return
	}

	redirectTo(w, r, "/?ns="+string(action))
}

// statusResponse es el estado actual de las instancias, la infraestructura y ns.
type statusResponse struct {
	Instances map[string]string `json:"instances"`
	Health    string            `json:"health"`
	NS        string            `json:"ns"`
}

// statusAPI devuelve el estado actual de las instancias, la infraestructura y ns.
func (h *Handler) statusAPI(w http.ResponseWriter, r *http.Request) {
	snap, views, ns := h.currentState()

	resp := statusResponse{
		Instances: make(map[string]string, len(views)),
		Health:    newHealthView(snap).Tone(),
		NS:        ns.State,
	}
	for _, v := range views {
		resp.Instances[v.Hostname] = v.Status
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, resp)
}

// renderDashboardError muestra el dashboard con el error arriba.
func (h *Handler) renderDashboardError(w http.ResponseWriter, r *http.Request, err error) {
	status, known := statusFor(err)
	message := err.Error()
	if !known {
		log.Printf("error inesperado: %v", err)
		message = "Ocurrió un error interno; revise el log del servidor"
	}

	data := h.newPage(r, "dashboard", "Dashboard")
	data.Recent = newestFirst(data.Instances, recentLimit)
	data.Error = message
	h.render(w, status, data)
}
