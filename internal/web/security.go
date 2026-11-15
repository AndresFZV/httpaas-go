package web

import (
	"crypto/sha256"
	"crypto/subtle"
	"log/slog"
	"net/http"
	"slices"
	"strings"

	"github.com/AndresFZV/httpaas-go/internal/metrics"
)

// metricRejections cuenta las peticiones rechazadas por las protecciones de seguridad.
const metricRejections = "httpaas_security_rejections_total"

// Motivos de rechazo, para el log y las métricas.
const (
	reasonCrossOrigin    = "otro_origen"
	reasonNoCredentials  = "sin_credenciales"
	reasonBadCredentials = "credenciales_incorrectas"
	reasonNoToken        = "sin_token"
	reasonBadToken       = "token_incorrecto"
)

// apiSpecPath es la especificación OpenAPI, que se publica sin token para poder leer la documentación.
const apiSpecPath = apiPrefix + "openapi.yaml"

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

	// PublicPaths son rutas que no piden autenticación, como las de salud que consultan
	// los orquestadores y balanceadores de carga.
	PublicPaths []string

	// APIToken es el token que deben enviar los clientes de /api/v1/ en la cabecera
	// Authorization: Bearer <token>. Si está vacío, la API REST queda deshabilitada.
	APIToken string

	// Metrics recibe los rechazos; puede ser nil.
	Metrics *metrics.Registry
}

// AuthEnabled indica si la aplicación pide usuario y contraseña.
func (c SecurityConfig) AuthEnabled() bool {
	return c.AuthUser != "" && c.AuthPassword != ""
}

// Secure envuelve next con las protecciones de la aplicación, de afuera hacia adentro:
// cabeceras de seguridad, autenticación (token para /api/v1/ y básica para el resto, si está activa)
// y protección CSRF. Las cabeceras van primero para que también acompañen las respuestas 401 y 403.
func Secure(next http.Handler, cfg SecurityConfig) http.Handler {
	cfg.Metrics.Counter(metricRejections, "Peticiones rechazadas por las protecciones de seguridad, por motivo.")

	csrf := http.NewCrossOriginProtection()
	csrf.SetDenyHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reject(cfg.Metrics, r, reasonCrossOrigin,
			"origen", r.Header.Get("Origin"), "sec_fetch_site", r.Header.Get("Sec-Fetch-Site"))
		http.Error(w, "Petición rechazada: proviene de otro sitio.", http.StatusForbidden)
	}))

	handler := csrf.Handler(next)
	if cfg.AuthEnabled() {
		handler = basicAuth(handler, cfg)
	}
	handler = apiTokenAuth(handler, cfg)
	return securityHeaders(handler)
}

// apiTokenAuth exige el token de la API en las rutas /api/v1/; las demás pasan a la autenticación básica.
// La comparación se hace en tiempo constante, igual que la de la autenticación básica.
func apiTokenAuth(next http.Handler, cfg SecurityConfig) http.Handler {
	wantToken := sha256.Sum256([]byte(cfg.APIToken))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, apiPrefix) || r.URL.Path == apiSpecPath {
			next.ServeHTTP(w, r)
			return
		}
		if cfg.APIToken == "" {
			writeAPIJSON(w, http.StatusServiceUnavailable, apiErrorBody{Error: apiError{
				Codigo:  "api_deshabilitada",
				Mensaje: "la API REST está deshabilitada; defina HTTPAAS_API_TOKEN para activarla",
			}})
			return
		}

		got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		reason := reasonNoToken
		if ok && got != "" {
			gotHash := sha256.Sum256([]byte(got))
			if subtle.ConstantTimeCompare(gotHash[:], wantToken[:]) == 1 {
				next.ServeHTTP(w, r)
				return
			}
			reason = reasonBadToken
		}

		reject(cfg.Metrics, r, reason)
		w.Header().Set("WWW-Authenticate", `Bearer realm="HTTPaaS API"`)
		writeAPIJSON(w, http.StatusUnauthorized, apiErrorBody{Error: apiError{
			Codigo:  "no_autorizado",
			Mensaje: "envíe el token de la API en la cabecera Authorization: Bearer <token>",
		}})
	})
}

// reject registra en el log y en las métricas una petición rechazada.
func reject(reg *metrics.Registry, r *http.Request, reason string, attrs ...any) {
	reg.Inc(metricRejections, metrics.L("motivo", reason))
	args := append([]any{"componente", "seguridad", "motivo", reason, "metodo", r.Method, "ruta", r.URL.Path}, attrs...)
	slog.Warn("petición rechazada", args...)
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
// Las rutas de cfg.PublicPaths pasan sin credenciales.
func basicAuth(next http.Handler, cfg SecurityConfig) http.Handler {
	wantUser := sha256.Sum256([]byte(cfg.AuthUser))
	wantPassword := sha256.Sum256([]byte(cfg.AuthPassword))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// La API usa su propio token (apiTokenAuth), así los scripts no necesitan usuario y contraseña.
		if slices.Contains(cfg.PublicPaths, r.URL.Path) || strings.HasPrefix(r.URL.Path, apiPrefix) {
			next.ServeHTTP(w, r)
			return
		}

		gotUser, gotPassword, ok := r.BasicAuth()
		if !ok {
			// El navegador hace la primera petición sin credenciales y las pide al recibir el 401;
			// se cuenta pero no se registra en el log, porque es parte normal del inicio de sesión.
			cfg.Metrics.Inc(metricRejections, metrics.L("motivo", reasonNoCredentials))
		} else {
			userHash := sha256.Sum256([]byte(gotUser))
			passwordHash := sha256.Sum256([]byte(gotPassword))
			userOK := subtle.ConstantTimeCompare(userHash[:], wantUser[:]) == 1
			passwordOK := subtle.ConstantTimeCompare(passwordHash[:], wantPassword[:]) == 1
			if userOK && passwordOK {
				next.ServeHTTP(w, r)
				return
			}
			reject(cfg.Metrics, r, reasonBadCredentials, "usuario", gotUser)
		}

		w.Header().Set("WWW-Authenticate", `Basic realm="HTTPaaS", charset="UTF-8"`)
		http.Error(w, "Se requiere usuario y contraseña.", http.StatusUnauthorized)
	})
}
