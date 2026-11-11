package deployment

import (
	"context"
	"errors"
	"io"
	"net/netip"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/AndresFZV/httpaas-go/internal/instance"
	"github.com/AndresFZV/httpaas-go/internal/provision"
)

// fakeProvisioner informa las etapas hasta failAt (0 = no falla) y luego falla con err.
type fakeProvisioner struct {
	failAt provision.Stage
	err    error
	calls  int
}

func (f *fakeProvisioner) Provision(_ context.Context, hostname string, report provision.ReportFunc) (instance.Instance, error) {
	f.calls++
	inst := instance.Instance{Hostname: hostname, IP: netip.MustParseAddr("192.168.10.30")}
	for stage := provision.StageCreateVM; stage <= provision.StageRegisterDNS; stage++ {
		report(stage, inst)
		if stage == f.failAt {
			return instance.Instance{}, f.err
		}
	}
	return inst, nil
}

type fakeDeployer struct {
	fail     bool
	calls    int
	received string
}

func (f *fakeDeployer) Deploy(_ context.Context, _ string, archive io.Reader) error {
	f.calls++
	data, _ := io.ReadAll(archive)
	f.received = string(data)
	if f.fail {
		return errors.New("ssh falló")
	}
	return nil
}

type fakeVerifier struct{ calls int }

func (f *fakeVerifier) Verify(context.Context, netip.Addr) error {
	f.calls++
	return nil
}

func newTestManager(t *testing.T, p *fakeProvisioner, d *fakeDeployer, v *fakeVerifier) (*Manager, string) {
	t.Helper()
	dir := t.TempDir()
	m, err := NewManager(dir, p, d, v)
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	return m, dir
}

// waitFinished espera a que el despliegue deje de estar en curso.
func waitFinished(t *testing.T, m *Manager, id string) Snapshot {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		snap, err := m.Snapshot(id)
		if err != nil {
			t.Fatalf("Snapshot() error: %v", err)
		}
		if snap.Status != StatusRunning {
			return snap
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("el despliegue no terminó a tiempo")
	return Snapshot{}
}

func uploadsLeft(t *testing.T, dir string) int {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir() error: %v", err)
	}
	return len(entries)
}

func TestDeploymentSucceedsWithContent(t *testing.T) {
	p, d, v := &fakeProvisioner{}, &fakeDeployer{}, &fakeVerifier{}
	m, dir := newTestManager(t, p, d, v)

	id, err := m.Start("paisajes", strings.NewReader("contenido zip"), "paisajes.zip")
	if err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	snap := waitFinished(t, m, id)

	if snap.Status != StatusSucceeded || snap.Percent != 100 {
		t.Errorf("estado = %s (%d%%), se esperaba éxito al 100%%", snap.Status, snap.Percent)
	}
	if snap.IP != "192.168.10.30" || snap.FQDN != "paisajes.cloud.local" {
		t.Errorf("IP = %q, FQDN = %q", snap.IP, snap.FQDN)
	}
	if d.received != "contenido zip" || v.calls != 1 {
		t.Errorf("contenido publicado = %q, verificaciones = %d", d.received, v.calls)
	}
	if n := uploadsLeft(t, dir); n != 0 {
		t.Errorf("quedaron %d archivos subidos, se esperaba 0", n)
	}
}

func TestDeploymentWithoutContentSkipsPublish(t *testing.T) {
	p, d, v := &fakeProvisioner{}, &fakeDeployer{}, &fakeVerifier{}
	m, _ := newTestManager(t, p, d, v)

	id, err := m.Start("paisajes", nil, "")
	if err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	snap := waitFinished(t, m, id)

	if snap.Status != StatusSucceeded || snap.HasContent {
		t.Errorf("estado = %s, con contenido = %v", snap.Status, snap.HasContent)
	}
	if d.calls != 0 || v.calls != 1 {
		t.Errorf("publicaciones = %d, verificaciones = %d", d.calls, v.calls)
	}
}

func TestDeploymentFailureKeepsArchiveAndReportsStep(t *testing.T) {
	p := &fakeProvisioner{failAt: provision.StageRegisterDNS, err: errors.New("nsupdate falló")}
	m, dir := newTestManager(t, p, &fakeDeployer{}, &fakeVerifier{})

	id, _ := m.Start("paisajes", strings.NewReader("zip"), "paisajes.zip")
	snap := waitFinished(t, m, id)

	if snap.Status != StatusFailed || snap.FailedStep != StepRegisterDNS {
		t.Errorf("estado = %s, etapa fallida = %d", snap.Status, snap.FailedStep)
	}
	if snap.Error != Steps[StepRegisterDNS].FailMessage || snap.Component != "Servidor DNS · Bind9" {
		t.Errorf("error = %q, componente = %q", snap.Error, snap.Component)
	}
	if n := uploadsLeft(t, dir); n != 1 {
		t.Errorf("archivos subidos = %d, se esperaba conservar 1 para reintentar", n)
	}
}

func TestDeploymentShowsUserErrors(t *testing.T) {
	p := &fakeProvisioner{failAt: provision.StageCreateVM, err: instance.ErrHostnameTaken}
	m, _ := newTestManager(t, p, &fakeDeployer{}, &fakeVerifier{})

	id, _ := m.Start("paisajes", nil, "")
	snap := waitFinished(t, m, id)

	if snap.Error != instance.ErrHostnameTaken.Error() {
		t.Errorf("error = %q, se esperaba el mensaje del registro", snap.Error)
	}
}

func TestRetryAfterPublishFailureOnlyRepublishes(t *testing.T) {
	p, d, v := &fakeProvisioner{}, &fakeDeployer{fail: true}, &fakeVerifier{}
	m, dir := newTestManager(t, p, d, v)

	id, _ := m.Start("paisajes", strings.NewReader("zip"), "paisajes.zip")
	if snap := waitFinished(t, m, id); snap.FailedStep != StepPublish {
		t.Fatalf("etapa fallida = %d, se esperaba la de publicación", snap.FailedStep)
	}

	d.fail = false
	retryID, err := m.Retry(id)
	if err != nil {
		t.Fatalf("Retry() error: %v", err)
	}
	snap := waitFinished(t, m, retryID)

	if snap.Status != StatusSucceeded {
		t.Errorf("estado del reintento = %s", snap.Status)
	}
	if p.calls != 1 {
		t.Errorf("aprovisionamientos = %d, el reintento no debía crear otra VM", p.calls)
	}
	if d.received != "zip" {
		t.Errorf("el reintento no publicó el mismo archivo: %q", d.received)
	}
	if n := uploadsLeft(t, dir); n != 0 {
		t.Errorf("quedaron %d archivos subidos tras el éxito", n)
	}
}

func TestRetryRejectsRunningOrUnknown(t *testing.T) {
	m, _ := newTestManager(t, &fakeProvisioner{}, &fakeDeployer{}, &fakeVerifier{})

	if _, err := m.Retry("no-existe"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Retry() de un ID desconocido = %v", err)
	}

	id, _ := m.Start("paisajes", nil, "")
	waitFinished(t, m, id)
	if _, err := m.Retry(id); !errors.Is(err, ErrNotRetryable) {
		t.Errorf("Retry() de un despliegue exitoso = %v", err)
	}
}
