package web

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/AndresFZV/httpaas-go/internal/deploy"
	"github.com/AndresFZV/httpaas-go/internal/deployment"
	"github.com/AndresFZV/httpaas-go/internal/instance"
	"github.com/AndresFZV/httpaas-go/internal/power"
)

// apiPrefix es la raíz de la API REST versionada. Las rutas /api/status y /api/deployments/{id}
// sin versión son internas de la interfaz web y pueden cambiar.
const apiPrefix = "/api/v1/"

// apiErrorCodes asigna a cada error conocido un código estable que los clientes pueden comparar
// sin depender del texto del mensaje.
var apiErrorCodes = []struct {
	err  error
	code string
}{
	{instance.ErrHostnameEmpty, "hostname_vacio"},
	{instance.ErrHostnameInvalid, "hostname_invalido"},
	{instance.ErrHostnameReserved, "hostname_reservado"},
	{instance.ErrHostnameTaken, "hostname_en_uso"},
	{instance.ErrNotFound, "instancia_no_encontrada"},
	{instance.ErrNoFreeIP, "sin_ips_libres"},
	{deploy.ErrArchiveMissing, "archivo_faltante"},
	{deploy.ErrArchiveExtension, "extension_invalida"},
	{deploy.ErrArchiveTooLarge, "archivo_muy_grande"},
	{deploy.ErrArchiveTooBig, "contenido_muy_grande"},
	{deploy.ErrArchiveCorrupt, "zip_invalido"},
	{deploy.ErrArchiveUnsafePath, "ruta_no_permitida"},
	{deploy.ErrArchiveNoIndex, "sin_index"},
	{deployment.ErrNotFound, "despliegue_no_encontrado"},
	{deployment.ErrNotRetryable, "despliegue_no_reintentable"},
	{deployment.ErrAlreadyRetried, "despliegue_ya_reintentado"},
	{deployment.ErrBusy, "instancia_ocupada"},
	{power.ErrInProgress, "accion_en_curso"},
	{power.ErrUnknownAction, "accion_invalida"},
	{errInvalidForm, "formulario_invalido"},
}

// apiErrorBody es el formato único de los errores de la API.
type apiErrorBody struct {
	Error apiError `json:"error"`
}

type apiError struct {
	Codigo  string `json:"codigo"`
	Mensaje string `json:"mensaje"`
}

// apiInstance es una instancia en las respuestas de la API.
type apiInstance struct {
	Hostname string    `json:"hostname"`
	IP       string    `json:"ip"`
	FQDN     string    `json:"fqdn"`
	URL      string    `json:"url"`
	Estado   string    `json:"estado"`
	Creada   time.Time `json:"creada"`
}

// apiDeployment es un despliegue en las respuestas de la API.
type apiDeployment struct {
	ID           string `json:"id"`
	Hostname     string `json:"hostname"`
	Tipo         string `json:"tipo"`
	Estado       string `json:"estado"`
	Etapa        string `json:"etapa,omitempty"`
	Porcentaje   int    `json:"porcentaje"`
	IP           string `json:"ip,omitempty"`
	URL          string `json:"url"`
	ConContenido bool   `json:"con_contenido"`
	Error        string `json:"error,omitempty"`
	Componente   string `json:"componente,omitempty"`
}

// apiPowerAction es la respuesta a una acción de energía aceptada.
type apiPowerAction struct {
	VM     string `json:"vm"`
	Accion string `json:"accion"`
	Estado string `json:"estado"`
}

// registerAPIRoutes registra las rutas de la API REST.
func (h *Handler) registerAPIRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/instances", h.apiListInstances)
	mux.HandleFunc("GET /api/v1/instances/{hostname}", h.apiGetInstance)
	mux.HandleFunc("DELETE /api/v1/instances/{hostname}", h.apiDeleteInstance)
	mux.HandleFunc("POST /api/v1/instances/{hostname}/deployments", h.apiPublish)
	mux.HandleFunc("POST /api/v1/instances/{hostname}/power/{action}", h.apiPower)
	mux.HandleFunc("POST /api/v1/deployments", h.apiCreateDeployment)
	mux.HandleFunc("GET /api/v1/deployments/{id}", h.apiGetDeployment)
	mux.HandleFunc("POST /api/v1/deployments/{id}/retry", h.apiRetryDeployment)
	mux.HandleFunc("GET /api/v1/openapi.yaml", h.apiSpec)
	mux.HandleFunc("/api/v1/", h.apiNotFound)
}

// apiListInstances devuelve todas las instancias con su estado actual.
func (h *Handler) apiListInstances(w http.ResponseWriter, r *http.Request) {
	_, views, _ := h.currentState()
	list := make([]apiInstance, 0, len(views))
	for _, v := range views {
		list = append(list, newAPIInstance(v))
	}
	writeAPIJSON(w, http.StatusOK, list)
}

