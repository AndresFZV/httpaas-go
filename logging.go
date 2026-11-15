package main

import (
	"fmt"
	"io"
	"log/slog"
	"strings"
)

// newLogger crea el logger de la aplicación. format es "text" (clave=valor, para leer en la terminal)
// o "json" (una línea JSON por evento, para herramientas como Loki o Elasticsearch).
// level es el nivel mínimo que se registra: debug, info, warn o error.
func newLogger(w io.Writer, format, level string) (*slog.Logger, error) {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(level)); err != nil {
		return nil, fmt.Errorf("HTTPAAS_LOG_LEVEL: %q no es un nivel válido (debug, info, warn o error)", level)
	}
	opts := &slog.HandlerOptions{Level: lvl}

	switch strings.ToLower(format) {
	case "text":
		return slog.New(slog.NewTextHandler(w, opts)), nil
	case "json":
		return slog.New(slog.NewJSONHandler(w, opts)), nil
	default:
		return nil, fmt.Errorf("HTTPAAS_LOG_FORMAT: %q no es un formato válido (text o json)", format)
	}
}
