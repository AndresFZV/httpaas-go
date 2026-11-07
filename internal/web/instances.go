package web

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"time"

	"github.com/AndresFZV/httpaas-go/internal/deploy"
	"github.com/AndresFZV/httpaas-go/internal/instance"
)

const (
	// maxRequestSize deja 1 MB de margen sobre el .zip para el resto del formulario.
	maxRequestSize = deploy.MaxArchiveSize + 1<<20
	// multipartMemory es lo que se guarda en memoria; el resto del archivo va a disco temporal.
	multipartMemory = 10 << 20
)

var (
	errInvalidForm      = errors.New("no se pudo leer el formulario enviado")
	errInstanceRequired = errors.New("la instancia no existe; primero créela con el botón Aceptar")
)

// validationErrors son los errores de datos inválidos enviados por el usuario.
var validationErrors = []error{
	instance.ErrHostnameEmpty,
	instance.ErrHostnameInvalid,
	instance.ErrHostnameReserved,
	deploy.ErrArchiveMissing,
	deploy.ErrArchiveExtension,
	deploy.ErrArchiveCorrupt,
	deploy.ErrArchiveUnsafePath,
	deploy.ErrArchiveNoIndex,
}

// createInstance atiende el botón Aceptar: valida el nombre y reserva el host y su IP.
func (h *Handler) createInstance(w http.ResponseWriter, r *http.Request) {
	if err := parseRequestForm(w, r); err != nil {
		h.renderError(w, "", err)
		return
	}

	hostname := instance.NormalizeHostname(r.FormValue("hostname"))
	if err := instance.ValidateHostname(hostname); err != nil {
		h.renderError(w, hostname, err)
		return
	}

	inst, err := h.store.Reserve(hostname, time.Now())
	if err != nil {
		h.renderError(w, hostname, err)
		return
	}
	log.Printf("instancia reservada: %s (%s)", inst.FQDN(), inst.IP)

	// Tarjetas 12 y 13: aquí se crea la VM y se registra en el DNS.
	redirectHome(w, r, hostname)
}

// deployInstance atiende el botón Publicar: valida el .zip para una instancia existente.
func (h *Handler) deployInstance(w http.ResponseWriter, r *http.Request) {
	if err := parseRequestForm(w, r); err != nil {
		h.renderError(w, "", err)
		return
	}

	hostname := instance.NormalizeHostname(r.FormValue("hostname"))
	if err := instance.ValidateHostname(hostname); err != nil {
		h.renderError(w, hostname, err)
		return
	}

	inst, err := h.store.Get(hostname)
	if errors.Is(err, instance.ErrNotFound) {
		h.renderError(w, hostname, errInstanceRequired)
		return
	}
	if err != nil {
		h.renderError(w, hostname, err)
		return
	}

	file, header, err := r.FormFile("content")
	if errors.Is(err, http.ErrMissingFile) {
		h.renderError(w, hostname, deploy.ErrArchiveMissing)
		return
	}
	if err != nil {
		log.Printf("leer archivo del formulario: %v", err)
		h.renderError(w, hostname, errInvalidForm)
		return
	}
	defer file.Close()

	if err := deploy.ValidateArchive(file, header.Size, header.Filename); err != nil {
		h.renderError(w, hostname, err)
		return
	}

	// Tarjeta 14: aquí se envía el .zip a la instancia y se descomprime.
	h.renderIndex(w, http.StatusOK, indexData{
		Hostname: hostname,
		Success:  fmt.Sprintf("Archivo %s válido para %s", header.Filename, inst.FQDN()),
	})
}

// deleteInstance atiende el botón Eliminar del dashboard.
func (h *Handler) deleteInstance(w http.ResponseWriter, r *http.Request) {
	hostname := instance.NormalizeHostname(r.PathValue("hostname"))

	// Tarjeta 16: aquí se apaga y borra la VM y se quita el registro DNS.
	if err := h.store.Remove(hostname); err != nil {
		h.renderError(w, "", err)
		return
	}
	log.Printf("instancia eliminada: %s", hostname)

	redirectHome(w, r, "")
}

// renderError muestra el error en la página principal con el código HTTP correspondiente.
// Los errores inesperados se registran en el log y el usuario recibe un mensaje genérico.
func (h *Handler) renderError(w http.ResponseWriter, hostname string, err error) {
	status, known := statusFor(err)
	message := err.Error()
	if !known {
		log.Printf("error inesperado: %v", err)
		message = "Ocurrió un error interno; revise el log del servidor"
	}
	h.renderIndex(w, status, indexData{Hostname: hostname, Error: message})
}

// statusFor traduce un error al código HTTP; known indica si el error es esperado.
func statusFor(err error) (status int, known bool) {
	switch {
	case errors.Is(err, deploy.ErrArchiveTooLarge):
		return http.StatusRequestEntityTooLarge, true
	case errors.Is(err, errInvalidForm):
		return http.StatusBadRequest, true
	case errors.Is(err, instance.ErrHostnameTaken):
		return http.StatusConflict, true
	case errors.Is(err, instance.ErrNotFound), errors.Is(err, errInstanceRequired):
		return http.StatusNotFound, true
	case errors.Is(err, instance.ErrNoFreeIP):
		return http.StatusServiceUnavailable, true
	}

	for _, target := range validationErrors {
		if errors.Is(err, target) {
			return http.StatusUnprocessableEntity, true
		}
	}
	return http.StatusInternalServerError, false
}

// redirectHome redirige a la página principal; si hostname no está vacío, lo deja en el campo.
func redirectHome(w http.ResponseWriter, r *http.Request, hostname string) {
	target := "/"
	if hostname != "" {
		target += "?host=" + url.QueryEscape(hostname)
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// parseRequestForm limita el tamaño de la petición y procesa el formulario multipart.
func parseRequestForm(w http.ResponseWriter, r *http.Request) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestSize)
	if err := r.ParseMultipartForm(multipartMemory); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return deploy.ErrArchiveTooLarge
		}
		log.Printf("procesar formulario: %v", err)
		return errInvalidForm
	}
	return nil
}
