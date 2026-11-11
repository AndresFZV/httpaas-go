package web

import (
	"time"

	"github.com/AndresFZV/httpaas-go/internal/health"
	"github.com/AndresFZV/httpaas-go/internal/instance"
	"github.com/AndresFZV/httpaas-go/internal/power"
)

// Estados de una instancia en la interfaz; también son clases CSS y valores del filtro.
const (
	statusActive      = "active"
	statusUnreachable = "unreachable"
	statusStopped     = "stopped"
	statusCreating    = "creating"
	statusStarting    = "starting"
	statusStopping    = "stopping"
	statusRestarting  = "restarting"
	statusChecking    = "checking"
)

// Estados de la VM del servidor DNS.
const (
	nsRunning    = "running"
	nsStopped    = "stopped"
	nsStarting   = "starting"
	nsRestarting = "restarting"
	nsUnknown    = "unknown"
)

// transitionGrace evita dar por terminada una transición con una verificación que empezó antes de la acción.
const transitionGrace = 5 * time.Second

// instanceView es una instancia con su estado, tal como la muestran las tablas.
type instanceView struct {
	instance.Instance
	Status string
}

// StatusLabel devuelve el texto de la etiqueta de estado.
func (v instanceView) StatusLabel() string {
	switch v.Status {
	case statusActive:
		return "Activa"
	case statusUnreachable:
		return "Sin respuesta"
	case statusStopped:
		return "Apagada"
	case statusCreating:
		return "Creando"
	case statusStarting:
		return "Encendiendo"
	case statusStopping:
		return "Apagando"
	case statusRestarting:
		return "Reiniciando"
	default:
		return "Verificando"
	}
}

// Transitional indica si el estado está cambiando; la página se actualiza sola mientras lo esté.
func (v instanceView) Transitional() bool {
	switch v.Status {
	case statusCreating, statusStarting, statusStopping, statusRestarting, statusChecking:
		return true
	}
	return false
}

// CanStart indica si se puede encender la instancia.
func (v instanceView) CanStart() bool {
	return v.Status == statusStopped
}

// CanControl indica si se puede publicar, reiniciar o apagar la instancia.
func (v instanceView) CanControl() bool {
	return v.Status == statusActive || v.Status == statusUnreachable
}

// CanDelete indica si se puede eliminar la instancia; no se permite durante una transición.
func (v instanceView) CanDelete() bool {
	return !v.Transitional() || v.Status == statusChecking
}

// instanceViews combina el registro con la última verificación, los despliegues y las acciones de encendido.
func instanceViews(list []instance.Instance, snap health.Snapshot, creating map[string]bool, pending map[string]power.Pending) []instanceView {
	views := make([]instanceView, len(list))
	for i, inst := range list {
		views[i] = instanceView{Instance: inst, Status: resolveStatus(inst.Hostname, snap, creating, pending)}
	}
	return views
}

// resolveStatus decide el estado de una instancia. La VM se llama igual que el hostname.
func resolveStatus(hostname string, snap health.Snapshot, creating map[string]bool, pending map[string]power.Pending) string {
	if creating[hostname] {
		return statusCreating
	}

	if p, ok := pending[hostname]; ok {
		switch p.Action {
		case power.ActionStop:
			return statusStopping
		case power.ActionStart, power.ActionRestart:
			confirmed := confirmedAfter(snap, p.StartedAt) && snap.Instances[hostname] == health.InstanceActive
			if !confirmed {
				if p.Action == power.ActionStart {
					return statusStarting
				}
				return statusRestarting
			}
		}
	}

	if running, known := snap.VMRunning(hostname); known && !running {
		return statusStopped
	}
	if result, ok := snap.Instances[hostname]; ok {
		if result == health.InstanceActive {
			return statusActive
		}
		return statusUnreachable
	}
	return statusChecking
}

// confirmedAfter indica si la última verificación terminó claramente después de since.
func confirmedAfter(snap health.Snapshot, since time.Time) bool {
	return snap.CheckedAt.After(since.Add(transitionGrace))
}

// countActive cuenta las instancias que respondieron por HTTP.
func countActive(views []instanceView) int {
	active := 0
	for _, v := range views {
		if v.Status == statusActive {
			active++
		}
	}
	return active
}

// healthView es el estado de la infraestructura, tal como lo muestra la interfaz.
type healthView struct {
	Checked     bool
	Operational bool
	CheckedAt   time.Time
	Components  []health.Component
	Failing     []string // nombres de los componentes con falla
}

func newHealthView(snap health.Snapshot) healthView {
	view := healthView{
		Checked:     snap.Checked(),
		Operational: snap.Operational(),
		CheckedAt:   snap.CheckedAt,
		Components:  snap.Components,
	}
	for _, c := range snap.Components {
		if !c.OK {
			view.Failing = append(view.Failing, c.Name)
		}
	}
	return view
}

// Tone devuelve la clase de color del indicador: ok, danger o pending.
func (h healthView) Tone() string {
	switch {
	case !h.Checked:
		return "pending"
	case h.Operational:
		return "ok"
	default:
		return "danger"
	}
}

// StatusLabel devuelve el estado general en una palabra.
func (h healthView) StatusLabel() string {
	switch {
	case !h.Checked:
		return "Verificando"
	case h.Operational:
		return "Operativa"
	default:
		return "Con fallas"
	}
}

// nsView es el estado de la VM del servidor DNS, para sus botones en el dashboard.
type nsView struct {
	State   string
	Failing bool // la infraestructura tiene fallas
}

func newNSView(name string, snap health.Snapshot, pending map[string]power.Pending) nsView {
	view := nsView{Failing: snap.Checked() && !snap.Operational()}

	if p, ok := pending[name]; ok && !(confirmedAfter(snap, p.StartedAt) && snap.Operational()) {
		switch p.Action {
		case power.ActionStart:
			view.State = nsStarting
			return view
		case power.ActionRestart:
			view.State = nsRestarting
			return view
		}
	}

	running, known := snap.VMRunning(name)
	switch {
	case !known:
		view.State = nsUnknown
	case running:
		view.State = nsRunning
	default:
		view.State = nsStopped
	}
	return view
}

// CanStart indica si se ofrece encender ns.
func (n nsView) CanStart() bool {
	return n.State == nsStopped
}

// CanRestart indica si se ofrece reiniciar ns: está encendida pero la plataforma tiene fallas.
func (n nsView) CanRestart() bool {
	return n.State == nsRunning && n.Failing
}

// Transitional indica si ns se está encendiendo o reiniciando.
func (n nsView) Transitional() bool {
	return n.State == nsStarting || n.State == nsRestarting
}

// TransitionLabel devuelve el texto que se muestra durante la transición.
func (n nsView) TransitionLabel() string {
	if n.State == nsRestarting {
		return "Reiniciando ns…"
	}
	return "Encendiendo ns…"
}
