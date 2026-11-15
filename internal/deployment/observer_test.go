package deployment

import (
	"errors"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AndresFZV/httpaas-go/internal/provision"
)

// fakeObserver guarda las mediciones que recibe; el despliegue corre en otra goroutine.
type fakeObserver struct {
	mu       sync.Mutex
	steps    []string
	finished []string // "tipo/resultado"
}

func (f *fakeObserver) StepCompleted(step string, _ time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.steps = append(f.steps, step)
}

func (f *fakeObserver) DeploymentFinished(kind, result string, _ time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.finished = append(f.finished, kind+"/"+result)
}

func (f *fakeObserver) snapshot() ([]string, []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.steps), slices.Clone(f.finished)
}

func TestObserverReceivesAllStepsOfASuccessfulDeployment(t *testing.T) {
	m, _ := newTestManager(t, &fakeProvisioner{}, &fakeDeployer{}, &fakeVerifier{})
	obs := &fakeObserver{}
	m.SetObserver(obs)

	id, _ := m.Start("paisajes", strings.NewReader("zip"), "paisajes.zip")
	waitFinished(t, m, id)

	steps, finished := obs.snapshot()
	want := []string{"validar", "crear_vm", "esperar_arranque", "configurar", "esperar_ip", "registrar_dns", "publicar", "verificar"}
	if !slices.Equal(steps, want) {
		t.Errorf("etapas = %v, se esperaba %v", steps, want)
	}
	if !slices.Equal(finished, []string{KindCreate + "/" + ResultSuccess}) {
		t.Errorf("despliegues terminados = %v", finished)
	}
}

func TestObserverSkipsPublishWithoutContent(t *testing.T) {
	m, _ := newTestManager(t, &fakeProvisioner{}, &fakeDeployer{}, &fakeVerifier{})
	obs := &fakeObserver{}
	m.SetObserver(obs)

	id, _ := m.Start("paisajes", nil, "")
	waitFinished(t, m, id)

	steps, _ := obs.snapshot()
	if slices.Contains(steps, "publicar") {
		t.Errorf("etapas = %v; la publicación omitida no debe medirse", steps)
	}
}

func TestObserverOnFailureMeasuresOnlyCompletedSteps(t *testing.T) {
	p := &fakeProvisioner{failAt: provision.StageRegisterDNS, err: errors.New("nsupdate falló")}
	m, _ := newTestManager(t, p, &fakeDeployer{}, &fakeVerifier{})
	obs := &fakeObserver{}
	m.SetObserver(obs)

	id, _ := m.Start("paisajes", strings.NewReader("zip"), "paisajes.zip")
	waitFinished(t, m, id)

	steps, finished := obs.snapshot()
	want := []string{"validar", "crear_vm", "esperar_arranque", "configurar", "esperar_ip"}
	if !slices.Equal(steps, want) {
		t.Errorf("etapas = %v, se esperaba %v", steps, want)
	}
	if !slices.Equal(finished, []string{KindCreate + "/" + ResultFailure}) {
		t.Errorf("despliegues terminados = %v", finished)
	}
}

func TestObserverPublishKind(t *testing.T) {
	m, _ := newTestManager(t, &fakeProvisioner{}, &fakeDeployer{}, &fakeVerifier{})
	obs := &fakeObserver{}
	m.SetObserver(obs)

	id, err := m.Publish("paisajes", netip.MustParseAddr("192.168.10.30"), strings.NewReader("zip"), "paisajes.zip")
	if err != nil {
		t.Fatalf("Publish() error: %v", err)
	}
	waitFinished(t, m, id)

	steps, finished := obs.snapshot()
	if !slices.Equal(steps, []string{"publicar", "verificar"}) {
		t.Errorf("etapas = %v", steps)
	}
	if !slices.Equal(finished, []string{KindPublish + "/" + ResultSuccess}) {
		t.Errorf("despliegues terminados = %v", finished)
	}
}
