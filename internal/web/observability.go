package web

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/AndresFZV/httpaas-go/internal/health"
	"github.com/AndresFZV/httpaas-go/internal/metrics"
)

// defaultReadyMaxAge es la antigüedad máxima de la última verificación para considerar la app lista.
const defaultReadyMaxAge = 30 * time.Second

// instanceStateLabels traduce los estados de las instancias a las etiquetas de las métricas.
// Se publican todos, aunque valgan 0, para que las gráficas no tengan huecos.
var instanceStateLabels = []struct{ status, label string }{
	{statusActive, "activa"},
	{statusUnreachable, "sin_respuesta"},
	{statusStopped, "apagada"},
	{statusCreating, "creando"},
	{statusStarting, "encendiendo"},
	{statusStopping, "apagando"},
	{statusRestarting, "reiniciando"},
	{statusChecking, "verificando"},
}

// healthz indica que el proceso está vivo (liveness). Si deja de responder, un orquestador lo reinicia.
func (h *Handler) healthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte("ok\n"))
}

// componentStatus es un componente en la respuesta de /readyz.
type componentStatus struct {
	ID      string `json:"id"`
	Nombre  string `json:"nombre"`
	OK      bool   `json:"ok"`
	Detalle string `json:"detalle"`
}

// readinessResponse es la respuesta de /readyz.
type readinessResponse struct {
	Estado             string            `json:"estado"`
	Motivo             string            `json:"motivo,omitempty"`
	UltimaVerificacion string            `json:"ultima_verificacion,omitempty"`
	Componentes        []componentStatus `json:"componentes"`
}

// readyz indica si la app puede atender (readiness): el monitor verificó hace poco y VirtualBox responde.
// Responde 503 si no, para que un balanceador deje de enviarle tráfico sin reiniciarla.
// Que ns esté caída no la marca como no lista: la app sigue funcionando y puede encenderla.
func (h *Handler) readyz(w http.ResponseWriter, r *http.Request) {
	snap := h.monitor.Snapshot()
	resp := readinessResponse{Estado: "listo", Componentes: []componentStatus{}}

	for _, c := range snap.Components {
		resp.Componentes = append(resp.Componentes, componentStatus{ID: c.ID, Nombre: c.Name, OK: c.OK, Detalle: c.Detail})
	}
	if snap.Checked() {
		resp.UltimaVerificacion = snap.CheckedAt.Format(time.RFC3339)
	}

	resp.Motivo = notReadyReason(snap, h.readyMaxAge())
	status := http.StatusOK
	if resp.Motivo != "" {
		resp.Estado = "no_listo"
		status = http.StatusServiceUnavailable
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	writeJSON(w, resp)
}

// notReadyReason devuelve por qué la app no está lista, o "" si lo está.
func notReadyReason(snap health.Snapshot, maxAge time.Duration) string {
	if !snap.Checked() {
		return "todavía no se ha verificado la infraestructura"
	}
	if age := time.Since(snap.CheckedAt); age > maxAge {
		return "la última verificación tiene más de " + maxAge.String()
	}
	for _, c := range snap.Components {
		if c.ID == health.ComponentVirtualBox && !c.OK {
			return "VirtualBox no responde"
		}
	}
	return ""
}

func (h *Handler) readyMaxAge() time.Duration {
	if h.cfg.ReadyMaxAge > 0 {
		return h.cfg.ReadyMaxAge
	}
	return defaultReadyMaxAge
}

// metricsEndpoint publica las métricas en el formato de texto de Prometheus.
func (h *Handler) metricsEndpoint(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", metrics.ContentType)
	w.Header().Set("Cache-Control", "no-store")
	if err := h.metrics.WriteText(w); err != nil {
		slog.Error("no se pudieron escribir las métricas", "componente", "web", "error", err)
	}
}

// registerGauges declara las métricas que se calculan al consultarlas, a partir del estado actual.
func (h *Handler) registerGauges(reg *metrics.Registry) {
	started := time.Now()

	reg.GaugeFunc("httpaas_instances", "Instancias registradas, por estado.", func() []metrics.Sample {
		_, views, _ := h.currentState()
		counts := make(map[string]int, len(instanceStateLabels))
		for _, v := range views {
			counts[v.Status]++
		}
		samples := make([]metrics.Sample, 0, len(instanceStateLabels))
		for _, s := range instanceStateLabels {
			samples = append(samples, metrics.Sample{
				Labels: []metrics.Label{metrics.L("estado", s.label)},
				Value:  float64(counts[s.status]),
			})
		}
		return samples
	})

	reg.GaugeFunc("httpaas_component_up", "1 si el componente respondió en la última verificación, 0 si no.", func() []metrics.Sample {
		snap := h.monitor.Snapshot()
		samples := make([]metrics.Sample, 0, len(snap.Components))
		for _, c := range snap.Components {
			value := 0.0
			if c.OK {
				value = 1
			}
			samples = append(samples, metrics.Sample{Labels: []metrics.Label{metrics.L("componente", c.ID)}, Value: value})
		}
		return samples
	})

	reg.GaugeFunc("httpaas_free_ips", "Direcciones IP libres en el rango de las instancias.", func() []metrics.Sample {
		capacity := ipCount(h.cfg.FirstIP, h.cfg.LastIP)
		return []metrics.Sample{{Value: float64(capacity - len(h.store.List()))}}
	})

	reg.GaugeFunc("httpaas_last_check_age_seconds", "Segundos desde la última verificación de la infraestructura.", func() []metrics.Sample {
		snap := h.monitor.Snapshot()
		if !snap.Checked() {
			return nil
		}
		return []metrics.Sample{{Value: time.Since(snap.CheckedAt).Seconds()}}
	})

	reg.GaugeFunc("httpaas_uptime_seconds", "Segundos desde que arrancó la aplicación.", func() []metrics.Sample {
		return []metrics.Sample{{Value: time.Since(started).Seconds()}}
	})
}
