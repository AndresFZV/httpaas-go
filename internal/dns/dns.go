package dns

import (
	"context"
	"fmt"
	"net/netip"
)

// Runner ejecuta scripts de bash en una máquina remota.
type Runner interface {
	Run(ctx context.Context, host, script string) error
}

// Updater administra registros A de la zona en Bind9.
// Ejecuta nsupdate -l por SSH en el servidor DNS, que usa la llave de sesión local de Bind9.
type Updater struct {
	runner Runner
	server string
	zone   string
	ttl    int
}

// NewUpdater crea un Updater para la zona zone en el servidor DNS server.
func NewUpdater(runner Runner, server, zone string, ttl int) *Updater {
	return &Updater{runner: runner, server: server, zone: zone, ttl: ttl}
}

// AddRecord crea o reemplaza el registro A de hostname apuntando a ip.
func (u *Updater) AddRecord(ctx context.Context, hostname string, ip netip.Addr) error {
	fqdn := u.fqdn(hostname)
	commands := fmt.Sprintf("update delete %s A\nupdate add %s %d A %s\n", fqdn, fqdn, u.ttl, ip)

	if err := u.runner.Run(ctx, u.server, u.script(commands)); err != nil {
		return fmt.Errorf("registrar %s en el DNS: %w", fqdn, err)
	}
	return nil
}

// DeleteRecord elimina el registro A de hostname.
func (u *Updater) DeleteRecord(ctx context.Context, hostname string) error {
	fqdn := u.fqdn(hostname)
	commands := fmt.Sprintf("update delete %s A\n", fqdn)

	if err := u.runner.Run(ctx, u.server, u.script(commands)); err != nil {
		return fmt.Errorf("eliminar %s del DNS: %w", fqdn, err)
	}
	return nil
}

// script arma el script de bash que pasa los comandos a nsupdate.
func (u *Updater) script(commands string) string {
	return fmt.Sprintf("sudo nsupdate -l <<'EOF'\nzone %s.\n%ssend\nEOF\n", u.zone, commands)
}

func (u *Updater) fqdn(hostname string) string {
	return hostname + "." + u.zone + "."
}
