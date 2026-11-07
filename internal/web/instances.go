package web

import (
	"context"
	"errors"
	"fmt"
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

	provisionTimeout = 5 * time.Minute
	deleteTimeout    = 2 * time.Minute
	deployTimeout    = 2 * time.Minute
)

var (
	errInvalidForm      = errors.New("no se pudo leer el formulario enviado")
	errInstanceRequired = errors.New("la instancia no existe; primero créela con el botón Aceptar")
	errProvisionFailed  = errors.New("no se pudo crear la instancia; revise el log del servidor")
	errDeleteFailed     = errors.New("no se pudo eliminar la instancia; revise el log del servidor")
	errDeployFailed     = errors.New("no se pudo publicar el contenido; revise el log del servidor")
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

// createInstance atiende el botón Aceptar: crea la VM, le asigna hostname e IP y la registra en el DNS.
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

	ctx, cancel := backgroundContext(r, provisionTimeout)
	defer cancel()

	log.Printf("aprovisionando %s...", hostname)
	inst, err := h.provisioner.Provision(ctx, hostname)
	if err != nil {
		h.renderError(w, hostname, infrastructureError(err, errProvisionFailed, "aprovisionar "+hostname))
		return
	}
	log.Printf("instancia creada: %s (%s)", inst.FQDN(), inst.IP)

	redirectHome(w, r, hostname)
}

// deployInstance atiende el botón Publicar: valida el .zip y lo despliega en la instancia.
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
	// La validación lee el archivo; se vuelve al inicio antes de enviarlo.
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		h.renderError(w, hostname, fmt.Errorf("reiniciar lectura del archivo: %w", err))
		return
	}

	ctx, cancel := backgroundContext(r, deployTimeout)
	defer cancel()

	if err := h.deployer.Deploy(ctx, inst.IP.String(), file); err != nil {
		h.renderError(w, hostname, infrastructureError(err, errDeployFailed, "publicar en "+hostname))
		return
	}
	log.Printf("contenido %s publicado en %s", header.Filename, inst.FQDN())

	h.renderIndex(w, http.StatusOK, indexData{
		Hostname: hostname,
		Success:  fmt.Sprintf("Contenido publicado en %s", inst.URL()),
	})
}

// deleteInstance atiende el botón Eliminar: borra la VM, su registro DNS y la instancia del registro.
func (h *Handler) deleteInstance(w http.ResponseWriter, r *http.Request) {
	hostname := instance.NormalizeHostname(r.PathValue("hostname"))

	ctx, cancel := backgroundContext(r, deleteTimeout)
	defer cancel()

	if err := h.provisioner.Delete(ctx, hostname); err != nil {
		h.renderError(w, "", infrastructureError(err, errDeleteFailed, "eliminar "+hostname))
		return
	}
	log.Printf("instancia eliminada: %s", hostname)

	redirectHome(w, r, "")
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
	case errors.Is(err, errProvisionFailed), errors.Is(err, errDeleteFailed), errors.Is(err, errDeployFailed):
		return http.StatusInternalServerError, true
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
