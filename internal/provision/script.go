package provision

import (
	"fmt"
	"net/netip"
)

// configureScriptPath es el script de la plantilla que valida los datos, cambia hostname,
// /etc/hosts e IP, y reinicia la VM a los 3 segundos.
const configureScriptPath = "/usr/local/bin/configurar-instancia.sh"

// configureCommand devuelve el comando que configura una VM recién clonada.
// El usuario deploy solo tiene sudo para ese script; -n hace que sudo falle de inmediato
// en vez de quedarse esperando una contraseña.
func configureCommand(hostname string, ip netip.Addr) string {
	return fmt.Sprintf("sudo -n %s %s %s\n", configureScriptPath, hostname, ip)
}