// apiGetInstance devuelve una instancia.
func (h *Handler) apiGetInstance(w http.ResponseWriter, r *http.Request) {
	hostname := instance.NormalizeHostname(r.PathValue("hostname"))
	_, views, _ := h.currentState()
	for _, v := range views {
		if v.Hostname == hostname {
			writeAPIJSON(w, http.StatusOK, newAPIInstance(v))
			return
		}
	}
	writeAPIError(w, instance.ErrNotFound)
}

// apiCreateDeployment crea una instancia y, si se envía content, publica el .zip.
// Responde 202 con la cabecera Location, donde el cliente consulta el avance.
func (h *Handler) apiCreateDeployment(w http.ResponseWriter, r *http.Request) {
	if err := parseRequestForm(w, r); err != nil {
		writeAPIError(w, err)
		return
	}

	hostname := instance.NormalizeHostname(r.FormValue("hostname"))
	if err := instance.ValidateHostname(hostname); err != nil {
		writeAPIError(w, err)
		return
	}
	if _, err := h.store.Get(hostname); err == nil {
		writeAPIError(w, instance.ErrHostnameTaken)
		return
	}

	var archive io.Reader
	archiveName := ""
	file, header, err := r.FormFile("content")
	switch {
	case err == nil:
		defer file.Close()
		if err := validateUpload(file, header); err != nil {
			writeAPIError(w, err)
			return
		}
		archive = file
		archiveName = header.Filename
	case !errors.Is(err, http.ErrMissingFile):
		slog.Error("no se pudo leer el archivo de la petición", "componente", "api", "error", err)
		writeAPIError(w, errInvalidForm)
		return
	}

	id, err := h.deployments.Start(hostname, archive, archiveName)
	if err != nil {
		slog.Error("no se pudo iniciar el despliegue", "componente", "api", "hostname", hostname, "error", err)
		writeAPIError(w, errStartFailed)
		return
	}
	slog.Info("solicitud de despliegue", "componente", "api", "despliegue_id", id, "hostname", hostname)
	h.writeAccepted(w, id)
}

// apiPublish publica un .zip en una instancia existente.
func (h *Handler) apiPublish(w http.ResponseWriter, r *http.Request) {
	hostname := instance.NormalizeHostname(r.PathValue("hostname"))

	inst, err := h.store.Get(hostname)
	if err != nil {
		writeAPIError(w, err)
		return
	}
	if err := parseRequestForm(w, r); err != nil {
		writeAPIError(w, err)
		return
	}

	file, header, err := r.FormFile("content")
	if errors.Is(err, http.ErrMissingFile) {
		writeAPIError(w, deploy.ErrArchiveMissing)
		return
	}
	if err != nil {
		slog.Error("no se pudo leer el archivo de la petición", "componente", "api", "error", err)
		writeAPIError(w, errInvalidForm)
		return
	}
	defer file.Close()

	if err := validateUpload(file, header); err != nil {
		writeAPIError(w, err)
		return
	}

	id, err := h.deployments.Publish(hostname, inst.IP, file, header.Filename)
	if err != nil {
		if !errors.Is(err, deployment.ErrBusy) {
			slog.Error("no se pudo iniciar la publicación", "componente", "api", "hostname", hostname, "error", err)
			err = errStartFailed
		}
		writeAPIError(w, err)
		return
	}
	slog.Info("solicitud de publicación", "componente", "api", "despliegue_id", id, "hostname", hostname)
	h.writeAccepted(w, id)
}

// apiGetDeployment devuelve el estado de un despliegue.
func (h *Handler) apiGetDeployment(w http.ResponseWriter, r *http.Request) {
	snap, err := h.deployments.Snapshot(r.PathValue("id"))
	if err != nil {
		writeAPIError(w, err)
		return
	}
	writeAPIJSON(w, http.StatusOK, newAPIDeployment(snap))
}

// apiRetryDeployment reintenta un despliegue fallido.
func (h *Handler) apiRetryDeployment(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	newID, err := h.deployments.Retry(id)
	if err != nil {
		writeAPIError(w, err)
		return
	}
	slog.Info("solicitud de reintento", "componente", "api", "despliegue_anterior", id, "despliegue_id", newID)
	h.writeAccepted(w, newID)
}

