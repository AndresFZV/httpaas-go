// Package hostnet agrupa operaciones sobre la red del equipo anfitrión.
package hostnet

import (
	"context"
	"fmt"
	"net/netip"
	"os/exec"
	"strings"
)

// runFunc ejecuta el comando arp con args y devuelve su salida combinada.
type runFunc func(ctx context.Context, args ...string) ([]byte, error)

// ARPCache borra entradas de la caché ARP del anfitrión.
type ARPCache struct {
	run runFunc
}

// NewARPCache crea un ARPCache que usa el comando arp del sistema.
func NewARPCache() *ARPCache {
	return &ARPCache{run: execARP}
}

// Forget borra la entrada de ip de la caché ARP, para que el anfitrión vuelva a preguntar
// qué tarjeta de red tiene esa IP. En Windows requiere ejecutar la app como administrador.
func (c *ARPCache) Forget(ctx context.Context, ip netip.Addr) error {
	out, err := c.run(ctx, "-d", ip.String())
	if err != nil {
		return fmt.Errorf("borrar %s de la caché ARP: %w: %s", ip, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func execARP(ctx context.Context, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, "arp", args...).CombinedOutput()
}
