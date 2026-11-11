// Package health verifica periódicamente la infraestructura y las instancias.
package health

import (
	"context"
	"io"
	"maps"
	"net"
	"net/http"
	"net/netip"
	"slices"
	"sync"
	"time"

	"github.com/AndresFZV/httpaas-go/internal/instance"
)

const (
	dnsTimeout  = 2 * time.Second
	dialTimeout = 2 * time.Second
	httpTimeout = 3 * time.Second
	vboxTimeout = 10 * time.Second
)

// InstanceStatus es el resultado de verificar una instancia por HTTP.
type InstanceStatus string

const (
	InstanceActive      InstanceStatus = "active"
	InstanceUnreachable InstanceStatus = "unreachable"
)

// Component es el estado de una pieza de la infraestructura.
type Component struct {
	Name   string
	OK     bool
	Detail string
}

// Snapshot es el resultado de la última verificación.
type Snapshot struct {
	CheckedAt  time.Time
	Components []Component
	Instances  map[string]InstanceStatus // por hostname
	VMs        map[string]bool           // VMs encendidas según VirtualBox
	VMsKnown   bool                      // false si no se pudo consultar VirtualBox
}

// Checked indica si ya se hizo al menos una verificación.
func (s Snapshot) Checked() bool {
	return !s.CheckedAt.IsZero()
}

// Operational indica si todos los componentes respondieron en la última verificación.
func (s Snapshot) Operational() bool {
	if !s.Checked() {
		return false
	}
	for _, c := range s.Components {
		if !c.OK {
			return false
		}
	}
	return true
}

// VMRunning indica si la VM name estaba encendida; known es false si no se pudo consultar VirtualBox.
func (s Snapshot) VMRunning(name string) (running, known bool) {
	if !s.VMsKnown {
		return false, false
	}
	return s.VMs[name], true
}

// InstanceLister entrega las instancias registradas.
type InstanceLister interface {
	List() []instance.Instance
}

// VirtualBox permite comprobar que VBoxManage responde y qué VMs están encendidas.
type VirtualBox interface {
	Version(ctx context.Context) (string, error)
	RunningVMs(ctx context.Context) (map[string]bool, error)
}

// Config son los parámetros del monitor.
type Config struct {
	DNSServer netip.Addr
	Domain    string
	Interval  time.Duration
}

// Monitor verifica la infraestructura cada cierto tiempo y guarda el último resultado.
type Monitor struct {
	cfg         Config
	instances   InstanceLister
	vbox        VirtualBox
	client      *http.Client
	instanceURL func(netip.Addr) string

	mu   sync.RWMutex
	snap Snapshot
}

// NewMonitor crea un monitor; Start lo pone a correr.
func NewMonitor(cfg Config, instances InstanceLister, vbox VirtualBox) *Monitor {
	return &Monitor{
		cfg:         cfg,
		instances:   instances,
		vbox:        vbox,
		client:      &http.Client{Timeout: httpTimeout},
		instanceURL: func(ip netip.Addr) string { return "http://" + ip.String() + "/" },
	}
}

// Start verifica de inmediato y luego cada cfg.Interval, hasta que ctx se cancele.
func (m *Monitor) Start(ctx context.Context) {
	go func() {
		m.Check(ctx)

		ticker := time.NewTicker(m.cfg.Interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				m.Check(ctx)
			}
		}
	}()
}

// Snapshot devuelve una copia del último resultado.
func (m *Monitor) Snapshot() Snapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()

	snap := m.snap
	snap.Components = slices.Clone(m.snap.Components)
	snap.Instances = maps.Clone(m.snap.Instances)
	snap.VMs = maps.Clone(m.snap.VMs)
	return snap
}

// Check verifica en paralelo la infraestructura y todas las instancias, y guarda el resultado.
func (m *Monitor) Check(ctx context.Context) {
	var wg sync.WaitGroup

	var dns, ssh, vbox Component
	var vms map[string]bool
	var vmsKnown bool

	wg.Add(3)
	go func() { defer wg.Done(); dns = m.checkDNS(ctx) }()
	go func() { defer wg.Done(); ssh = m.checkSSH(ctx) }()
	go func() { defer wg.Done(); vbox, vms, vmsKnown = m.checkVirtualBox(ctx) }()

	list := m.instances.List()
	statuses := make([]InstanceStatus, len(list))
	for i, inst := range list {
		wg.Add(1)
		go func() {
			defer wg.Done()
			statuses[i] = m.checkInstance(ctx, inst.IP)
		}()
	}

	wg.Wait()

	instances := make(map[string]InstanceStatus, len(list))
	for i, inst := range list {
		instances[inst.Hostname] = statuses[i]
	}

	m.mu.Lock()
	m.snap = Snapshot{
		CheckedAt:  time.Now(),
		Components: []Component{dns, ssh, vbox},
		Instances:  instances,
		VMs:        vms,
		VMsKnown:   vmsKnown,
	}
	m.mu.Unlock()
}

// checkDNS consulta directamente a ns por ns.<dominio>, sin pasar por el DNS del anfitrión.
func (m *Monitor) checkDNS(ctx context.Context) Component {
	c := Component{Name: "Servidor DNS · Bind9"}
	server := netip.AddrPortFrom(m.cfg.DNSServer, 53).String()

	resolver := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			dialer := net.Dialer{Timeout: dnsTimeout}
			return dialer.DialContext(ctx, network, server)
		},
	}

	ctx, cancel := context.WithTimeout(ctx, dnsTimeout)
	defer cancel()

	name := "ns." + m.cfg.Domain
	if addrs, err := resolver.LookupHost(ctx, name); err != nil || len(addrs) == 0 {
		c.Detail = "No responde consultas"
		return c
	}
	c.OK = true
	c.Detail = "Resuelve " + name
	return c
}

// checkSSH comprueba que ns acepte conexiones SSH, que la app usa para actualizar el DNS.
func (m *Monitor) checkSSH(ctx context.Context) Component {
	c := Component{Name: "Acceso SSH a ns"}
	dialer := net.Dialer{Timeout: dialTimeout}

	conn, err := dialer.DialContext(ctx, "tcp", netip.AddrPortFrom(m.cfg.DNSServer, 22).String())
	if err != nil {
		c.Detail = "Puerto 22 sin respuesta"
		return c
	}
	conn.Close()
	c.OK = true
	c.Detail = "Puerto 22 disponible"
	return c
}

// checkVirtualBox comprueba que VBoxManage responda y obtiene las VMs encendidas.
func (m *Monitor) checkVirtualBox(ctx context.Context) (c Component, vms map[string]bool, known bool) {
	c = Component{Name: "VirtualBox"}

	ctx, cancel := context.WithTimeout(ctx, vboxTimeout)
	defer cancel()

	version, err := m.vbox.Version(ctx)
	if err != nil {
		c.Detail = "VBoxManage no responde"
		return c, nil, false
	}
	c.OK = true
	c.Detail = "Versión " + version

	vms, err = m.vbox.RunningVMs(ctx)
	if err != nil {
		return c, nil, false
	}
	return c, vms, true
}

// checkInstance hace una petición HTTP a la instancia.
func (m *Monitor) checkInstance(ctx context.Context, ip netip.Addr) InstanceStatus {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.instanceURL(ip), nil)
	if err != nil {
		return InstanceUnreachable
	}

	resp, err := m.client.Do(req)
	if err != nil {
		return InstanceUnreachable
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))

	if resp.StatusCode >= http.StatusBadRequest {
		return InstanceUnreachable
	}
	return InstanceActive
}
