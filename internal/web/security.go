package web

import (
	"crypto/sha256"
	"crypto/subtle"
	"log/slog"
	"net/http"
)

// contentSecurityPolicy solo permite recursos del propio servidor, más las fuentes de Google Fonts.
// Las plantillas no usan scripts ni estilos en línea, así que no hace falta 'unsafe-inline'.
const contentSecurityPolicy = "default-src 'self'; " +
	"style-src 'self' https://fonts.googleapis.com; " +
	"font-src https://fonts.gstatic.com; " +
	"img-src 'self' data:; " +
	"object-src 'none'; " +
	"base-uri 'self'; " +
	"form-action 'self'; " +
	"frame-ancestors 'none'"

// SecurityConfig son las opciones de seguridad de la aplicación.
type SecurityConfig struct {
	// AuthUser y AuthPassword activan la autenticación básica. Si alguno está vacío, no se pide.
	AuthUser     string
	AuthPassword string
}

// AuthEnabled indica si la aplicación pide usuario y contraseña.
func (c SecurityConfig) AuthEnabled() bool {
	return c.AuthUser != "" && c.AuthPassword != ""
}

// Secure envuelve next con las protecciones de la aplicación, de afuera hacia adentro:
// cabeceras de seguridad, autenticación básica (si está activa) y protección CSRF.
// Las cabeceras van primero para que también acompañen las respuestas 401 y 403.
func Secure(next http.Handler, cfg SecurityConfig) http.Handler {
	csrf := http.NewCrossOriginProtection()
	csrf.SetDenyHandler(http.HandlerFunc(denyCrossOrigin))

	handler := csrf.Handler(next)
	if cfg.AuthEnabled() {
		handler = basicAuth(handler, cfg.AuthUser, cfg.AuthPassword)
	}
	return securityHeaders(handler)
}

// denyCrossOrigin responde a las peticiones que la protección CSRF rechaza y las registra.
func denyCrossOrigin(w http.ResponseWriter, r *http.Request) {
	slog.Warn("petición rechazada", "componente", "seguridad", "motivo", "otro_origen",
		"metodo", r.Method, "ruta", r.URL.Path,
		"origen", r.Header.Get("Origin"), "sec_fetch_site", r.Header.Get("Sec-Fetch-Site"))
	http.Error(w, "Petición rechazada: proviene de otro sitio.", http.StatusForbidden)
}

// securityHeaders agrega a todas las respuestas las cabeceras que endurecen el navegador.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", contentSecurityPolicy)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

// basicAuth pide usuario y contraseña con autenticación HTTP básica.
// Compara los resúmenes SHA-256 en tiempo constante, para no revelar por el tiempo de respuesta
// cuántos caracteres coinciden ni la longitud de los valores correctos.
func basicAuth(next http.Handler, user, password string) http.Handler {
	wantUser := sha256.Sum256([]byte(user))
	wantPassword := sha256.Sum256([]byte(password))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser, gotPassword, ok := r.BasicAuth()
		if ok {
			userHash := sha256.Sum256([]byte(gotUser))
			passwordHash := sha256.Sum256([]byte(gotPassword))
			userOK := subtle.ConstantTimeCompare(userHash[:], wantUser[:]) == 1
			passwordOK := subtle.ConstantTimeCompare(passwordHash[:], wantPassword[:]) == 1
			if userOK && passwordOK {
				next.ServeHTTP(w, r)
				return
			}
			slog.Warn("petición rechazada", "componente", "seguridad", "motivo", "credenciales_incorrectas",
				"metodo", r.Method, "ruta", r.URL.Path, "usuario", gotUser)
		}

		w.Header().Set("WWW-Authenticate", `Basic realm="HTTPaaS", charset="UTF-8"`)
		http.Error(w, "Se requiere usuario y contraseña.", http.StatusUnauthorized)
	})
}
