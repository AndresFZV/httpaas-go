package web

import (
	"context"
	"errors"
	"io"
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

	deleteTimeout = 2 * time.Minute
)

var (
	errInvalidForm  = errors.New("no se pudo leer el formulario enviado")
	errStartFailed  = errors.New("no se pudo iniciar el despliegue; revise el log del servidor")
	errDeleteFailed = errors.New("no se pudo eliminar la instancia; revise el log del servidor")
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

// deploy valida el formulario y arranca el despliegue en segundo plano.
// Redirige de inmediato a la página de progreso.
func (h *Handler) deploy(w http.ResponseWriter, r *http.Request) {
	if err := parseRequestForm(w, r); err != nil {
		h.renderDeployError(w, r, "", err)
		return
	}

	hostname := instance.NormalizeHostname(r.FormValue("hostname"))
	if err := instance.ValidateHostname(hostname); err != nil {
		h.renderDeployError(w, r, hostname, err)
		return
	}
	if _, err := h.store.Get(hostname); err == nil {
		h.renderDeployError(w, r, hostname, instance.ErrHostnameTaken)
		return
	}

	// El .zip es opcional: sin archivo solo se crea la instancia.
	var archive io.Reader
	archiveName := ""

	file, header, err := r.FormFile("content")
	switch {
	case err == nil:
		defer file.Close()
		if err := deploy.ValidateArchive(file, header.Size, header.Filename); err != nil {
			h.renderDeployError(w, r, hostname, err)
			return
		}
		// La validación leyó el archivo; se vuelve al inicio antes de guardarlo.
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			log.Printf("reiniciar lectura del archivo: %v", err)
			h.renderDeployError(w, r, hostname, errInvalidForm)
			return
		}
		archive = file
		archiveName = header.Filename
	case !errors.Is(err, http.ErrMissingFile):
		log.Printf("leer archivo del formulario: %v", err)
		h.renderDeployError(w, r, hostname, errInvalidForm)
		return
	}

	id, err := h.deployments.Start(hostname, archive, archiveName)
	if err != nil {
		log.Printf("iniciar despliegue de %s: %v", hostname, err)
		h.renderDeployError(w, r, hostname, errStartFailed)
		return
	}
	log.Printf("despliegue %s iniciado para %s", id, hostname)

	redirectTo(w, r, "/deployments/"+url.PathEscape(id))
}

// deleteInstance borra la VM, su registro DNS y la instancia del registro.
func (h *Handler) deleteInstance(w http.ResponseWriter, r *http.Request) {
	hostname := instance.NormalizeHostname(r.PathValue("hostname"))

	ctx, cancel := backgroundContext(r, deleteTimeout)
	defer cancel()

	if err := h.provisioner.Delete(ctx, hostname); err != nil {
		err = infrastructureError(err, errDeleteFailed, "eliminar "+hostname)
		status, _ := statusFor(err)
		data := h.newPage(r, "instances", "Instancias")
		data.Error = err.Error()
		h.render(w, status, data)
		return
	}
	log.Printf("instancia eliminada: %s", hostname)

	redirectTo(w, r, "/instances?deleted="+url.QueryEscape(hostname))
}

// renderDeployError vuelve a mostrar el formulario con el error y el nombre que había escrito el usuario.
// Los errores inesperados se registran en el log y el usuario recibe un mensaje genérico.
func (h *Handler) renderDeployError(w http.ResponseWriter, r *http.Request, hostname string, err error) {
	status, known := statusFor(err)
	message := err.Error()
	if !known {
		log.Printf("error inesperado: %v", err)
		message = "Ocurrió un error interno; revise el log del servidor"
	}

	data := h.newPage(r, "deploy", "Nuevo despliegue")
	data.Hostname = hostname
	data.FormError = message
	h.render(w, status, data)
}

// backgroundContext devuelve un contexto que no se cancela si el cliente cierra la conexión,
// para que una operación sobre la infraestructura no quede a medias.
func backgroundContext(r *http.Request, timeout time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(r.Context()), timeout)
}

// infrastructureError deja pasar los errores conocidos y reemplaza los demás por public,
// registrando el detalle técnico en el log.
func infrastructureError(err, public error, action string) error {
	if _, known := statusFor(err); known {
		return err
	}
	log.Printf("%s: %v", action, err)
	return public
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
	case errors.Is(err, instance.ErrNotFound):
		return http.StatusNotFound, true
	case errors.Is(err, instance.ErrNoFreeIP):
		return http.StatusServiceUnavailable, true
	case errors.Is(err, errStartFailed), errors.Is(err, errDeleteFailed):
		return http.StatusInternalServerError, true
	}

	for _, target := range validationErrors {
		if errors.Is(err, target) {
			return http.StatusUnprocessableEntity, true
		}
	}
	return http.StatusInternalServerError, false
}

// redirectTo redirige con 303, que obliga al navegador a hacer un GET después de un POST.
func redirectTo(w http.ResponseWriter, r *http.Request, target string) {
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
