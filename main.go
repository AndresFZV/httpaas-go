package main

import (
	"log"
	"net/http"
	"os"

	"github.com/AndresFZV/httpaas-go/internal/web"
)

func main() {
	addr := getEnv("HTTPAAS_ADDR", ":8080")

	handler, err := web.NewHandler("templates", "static")
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
