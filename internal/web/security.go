package web

import (
	"crypto/sha256"
	"crypto/subtle"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/AndresFZV/httpaas-go/internal/auth"
	"github.com/AndresFZV/httpaas-go/internal/metrics"
)

// metricRejections cuenta las peticiones rechazadas por las protecciones de seguridad.
const metricRejections = "httpaas_security_rejections_total"

// Motivos de rechazo, para el log y las métricas.
const (
	reasonCrossOrigin    = "otro_origen"
	reasonNoCredentials  = "sin_credenciales"
	reasonBadCredentials = "credenciales_incorrectas"
	reasonNoSession      = "sin_sesion"
	reasonTooManyLogins  = "demasiados_intentos"
	reasonNoToken        = "sin_token"
	reasonBadToken       = "token_incorrecto"
)

// sessionCookie es la cookie que guarda el identificador de la sesión.
const sessionCookie = "httpaas_sesion"

// Rutas de inicio y cierre de sesión.
const (
	loginPath  = "/login"
	logoutPath = "/logout"
)

// apiSpecPath es la especificación OpenAPI, que se publica sin token para poder leer la documentación.
const apiSpecPath = apiPrefix + "openapi.yaml"

// metricsPath acepta también usuario y contraseña con autenticación básica, porque Prometheus
// no puede llenar el formulario de inicio de sesión.
const metricsPath = "/metrics"

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
	// Auth valida las credenciales y las sesiones. Si es nil, la interfaz no pide inicio de sesión.
	Auth *auth.Authenticator

	// PublicPaths son rutas que no piden autenticación, como las de salud que consultan
	// los orquestadores y balanceadores de carga.
	PublicPaths []string

	// APIToken es el token que deben enviar los clientes de /api/v1/ en la cabecera
	// Authorization: Bearer <token>. Si está vacío, la API REST queda deshabilitada.
	APIToken string

	// Metrics recibe los rechazos; puede ser nil.
	Metrics *metrics.Registry
}

// Secure envuelve next con las protecciones de la aplicación, de afuera hacia adentro:
// cabeceras de seguridad, token para /api/v1/, sesión para la interfaz (si está activa)
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
	if cfg.Auth != nil {
		handler = sessionAuth(handler, cfg)
	}
	handler = apiTokenAuth(handler, cfg)
	return securityHeaders(handler)
}

// sessionAuth exige una sesión iniciada en la interfaz. Sin sesión, las páginas redirigen
// al formulario de inicio de sesión y las consultas internas (/api/...) responden 401.
// No se aplica a la API versionada (usa token), a las rutas públicas, a los archivos estáticos
// ni al propio formulario.
func sessionAuth(next http.Handler, cfg SecurityConfig) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path == loginPath || strings.HasPrefix(path, "/static/") || strings.HasPrefix(path, apiPrefix) ||
			slices.Contains(cfg.PublicPaths, path) {
			next.ServeHTTP(w, r)
			return
		}

		if cookie, err := r.Cookie(sessionCookie); err == nil {
			// No se pasa el usuario en el contexto: crear una petición nueva haría que el middleware
			// de métricas no viera la ruta que asigna el ServeMux. Los handlers lo leen de la cookie.
			if _, ok := cfg.Auth.Session(cookie.Value); ok {
				next.ServeHTTP(w, r)
				return
			}
		}

		if path == metricsPath {
			metricsBasicAuth(w, r, next, cfg)
			return
		}

		// Llegar sin sesión es parte normal del flujo (la primera visita o una sesión vencida):
		// se cuenta pero no se registra en el log.
		cfg.Metrics.Inc(metricRejections, metrics.L("motivo", reasonNoSession))

		if strings.HasPrefix(path, "/api/") {
			writeAPIJSON(w, http.StatusUnauthorized, apiErrorBody{Error: apiError{
				Codigo:  "sesion_requerida",
				Mensaje: "la sesión venció; inicie sesión de nuevo",
			}})
			return
		}

		target := loginPath
		if r.Method == http.MethodGet && path != "/" && path != logoutPath {
			target += "?next=" + url.QueryEscape(r.URL.RequestURI())
		}
		http.Redirect(w, r, target, http.StatusSeeOther)
	})
}

// metricsBasicAuth deja pasar a /metrics con usuario y contraseña por autenticación básica (Prometheus).
func metricsBasicAuth(w http.ResponseWriter, r *http.Request, next http.Handler, cfg SecurityConfig) {
	user, password, ok := r.BasicAuth()
	if ok && cfg.Auth.Check(user, password) {
		next.ServeHTTP(w, r)
		return
	}
	if ok {
		reject(cfg.Metrics, r, reasonBadCredentials, "usuario", user)
	} else {
		cfg.Metrics.Inc(metricRejections, metrics.L("motivo", reasonNoCredentials))
	}
	w.Header().Set("WWW-Authenticate", `Basic realm="HTTPaaS métricas", charset="UTF-8"`)
	http.Error(w, "Se requiere usuario y contraseña.", http.StatusUnauthorized)
}

// apiTokenAuth exige el token de la API en las rutas /api/v1/; las demás pasan a la sesión.
// La comparación se hace en tiempo constante, igual que la de las credenciales.
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
