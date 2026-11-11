package provision

import (
	"context"
	"errors"
	"net/netip"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/AndresFZV/httpaas-go/internal/instance"
)

type fakeVMs struct {
	created []string
	deleted []string
}

func (f *fakeVMs) CreateVM(_ context.Context, name string) error {
	f.created = append(f.created, name)
	return nil
}

func (f *fakeVMs) DeleteVM(_ context.Context, name string) error {
	f.deleted = append(f.deleted, name)
	return nil
}

type fakeSSH struct {
	waited  []string
	ranOn   []string
	lastRun string
}

func (f *fakeSSH) Run(_ context.Context, host, script string) error {
	f.ranOn = append(f.ranOn, host)
	f.lastRun = script
	return nil
}

func (f *fakeSSH) WaitForSSH(_ context.Context, host string, _ time.Duration) error {
	f.waited = append(f.waited, host)
	return nil
}

type fakeDNS struct {
	added   []string
	deleted []string
	failAdd bool
}

func (f *fakeDNS) AddRecord(_ context.Context, hostname string, _ netip.Addr) error {
	if f.failAdd {
		return errors.New("nsupdate falló")
	}
	f.added = append(f.added, hostname)
	return nil
}

func (f *fakeDNS) DeleteRecord(_ context.Context, hostname string) error {
	f.deleted = append(f.deleted, hostname)
	return nil
}

type fakeNeighbors struct {
	forgotten []string
}

func (f *fakeNeighbors) Forget(_ context.Context, ip netip.Addr) error {
	f.forgotten = append(f.forgotten, ip.String())
	return nil
}

func newTestProvisioner(t *testing.T, dns *fakeDNS) (*Provisioner, *instance.Store, *fakeVMs, *fakeSSH) {
	t.Helper()

	store, err := instance.NewStore(
		filepath.Join(t.TempDir(), "instances.json"),
		netip.MustParseAddr("192.168.10.30"),
		netip.MustParseAddr("192.168.10.254"),
	)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}

	vms := &fakeVMs{}
	ssh := &fakeSSH{}
	cfg := Config{
		TemplateIP:   netip.MustParseAddr("192.168.10.20"),
		BootTimeout:  time.Second,
		PollInterval: time.Millisecond,
	}
	return New(cfg, store, vms, ssh, dns, &fakeNeighbors{}), store, vms, ssh
}

func TestProvisionRunsAllSteps(t *testing.T) {
	dns := &fakeDNS{}
	p, store, vms, ssh := newTestProvisioner(t, dns)

	inst, err := p.Provision(context.Background(), "paisajes")
	if err != nil {
		t.Fatalf("Provision() error: %v", err)
	}

	if inst.IP.String() != "192.168.10.30" {
		t.Errorf("IP = %s, se esperaba 192.168.10.30", inst.IP)
	}
	if !slices.Equal(vms.created, []string{"paisajes"}) {
		t.Errorf("VMs creadas = %v", vms.created)
	}
	if want := []string{"192.168.10.20", "192.168.10.30"}; !slices.Equal(ssh.waited, want) {
		t.Errorf("esperas SSH = %v, se esperaba %v", ssh.waited, want)
	}
	if !slices.Equal(ssh.ranOn, []string{"192.168.10.20"}) {
		t.Errorf("script ejecutado en %v, se esperaba la IP temporal", ssh.ranOn)
	}
	if want := "sudo -n /usr/local/bin/configurar-instancia.sh paisajes 192.168.10.30"; !strings.Contains(ssh.lastRun, want) {
		t.Errorf("comando de configuración = %q, se esperaba %q", ssh.lastRun, want)
	}
	if !slices.Equal(dns.added, []string{"paisajes"}) {
		t.Errorf("registros DNS = %v", dns.added)
	}
	if _, err := store.Get("paisajes"); err != nil {
		t.Errorf("la instancia no quedó en el registro: %v", err)
	}
}

func TestProvisionRollsBackWhenDNSFails(t *testing.T) {
	dns := &fakeDNS{failAdd: true}
	p, store, vms, _ := newTestProvisioner(t, dns)

	if _, err := p.Provision(context.Background(), "paisajes"); err == nil {
		t.Fatal("Provision() no devolvió error")
	}

	if !slices.Equal(vms.deleted, []string{"paisajes"}) {
		t.Errorf("VMs borradas = %v, se esperaba paisajes", vms.deleted)
	}
	if _, err := store.Get("paisajes"); !errors.Is(err, instance.ErrNotFound) {
		t.Errorf("la instancia sigue en el registro: %v", err)
	}
}

func TestProvisionRejectsDuplicateWithoutCreatingVM(t *testing.T) {
	dns := &fakeDNS{}
	p, _, vms, _ := newTestProvisioner(t, dns)

	if _, err := p.Provision(context.Background(), "paisajes"); err != nil {
		t.Fatalf("primer Provision() error: %v", err)
	}
	_, err := p.Provision(context.Background(), "paisajes")
	if !errors.Is(err, instance.ErrHostnameTaken) {
		t.Errorf("Provision() duplicado = %v, se esperaba %v", err, instance.ErrHostnameTaken)
	}
	if len(vms.created) != 1 {
		t.Errorf("VMs creadas = %d, se esperaba 1", len(vms.created))
	}
}

func TestDeleteRemovesDNSVMAndRecord(t *testing.T) {
	dns := &fakeDNS{}
	p, store, vms, _ := newTestProvisioner(t, dns)
	if _, err := p.Provision(context.Background(), "paisajes"); err != nil {
		t.Fatalf("Provision() error: %v", err)
	}

	if err := p.Delete(context.Background(), "paisajes"); err != nil {
		t.Fatalf("Delete() error: %v", err)
	}

	if !slices.Equal(dns.deleted, []string{"paisajes"}) || !slices.Equal(vms.deleted, []string{"paisajes"}) {
		t.Errorf("DNS borrados = %v, VMs borradas = %v", dns.deleted, vms.deleted)
	}
	if _, err := store.Get("paisajes"); !errors.Is(err, instance.ErrNotFound) {
		t.Errorf("la instancia sigue en el registro: %v", err)
	}
}

func TestProvisionForgetsARPBeforeWaiting(t *testing.T) {
	dns := &fakeDNS{}
	p, _, _, _ := newTestProvisioner(t, dns)
	neighbors := &fakeNeighbors{}
	p.neighbors = neighbors

	if _, err := p.Provision(context.Background(), "paisajes"); err != nil {
		t.Fatalf("Provision() error: %v", err)
	}

	if want := []string{"192.168.10.20", "192.168.10.30"}; !slices.Equal(neighbors.forgotten, want) {
		t.Errorf("IPs limpiadas = %v, se esperaba %v", neighbors.forgotten, want)
	}
}
