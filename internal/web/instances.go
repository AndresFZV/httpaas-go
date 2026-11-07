package web

import (
	"errors"
	"fmt"
	"log"
	"net/http"

	"github.com/AndresFZV/httpaas-go/internal/deploy"
	"github.com/AndresFZV/httpaas-go/internal/instance"
)

const (
	// maxRequestSize deja 1 MB de margen sobre el .zip para el resto del formulario.
	maxRequestSize = deploy.MaxArchiveSize + 1<<20
	// multipartMemory es lo que se guarda en memoria; el resto del archivo va a disco temporal.
	multipartMemory = 10 << 20
)

var errInvalidForm = errors.New("no se pudo leer el formulario enviado")

// createInstance atiende el botón Aceptar: valida el nombre del host solicitado.
func (h *Handler) createInstance(w http.ResponseWriter, r *http.Request) {
	if err := parseRequestForm(w, r); err != nil {
		h.renderIndex(w, statusFor(err), indexData{Error: err.Error()})
		return
	}

	hostname := instance.NormalizeHostname(r.FormValue("hostname"))
	if err := instance.ValidateHostname(hostname); err != nil {
		h.renderIndex(w, http.StatusUnprocessableEntity, indexData{Hostname: hostname, Error: err.Error()})
		return
	}

	// Tarjeta 12: aquí se aprovisiona la máquina virtual.
	h.renderIndex(w, http.StatusOK, indexData{
		Hostname: hostname,
		Success:  fmt.Sprintf("Solicitud válida para %s.cloud.local", hostname),
	})
}

// deployInstance atiende el botón Publicar: valida el host y el .zip con el contenido web.
func (h *Handler) deployInstance(w http.ResponseWriter, r *http.Request) {
	if err := parseRequestForm(w, r); err != nil {
		h.renderIndex(w, statusFor(err), indexData{Error: err.Error()})
		return
	}

	hostname := instance.NormalizeHostname(r.FormValue("hostname"))
	if err := instance.ValidateHostname(hostname); err != nil {
		h.renderIndex(w, http.StatusUnprocessableEntity, indexData{Hostname: hostname, Error: err.Error()})
		return
	}

	file, header, err := r.FormFile("content")
	if errors.Is(err, http.ErrMissingFile) {
		h.renderIndex(w, http.StatusUnprocessableEntity, indexData{Hostname: hostname, Error: deploy.ErrArchiveMissing.Error()})
		return
	}
	if err != nil {
		log.Printf("leer archivo del formulario: %v", err)
		h.renderIndex(w, http.StatusBadRequest, indexData{Hostname: hostname, Error: errInvalidForm.Error()})
		return
	}
	defer file.Close()

	if err := deploy.ValidateArchive(file, header.Size, header.Filename); err != nil {
		h.renderIndex(w, statusFor(err), indexData{Hostname: hostname, Error: err.Error()})
		return
	}

	// Tarjeta 14: aquí se envía el .zip a la instancia y se descomprime.
	h.renderIndex(w, http.StatusOK, indexData{
		Hostname: hostname,
		Success:  fmt.Sprintf("Archivo %s válido para %s.cloud.local", header.Filename, hostname),
	})
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

// statusFor traduce un error de validación al código HTTP correspondiente.
func statusFor(err error) int {
	switch {
	case errors.Is(err, deploy.ErrArchiveTooLarge):
		return http.StatusRequestEntityTooLarge
	case errors.Is(err, errInvalidForm):
		return http.StatusBadRequest
	default:
		return http.StatusUnprocessableEntity
	}
}
