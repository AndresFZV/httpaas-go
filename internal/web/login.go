package web

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/AndresFZV/httpaas-go/internal/auth"
)

// maxLoginForm limita el tamaño del formulario de inicio de sesión.
const maxLoginForm = 4 << 10

// loginData son los datos de la página de inicio de sesión.
type loginData struct {
	Domain     string
	User       string // usuario escrito, para no tener que repetirlo tras un error
	Next       string // página a la que se vuelve después de iniciar sesión
	Error      string
	Notice     string
	SessionTTL string
}

// loginPage muestra el formulario de inicio de sesión.
func (h *Handler) loginPage(w http.ResponseWriter, r *http.Request) {
	next := safeNext(r.URL.Query().Get("next"))
	if h.auth == nil || h.currentUser(r) != "" {
		redirectTo(w, r, next)
		return
	}

	data := h.newLoginData(next)
	if r.URL.Query().Get("salida") == "1" {
		data.Notice = "Cerraste sesión correctamente."
	}
	h.renderLogin(w, http.StatusOK, data)
}

// login valida las credenciales, crea la sesión y vuelve a la página que se intentaba abrir.
func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	if h.auth == nil {
		redirectTo(w, r, "/")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxLoginForm)
	if err := r.ParseForm(); err != nil {
		data := h.newLoginData("/")
		data.Error = "No se pudo leer el formulario. Inténtalo de nuevo."
		h.renderLogin(w, http.StatusBadRequest, data)
		return
	}

	user := strings.TrimSpace(r.PostFormValue("usuario"))
	password := r.PostFormValue("contrasena")
	next := safeNext(r.PostFormValue("next"))
	ip := clientIP(r)

	session, retryAfter, err := h.auth.Login(user, password, ip)
	if err != nil {
		data := h.newLoginData(next)
		data.User = user
		status := http.StatusUnauthorized

		if errors.Is(err, auth.ErrTooManyAttempts) {
			reject(h.metrics, r, reasonTooManyLogins, "usuario", user, "ip", ip)
			seconds := int(math.Ceil(retryAfter.Seconds()))
			w.Header().Set("Retry-After", fmt.Sprint(seconds))
			data.Error = fmt.Sprintf("Demasiados intentos fallidos. Espera %d segundos e inténtalo de nuevo.", seconds)
			status = http.StatusTooManyRequests
		} else {
			reject(h.metrics, r, reasonBadCredentials, "usuario", user, "ip", ip)
			data.Error = "Usuario o contraseña incorrectos."
		}
		h.renderLogin(w, status, data)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    session.ID,
		Path:     "/",
		Expires:  session.ExpiresAt,
		MaxAge:   int(h.auth.SessionTTL().Seconds()),
		HttpOnly: true,                 // JavaScript no puede leerla
		SameSite: http.SameSiteLaxMode, // otro sitio no puede usarla en un POST
	})
	slog.Info("sesión iniciada", "componente", "seguridad", "usuario", session.User, "ip", ip)
	redirectTo(w, r, next)
}

// logout cierra la sesión y borra la cookie.
func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	if h.auth != nil {
		if cookie, err := r.Cookie(sessionCookie); err == nil {
			if s, ok := h.auth.Session(cookie.Value); ok {
				slog.Info("sesión cerrada", "componente", "seguridad", "usuario", s.User, "ip", clientIP(r))
			}
			h.auth.Logout(cookie.Value)
		}
	}

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	redirectTo(w, r, loginPath+"?salida=1")
}

// currentUser devuelve el usuario de la sesión de la petición, o "" si no hay sesión
// o si la interfaz no pide inicio de sesión.
func (h *Handler) currentUser(r *http.Request) string {
	if h.auth == nil {
		return ""
	}
	cookie, err := r.Cookie(sessionCookie)
	if err != nil {
		return ""
	}
	s, ok := h.auth.Session(cookie.Value)
	if !ok {
		return ""
	}
	return s.User
}

func (h *Handler) newLoginData(next string) loginData {
	data := loginData{Domain: h.cfg.Domain, Next: next}
	if h.auth != nil {
		data.SessionTTL = sessionLifetime(h.auth.SessionTTL())
	}
	return data
}

// renderLogin ejecuta la página de inicio de sesión, que no usa el layout de la aplicación.
func (h *Handler) renderLogin(w http.ResponseWriter, status int, data loginData) {
	var buf bytes.Buffer
	if err := h.loginTmpl.ExecuteTemplate(&buf, "login", data); err != nil {
		slog.Error("no se pudo renderizar la página", "componente", "web", "pagina", "login", "error", err)
		http.Error(w, "Error interno del servidor", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = buf.WriteTo(w)
}

// safeNext acepta solo rutas internas de la aplicación, para que un enlace con ?next=
// no pueda llevar al usuario a otro sitio después de iniciar sesión.
func safeNext(next string) string {
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.HasPrefix(next, `/\`) {
		return "/"
	}
	u, err := url.Parse(next)
	if err != nil || u.Scheme != "" || u.Host != "" || u.Path == loginPath || u.Path == logoutPath {
		return "/"
	}
	return next
}

// clientIP devuelve la IP del cliente, para limitar los intentos fallidos por equipo.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// sessionLifetime describe la duración de una sesión para la interfaz, por ejemplo "8 horas".
func sessionLifetime(d time.Duration) string {
	return fmt.Sprintf("%d horas", int(d.Hours()))
}
