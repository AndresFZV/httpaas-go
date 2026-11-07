package instance

import (
	"net/netip"
	"time"
)

const (
	// Domain es la zona DNS administrada por Bind9.
	Domain = "cloud.local"
	// PrefixLen es la longitud del prefijo de la red 192.168.10.0/24.
	PrefixLen = 24
)

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

// CIDR devuelve la IP con la longitud del prefijo de red, por ejemplo 192.168.10.30/24.
func (i Instance) CIDR() string {
	return netip.PrefixFrom(i.IP, PrefixLen).String()
}
