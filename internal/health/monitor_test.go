package health

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"github.com/AndresFZV/httpaas-go/internal/instance"
)

type fakeLister []instance.Instance

func (f fakeLister) List() []instance.Instance { return f }

type fakeVirtualBox struct{ err error }

func (f fakeVirtualBox) Version(context.Context) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return "7.2.6", nil
}

func TestCheckReportsInstancesAndComponents(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	up := netip.MustParseAddr("192.168.10.30")
	down := netip.MustParseAddr("192.168.10.31")
	instances := fakeLister{
		{Hostname: "paisajes", IP: up},
		{Hostname: "negocios", IP: down},
	}

	m := NewMonitor(Config{DNSServer: netip.MustParseAddr("127.0.0.1"), Domain: "cloud.local"}, instances, fakeVirtualBox{})
	m.instanceURL = func(ip netip.Addr) string {
		if ip == up {
			return server.URL
		}
		return "http://127.0.0.1:1/" // puerto sin servidor
	}

	m.Check(context.Background())
	snap := m.Snapshot()

	if !snap.Checked() {
		t.Fatal("el monitor no registró la verificación")
	}
	if snap.Instances["paisajes"] != InstanceActive {
		t.Errorf("paisajes = %q, se esperaba activa", snap.Instances["paisajes"])
	}
	if snap.Instances["negocios"] != InstanceUnreachable {
		t.Errorf("negocios = %q, se esperaba sin respuesta", snap.Instances["negocios"])
	}

	var vboxOK bool
	for _, c := range snap.Components {
		if c.Name == "VirtualBox" {
			vboxOK = c.OK
		}
	}
	if !vboxOK {
		t.Error("VirtualBox debía figurar como disponible")
	}
	if snap.Operational() {
		t.Error("sin DNS en 127.0.0.1 la infraestructura no debía figurar como operativa")
	}
}

func TestOperationalRequiresAllComponents(t *testing.T) {
	if (Snapshot{}).Operational() {
		t.Error("sin verificar no debía figurar como operativa")
	}

	m := NewMonitor(Config{DNSServer: netip.MustParseAddr("127.0.0.1"), Domain: "cloud.local"},
		fakeLister{}, fakeVirtualBox{err: errors.New("no instalado")})
	m.Check(context.Background())

	for _, c := range m.Snapshot().Components {
		if c.Name == "VirtualBox" && c.OK {
			t.Error("VirtualBox debía figurar con falla")
		}
	}
}

func TestSnapshotReturnsCopy(t *testing.T) {
	m := NewMonitor(Config{DNSServer: netip.MustParseAddr("127.0.0.1"), Domain: "cloud.local"},
		fakeLister{}, fakeVirtualBox{})
	m.Check(context.Background())

	snap := m.Snapshot()
	snap.Instances["intruso"] = InstanceActive
	snap.Components[0].OK = true

	again := m.Snapshot()
	if _, ok := again.Instances["intruso"]; ok {
		t.Error("modificar la copia cambió el estado del monitor")
	}
	if again.Components[0].OK {
		t.Error("modificar la copia cambió los componentes del monitor")
	}
}
