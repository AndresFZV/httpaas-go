package web

import (
	"time"

	"github.com/AndresFZV/httpaas-go/internal/health"
	"github.com/AndresFZV/httpaas-go/internal/instance"
)

// Estados de una instancia en la interfaz; también son clases CSS y valores del filtro.
const (
	statusActive      = "active"
	statusUnreachable = "unreachable"
	statusCreating    = "creating"
	statusChecking    = "checking"
)

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
	case statusCreating:
		return "Creando"
	default:
		return "Verificando"
	}
}

// instanceViews combina el registro con la última verificación y los despliegues en curso.
func instanceViews(list []instance.Instance, snap health.Snapshot, creating map[string]bool) []instanceView {
	views := make([]instanceView, len(list))
	for i, inst := range list {
		status := statusChecking
		if creating[inst.Hostname] {
			status = statusCreating
		} else if result, ok := snap.Instances[inst.Hostname]; ok {
			status = statusUnreachable
			if result == health.InstanceActive {
				status = statusActive
			}
		}
		views[i] = instanceView{Instance: inst, Status: status}
	}
	return views
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
