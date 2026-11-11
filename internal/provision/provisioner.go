package provision

import (
	"context"
	"fmt"
	"log"
	"net/netip"
	"sync"
	"time"

	"github.com/AndresFZV/httpaas-go/internal/instance"
)

const cleanupTimeout = 2 * time.Minute

// VMManager crea y elimina máquinas virtuales.
type VMManager interface {
	CreateVM(ctx context.Context, name string) error
	DeleteVM(ctx context.Context, name string) error
}

// RemoteRunner ejecuta scripts en máquinas remotas.
type RemoteRunner interface {
	Run(ctx context.Context, host, script string) error
	WaitForSSH(ctx context.Context, host string, interval time.Duration) error
}

// DNSUpdater registra y elimina hosts en la zona DNS.
type DNSUpdater interface {
	AddRecord(ctx context.Context, hostname string, ip netip.Addr) error
	DeleteRecord(ctx context.Context, hostname string) error
}

// Config son los parámetros del aprovisionamiento.
type Config struct {
	TemplateIP   netip.Addr    // IP con la que arranca toda VM clonada de la plantilla
	BootTimeout  time.Duration // tiempo máximo para que la VM responda por SSH
	PollInterval time.Duration // espera entre intentos de conexión SSH
}

// Provisioner crea y elimina instancias completas del servidor Apache.
type Provisioner struct {
	mu    sync.Mutex
	cfg   Config
	store *instance.Store
	vms   VMManager
	ssh   RemoteRunner
	dns   DNSUpdater
}

// New crea un Provisioner con sus dependencias.
func New(cfg Config, store *instance.Store, vms VMManager, ssh RemoteRunner, dns DNSUpdater) *Provisioner {
	return &Provisioner{cfg: cfg, store: store, vms: vms, ssh: ssh, dns: dns}
}

// Provision reserva el nombre y la IP, crea la VM, le asigna hostname e IP definitivos
// y la registra en el DNS. Si un paso falla, deshace los anteriores.
// Las instancias se crean de a una, porque todas arrancan con la IP temporal de la plantilla.
func (p *Provisioner) Provision(ctx context.Context, hostname string) (instance.Instance, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	inst, err := p.store.Reserve(hostname, time.Now())
	if err != nil {
		return instance.Instance{}, err
	}

	vmCreated, err := p.createAndConfigure(ctx, inst)
	if err != nil {
		p.rollback(inst, vmCreated)
		return instance.Instance{}, err
	}
	return inst, nil
}

// Delete quita el registro DNS, borra la VM y elimina la instancia del registro.
// Si la VM no se puede borrar, la instancia se conserva para poder reintentar.
func (p *Provisioner) Delete(ctx context.Context, hostname string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if _, err := p.store.Get(hostname); err != nil {
		return err
	}

	// Un registro DNS sobrante no impide liberar la VM; solo se registra en el log.
	if err := p.dns.DeleteRecord(ctx, hostname); err != nil {
		log.Printf("eliminar instancia %s: %v", hostname, err)
	}
	if err := p.vms.DeleteVM(ctx, hostname); err != nil {
		return err
	}
	return p.store.Remove(hostname)
}

// createAndConfigure ejecuta los pasos del aprovisionamiento e indica si la VM alcanzó a crearse.
func (p *Provisioner) createAndConfigure(ctx context.Context, inst instance.Instance) (vmCreated bool, err error) {
	if err := p.vms.CreateVM(ctx, inst.Hostname); err != nil {
		return false, err
	}

	if err := p.waitForSSH(ctx, p.cfg.TemplateIP); err != nil {
		return true, fmt.Errorf("la VM %s no respondió en la IP temporal: %w", inst.Hostname, err)
	}

	// El script de la plantilla reinicia la VM a los 3 segundos; el comando termina antes.
	if err := p.ssh.Run(ctx, p.cfg.TemplateIP.String(), configureCommand(inst.Hostname, inst.IP)); err != nil {
		return true, fmt.Errorf("configurar hostname e IP de %s: %w", inst.Hostname, err)
	}

	if err := p.waitForSSH(ctx, inst.IP); err != nil {
		return true, fmt.Errorf("la VM %s no respondió en %s: %w", inst.Hostname, inst.IP, err)
	}

	if err := p.dns.AddRecord(ctx, inst.Hostname, inst.IP); err != nil {
		return true, err
	}
	return true, nil
}

func (p *Provisioner) waitForSSH(ctx context.Context, ip netip.Addr) error {
	ctx, cancel := context.WithTimeout(ctx, p.cfg.BootTimeout)
	defer cancel()
	return p.ssh.WaitForSSH(ctx, ip.String(), p.cfg.PollInterval)
}

// rollback deshace un aprovisionamiento fallido. Los errores se registran en el log
// porque el error original es el que se le informa al usuario.
func (p *Provisioner) rollback(inst instance.Instance, vmCreated bool) {
	ctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
	defer cancel()

	if vmCreated {
		if err := p.dns.DeleteRecord(ctx, inst.Hostname); err != nil {
			log.Printf("rollback de %s: %v", inst.Hostname, err)
		}
		if err := p.vms.DeleteVM(ctx, inst.Hostname); err != nil {
			log.Printf("rollback de %s: %v", inst.Hostname, err)
		}
	}
	if err := p.store.Remove(inst.Hostname); err != nil {
		log.Printf("rollback de %s: %v", inst.Hostname, err)
	}
}
