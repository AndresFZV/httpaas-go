package web

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/url"

	"github.com/AndresFZV/httpaas-go/internal/deployment"
)

// deploymentPage muestra el progreso, el éxito o el error de un despliegue.
func (h *Handler) deploymentPage(w http.ResponseWriter, r *http.Request) {
	data := h.newPage(r, "deployment", "Nuevo despliegue")

	snap, err := h.deployments.Snapshot(r.PathValue("id"))
	if err != nil {
		h.render(w, http.StatusNotFound, data)
		return
	}
	data.Deployment = &snap
	h.render(w, http.StatusOK, data)
}

// retryDeployment lanza de nuevo un despliegue fallido y redirige a la página del nuevo intento.
func (h *Handler) retryDeployment(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	newID, err := h.deployments.Retry(id)
	if err != nil {
		status := http.StatusConflict
		if errors.Is(err, deployment.ErrNotFound) {
			status = http.StatusNotFound
		}

		data := h.newPage(r, "deployment", "Nuevo despliegue")
		if snap, snapErr := h.deployments.Snapshot(id); snapErr == nil {
			data.Deployment = &snap
		}
		data.Error = err.Error()
		h.render(w, status, data)
		return
	}

	log.Printf("despliegue %s reintentado como %s", id, newID)
	redirectTo(w, r, "/deployments/"+url.PathEscape(newID))
}

// deploymentStatus devuelve el estado de un despliegue en JSON para la página de progreso.
func (h *Handler) deploymentStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")

	snap, err := h.deployments.Snapshot(r.PathValue("id"))
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		writeJSON(w, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, snap)
}

func writeJSON(w http.ResponseWriter, value any) {
	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Printf("escribir JSON: %v", err)
	}
}
