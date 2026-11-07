package main

import (
	"log"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"

	"github.com/AndresFZV/httpaas-go/internal/instance"
	"github.com/AndresFZV/httpaas-go/internal/web"
)

// Rango de IPs para las instancias Apache; la .10 es del servidor DNS ns.
const (
	firstInstanceIP = "192.168.10.30"
	lastInstanceIP  = "192.168.10.254"
)

func main() {
	addr := getEnv("HTTPAAS_ADDR", ":8080")
	dataFile := getEnv("HTTPAAS_DATA_FILE", filepath.Join("data", "instances.json"))

	store, err := instance.NewStore(
		dataFile,
		netip.MustParseAddr(firstInstanceIP),
		netip.MustParseAddr(lastInstanceIP),
	)
	if err != nil {
		log.Fatalf("no se pudo cargar el registro de instancias: %v", err)
	}

	handler, err := web.NewHandler(store, "templates", "static")
	if err != nil {
		log.Fatalf("no se pudo inicializar la aplicación: %v", err)
	}

	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	log.Printf("servidor escuchando en http://localhost%s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("el servidor se detuvo: %v", err)
	}
}

// getEnv devuelve el valor de la variable de entorno key o fallback si no está definida.
func getEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}
