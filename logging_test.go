package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestNewLoggerJSON(t *testing.T) {
	var buf bytes.Buffer
	logger, err := newLogger(&buf, "json", "info")
	if err != nil {
		t.Fatalf("newLogger() error: %v", err)
	}

	logger.Info("despliegue completado", "componente", "despliegue", "hostname", "paisajes")

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("la línea no es JSON válido: %v\n%s", err, buf.String())
	}
	if entry["msg"] != "despliegue completado" || entry["componente"] != "despliegue" || entry["hostname"] != "paisajes" {
		t.Errorf("entrada = %v", entry)
	}
}

func TestNewLoggerText(t *testing.T) {
	var buf bytes.Buffer
	logger, err := newLogger(&buf, "TEXT", "info")
	if err != nil {
		t.Fatalf("newLogger() error: %v", err)
	}

	logger.Info("instancia eliminada", "hostname", "paisajes")

	if line := buf.String(); !strings.Contains(line, `msg="instancia eliminada"`) || !strings.Contains(line, "hostname=paisajes") {
		t.Errorf("línea = %q", line)
	}
}

func TestNewLoggerLevel(t *testing.T) {
	var buf bytes.Buffer
	logger, err := newLogger(&buf, "text", "warn")
	if err != nil {
		t.Fatalf("newLogger() error: %v", err)
	}

	logger.Info("no debe aparecer")
	logger.Warn("sí debe aparecer")

	if out := buf.String(); strings.Contains(out, "no debe aparecer") || !strings.Contains(out, "sí debe aparecer") {
		t.Errorf("salida = %q", out)
	}
}

func TestNewLoggerRejectsInvalidValues(t *testing.T) {
	if _, err := newLogger(&bytes.Buffer{}, "xml", "info"); err == nil {
		t.Error("se aceptó el formato xml")
	}
	if _, err := newLogger(&bytes.Buffer{}, "text", "mucho"); err == nil {
		t.Error("se aceptó el nivel mucho")
	}
}
