package power

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"
)

type fakeVMs struct {
	mu          sync.Mutex
	calls       []string
	running     map[string]bool
	stopsOnACPI bool // si es false, la VM ignora el apagado ordenado
	startErr    error
	blockACPI   chan struct{} // si no es nil, el apagado ordenado espera a que se cierre
}

func (f *fakeVMs) record(call string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call)
}

func (f *fakeVMs) StartVM(_ context.Context, name string) error {
	f.record("start " + name)
	return f.startErr
}

func (f *fakeVMs) ShutdownVM(_ context.Context, name string) error {
	if f.blockACPI != nil {
		<-f.blockACPI
	}
	f.record("acpi " + name)
	if f.stopsOnACPI {
		f.mu.Lock()
		delete(f.running, name)
		f.mu.Unlock()
	}
	return nil
}

func (f *fakeVMs) PowerOffVM(_ context.Context, name string) error {
	f.record("poweroff " + name)
	f.mu.Lock()
	delete(f.running, name)
	f.mu.Unlock()
	return nil
}

func (f *fakeVMs) ResetVM(_ context.Context, name string) error {
	f.record("reset " + name)
	return nil
}

func (f *fakeVMs) RunningVMs(context.Context) (map[string]bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	copied := make(map[string]bool, len(f.running))
	for name, on := range f.running {
		copied[name] = on
	}
	return copied, nil
}

func (f *fakeVMs) callList() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

func newTestController(vms *fakeVMs) *Controller {
	c := NewController(vms)
	c.shutdownTimeout = 50 * time.Millisecond
	c.pollInterval = 5 * time.Millisecond
	return c
}

// waitIdle espera a que la VM deje de estar ocupada.
func waitIdle(t *testing.T, c *Controller, name string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		busy := c.busy[name]
		c.mu.Unlock()
		if !busy {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("la acción no terminó a tiempo")
}

func TestStartRecordsPendingAction(t *testing.T) {
	vms := &fakeVMs{}
	c := newTestController(vms)

	if err := c.Do(context.Background(), "paisajes", ActionStart); err != nil {
		t.Fatalf("Do() error: %v", err)
	}

	if got := c.Pending()["paisajes"].Action; got != ActionStart {
		t.Errorf("acción pendiente = %q, se esperaba %q", got, ActionStart)
	}
	if !slices.Equal(vms.callList(), []string{"start paisajes"}) {
		t.Errorf("comandos = %v", vms.callList())
	}
}

func TestStartErrorIsReturnedWithoutPending(t *testing.T) {
	vms := &fakeVMs{startErr: errors.New("VM bloqueada")}
	c := newTestController(vms)

	if err := c.Do(context.Background(), "paisajes", ActionStart); err == nil {
		t.Fatal("Do() no devolvió error")
	}
	if _, ok := c.Pending()["paisajes"]; ok {
		t.Error("una acción fallida no debía quedar pendiente")
	}
}

func TestStopGracefullyWithoutForcing(t *testing.T) {
	vms := &fakeVMs{running: map[string]bool{"paisajes": true}, stopsOnACPI: true}
	c := newTestController(vms)

	if err := c.Do(context.Background(), "paisajes", ActionStop); err != nil {
		t.Fatalf("Do() error: %v", err)
	}
	waitIdle(t, c, "paisajes")

	if slices.Contains(vms.callList(), "poweroff paisajes") {
		t.Errorf("no debía forzar el apagado: %v", vms.callList())
	}
	if _, ok := c.Pending()["paisajes"]; ok {
		t.Error("el apagado terminado no debía seguir pendiente")
	}
}

func TestStopForcesPowerOffAfterTimeout(t *testing.T) {
	vms := &fakeVMs{running: map[string]bool{"paisajes": true}, stopsOnACPI: false}
	c := newTestController(vms)

	if err := c.Do(context.Background(), "paisajes", ActionStop); err != nil {
		t.Fatalf("Do() error: %v", err)
	}
	waitIdle(t, c, "paisajes")

	if !slices.Contains(vms.callList(), "poweroff paisajes") {
		t.Errorf("debía forzar el apagado: %v", vms.callList())
	}
}

func TestRejectsConcurrentActions(t *testing.T) {
	vms := &fakeVMs{running: map[string]bool{"paisajes": true}, stopsOnACPI: true, blockACPI: make(chan struct{})}
	c := newTestController(vms)

	if err := c.Do(context.Background(), "paisajes", ActionStop); err != nil {
		t.Fatalf("Do() error: %v", err)
	}
	if err := c.Do(context.Background(), "paisajes", ActionStart); !errors.Is(err, ErrInProgress) {
		t.Errorf("Do() durante otra acción = %v, se esperaba %v", err, ErrInProgress)
	}
	if got := c.Pending()["paisajes"].Action; got != ActionStop {
		t.Errorf("acción pendiente = %q, se esperaba %q", got, ActionStop)
	}

	close(vms.blockACPI)
	waitIdle(t, c, "paisajes")
}

func TestParseAction(t *testing.T) {
	if action, err := ParseAction("restart"); err != nil || action != ActionRestart {
		t.Errorf("ParseAction(restart) = %q, %v", action, err)
	}
	if _, err := ParseAction("borrar"); !errors.Is(err, ErrUnknownAction) {
		t.Errorf("ParseAction(borrar) = %v, se esperaba %v", err, ErrUnknownAction)
	}
}