// apiDeleteInstance elimina una instancia: registro DNS, VM, disco e IP. Responde 204 al terminar.
func (h *Handler) apiDeleteInstance(w http.ResponseWriter, r *http.Request) {
	hostname := instance.NormalizeHostname(r.PathValue("hostname"))

	if _, err := h.store.Get(hostname); err != nil {
		writeAPIError(w, err)
		return
	}
	if h.deployments.ActiveHostnames()[hostname] {
		writeAPIError(w, deployment.ErrBusy)
		return
	}
	if p, ok := h.power.Pending()[hostname]; ok && p.Action == power.ActionStop {
		writeAPIError(w, power.ErrInProgress)
		return
	}

	ctx, cancel := backgroundContext(r, deleteTimeout)
	defer cancel()

	if err := h.provisioner.Delete(ctx, hostname); err != nil {
		writeAPIError(w, infrastructureError(err, errDeleteFailed, "eliminar "+hostname))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// apiPower enciende, apaga o reinicia la VM de una instancia. Responde 202: el encendido
// y el reinicio se envían de inmediato, y el apagado sigue en segundo plano.
func (h *Handler) apiPower(w http.ResponseWriter, r *http.Request) {
	hostname := instance.NormalizeHostname(r.PathValue("hostname"))

	if _, err := h.store.Get(hostname); err != nil {
		writeAPIError(w, err)
		return
	}
	action, err := power.ParseAction(r.PathValue("action"))
	if err != nil {
		writeAPIError(w, err)
		return
	}
	if h.deployments.ActiveHostnames()[hostname] {
		writeAPIError(w, deployment.ErrBusy)
		return
	}

	slog.Info("solicitud de energía", "componente", "api", "vm", hostname, "accion", action.Label())

	ctx, cancel := backgroundContext(r, powerTimeout)
	defer cancel()

	if err := h.power.Do(ctx, hostname, action); err != nil {
		writeAPIError(w, infrastructureError(err, errPowerFailed, string(action)+" "+hostname))
		return
	}
	writeAPIJSON(w, http.StatusAccepted, apiPowerAction{VM: hostname, Accion: string(action), Estado: "aceptada"})
}

// apiSpec publica la especificación OpenAPI de la API.
func (h *Handler) apiSpec(w http.ResponseWriter, r *http.Request) {
	if h.cfg.OpenAPIFile == "" {
		h.apiNotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	http.ServeFile(w, r, h.cfg.OpenAPIFile)
}

// apiNotFound responde en el formato de error de la API a las rutas que no existen.
func (h *Handler) apiNotFound(w http.ResponseWriter, r *http.Request) {
	writeAPIJSON(w, http.StatusNotFound, apiErrorBody{Error: apiError{
		Codigo:  "ruta_no_encontrada",
		Mensaje: "la ruta " + r.Method + " " + r.URL.Path + " no existe",
	}})
}

// writeAccepted responde 202 con el despliegue recién creado y la cabecera Location para consultarlo.
func (h *Handler) writeAccepted(w http.ResponseWriter, id string) {
	w.Header().Set("Location", apiPrefix+"deployments/"+url.PathEscape(id))
	snap, err := h.deployments.Snapshot(id)
	if err != nil {
		writeAPIJSON(w, http.StatusAccepted, apiDeployment{ID: id, Estado: "en_curso"})
		return
	}
	writeAPIJSON(w, http.StatusAccepted, newAPIDeployment(snap))
}

// writeAPIError responde con el código HTTP y el código de error que corresponden a err.
// Los errores inesperados se registran y el cliente recibe un mensaje genérico.
func writeAPIError(w http.ResponseWriter, err error) {
	status, known := statusFor(err)
	if errors.Is(err, deployment.ErrNotFound) {
		status, known = http.StatusNotFound, true
	}
	if errors.Is(err, deployment.ErrNotRetryable) || errors.Is(err, deployment.ErrAlreadyRetried) {
		status, known = http.StatusConflict, true
	}

	body := apiError{Codigo: "error_interno", Mensaje: "ocurrió un error interno; revise el log del servidor"}
	if known {
		body.Mensaje = err.Error()
		for _, c := range apiErrorCodes {
			if errors.Is(err, c.err) {
				body.Codigo = c.code
				break
			}
		}
	} else {
		slog.Error("error inesperado", "componente", "api", "error", err)
	}
	writeAPIJSON(w, status, apiErrorBody{Error: body})
}

func writeAPIJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	writeJSON(w, value)
}

func newAPIInstance(v instanceView) apiInstance {
	return apiInstance{
		Hostname: v.Hostname,
		IP:       v.IP.String(),
		FQDN:     v.FQDN(),
		URL:      v.URL(),
		Estado:   stateLabel(v.Status),
		Creada:   v.CreatedAt,
	}
}

func newAPIDeployment(s deployment.Snapshot) apiDeployment {
	d := apiDeployment{
		ID:           s.ID,
		Hostname:     s.Hostname,
		Tipo:         deployment.KindCreate,
		Porcentaje:   s.Percent,
		IP:           s.IP,
		URL:          s.URL,
		ConContenido: s.HasContent,
		Error:        s.Error,
		Componente:   s.Component,
	}
	if s.Republish {
		d.Tipo = deployment.KindPublish
	}

	switch s.Status {
	case deployment.StatusRunning:
		d.Estado = "en_curso"
		d.Etapa = stepKey(s.Step)
	case deployment.StatusSucceeded:
		d.Estado = "exito"
	case deployment.StatusFailed:
		d.Estado = "error"
		d.Etapa = stepKey(s.FailedStep)
	}
	return d
}

// stepKey devuelve el identificador de la etapa i, o "" si no existe.
func stepKey(i int) string {
	if i < 0 || i >= len(deployment.Steps) {
		return ""
	}
	return deployment.Steps[i].Key
}

// stateLabel traduce el estado interno de una instancia a la etiqueta de la API y las métricas.
func stateLabel(status string) string {
	for _, s := range instanceStateLabels {
		if s.status == status {
			return s.label
		}
	}
	return status
}
