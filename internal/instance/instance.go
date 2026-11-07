package instance

import (
	"net/netip"
	"time"
)

// Domain es la zona DNS administrada por Bind9.
const Domain = "cloud.local"

// Instance es una máquina virtual con Apache aprovisionada por la aplicación.
type Instance struct {
	Hostname  string     `json:"hostname"`
	IP        netip.Addr `json:"ip"`
	CreatedAt time.Time  `json:"created_at"`
}

// FQDN devuelve el nombre completo de la instancia dentro del dominio.
func (i Instance) FQDN() string {
	return i.Hostname + "." + Domain
}

// URL devuelve la dirección para abrir la página publicada en la instancia.
func (i Instance) URL() string {
	return "http://" + i.FQDN()
}
