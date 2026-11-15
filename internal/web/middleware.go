package web

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/AndresFZV/httpaas-go/internal/metrics"
)

// Métricas de las peticiones HTTP.
const (
	metricRequests        = "httpaas_http_requests_total"
	metricRequestDuration = "httpaas_http_request_duration_seconds"
)

// requestIDPattern limita el identificador que puede enviar el cliente, para que no inyecte texto en el log.
var requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)

// quietPrefixes son rutas que se consultan cada pocos segundos (sondeo de la interfaz, archivos
// estáticos, salud y métricas). Se registran en nivel DEBUG para que no llenen el log.
var quietPrefixes = []string{"/static/", "/api/status", "/api/deployments/", "/healthz", "/readyz", "/metrics"}

// statusRecorder guarda el código de respuesta para el log y las métricas.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// Unwrap permite a http.ResponseController llegar al ResponseWriter original.
func (r *statusRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}

// Instrument registra cada petición en el log y en las métricas, y le asigna un identificador
// que se devuelve en la cabecera X-Request-ID. Va por fuera de todo lo demás, para que también
// cuente las peticiones rechazadas por la autenticación o la protección CSRF.
func Instrument(next http.Handler, reg *metrics.Registry) http.Handler {
	reg.Counter(metricRequests, "Peticiones HTTP atendidas, por método, ruta y código de respuesta.")
	reg.Summary(metricRequestDuration, "Duración de las peticiones HTTP en segundos, por método y ruta.")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		id := requestID(r)
		w.Header().Set("X-Request-ID", id)

		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		elapsed := time.Since(start)

		// El ServeMux deja en r.Pattern la ruta que atendió la petición, por ejemplo
		// "/instances/{hostname}/delete". Se usa la ruta y no la URL, para que las métricas
		// no tengan una serie distinta por cada hostname.
		route := routeOf(r)
		reg.Inc(metricRequests, metrics.L("metodo", r.Method), metrics.L("ruta", route),
			metrics.L("codigo", strconv.Itoa(rec.status)))
		reg.Observe(metricRequestDuration, elapsed.Seconds(), metrics.L("metodo", r.Method), metrics.L("ruta", route))

		slog.Log(context.Background(), requestLevel(r.URL.Path, rec.status), "petición atendida",
			"componente", "http",
			"peticion_id", id,
			"metodo", r.Method,
			"ruta", r.URL.Path,
			"estado", rec.status,
			"duracion_ms", elapsed.Milliseconds(),
		)
	})
}

// requestID usa el identificador que envió el cliente si es válido, o genera uno nuevo.
func requestID(r *http.Request) string {
	if id := r.Header.Get("X-Request-ID"); requestIDPattern.MatchString(id) {
		return id
	}
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// routeOf devuelve la ruta registrada que atendió la petición, sin el método.
// Las peticiones que no llegaron al ServeMux (rechazadas antes) o que no coinciden con ninguna ruta
// se agrupan como "sin_ruta".
func routeOf(r *http.Request) string {
	if r.Pattern == "" {
		return "sin_ruta"
	}
	if _, path, ok := strings.Cut(r.Pattern, " "); ok {
		return path
	}
	return r.Pattern
}

// requestLevel decide el nivel del log de una petición. Las rutas de sondeo van en DEBUG,
// salvo los errores internos; un 503 de /readyz es una respuesta esperada y también va en DEBUG.
func requestLevel(path string, status int) slog.Level {
	if status >= http.StatusInternalServerError && status != http.StatusServiceUnavailable {
		return slog.LevelError
	}
	for _, prefix := range quietPrefixes {
		if strings.HasPrefix(path, prefix) {
			return slog.LevelDebug
		}
	}
	switch {
	case status >= http.StatusInternalServerError:
		return slog.LevelError
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return slog.LevelWarn
	}
	return slog.LevelInfo
}
