package vbox

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"
)

// fakeRunner registra los subcomandos ejecutados y falla en el subcomando failOn.
type fakeRunner struct {
	calls  []string
	failOn string
}

func (f *fakeRunner) run(_ context.Context, args ...string) ([]byte, error) {
	f.calls = append(f.calls, args[0])
	if args[0] == f.failOn {
		return []byte("error simulado"), errors.New("exit status 1")
	}
	return nil, nil
}

func newTestManager(f *fakeRunner) *Manager {
	return &Manager{
		cfg: Config{
			TemplateDisk: `C:\VMs\ApacheServer.vdi`,
			HostOnlyIF:   "VirtualBox Host-Only Ethernet Adapter",
			MemoryMB:     512,
			CPUs:         1,
		},
		run:   f.run,
		sleep: func(time.Duration) {},
	}
}

func TestCreateVMRunsStepsInOrder(t *testing.T) {
	f := &fakeRunner{}
	m := newTestManager(f)

	if err := m.CreateVM(context.Background(), "paisajes"); err != nil {
		t.Fatalf("CreateVM() error: %v", err)
	}

	want := []string{"createvm", "modifyvm", "storagectl", "storageattach", "startvm"}
	if !slices.Equal(f.calls, want) {
		t.Errorf("comandos = %v, se esperaba %v", f.calls, want)
	}
}

func TestCreateVMCleansUpOnFailure(t *testing.T) {
	f := &fakeRunner{failOn: "storageattach"}
	m := newTestManager(f)

	if err := m.CreateVM(context.Background(), "paisajes"); err == nil {
		t.Fatal("CreateVM() no devolvió error")
	}

	want := []string{"createvm", "modifyvm", "storagectl", "storageattach", "controlvm", "unregistervm"}
	if !slices.Equal(f.calls, want) {
		t.Errorf("comandos = %v, se esperaba %v", f.calls, want)
	}
}

func TestCreateVMWithoutCleanupWhenCreateFails(t *testing.T) {
	f := &fakeRunner{failOn: "createvm"}
	m := newTestManager(f)

	if err := m.CreateVM(context.Background(), "paisajes"); err == nil {
		t.Fatal("CreateVM() no devolvió error")
	}

	if want := []string{"createvm"}; !slices.Equal(f.calls, want) {
		t.Errorf("comandos = %v, se esperaba %v", f.calls, want)
	}
}

func TestDeleteVMRetriesUnregister(t *testing.T) {
	f := &fakeRunner{failOn: "unregistervm"}
	m := newTestManager(f)

	if err := m.DeleteVM(context.Background(), "paisajes"); err == nil {
		t.Fatal("DeleteVM() no devolvió error")
	}

	unregisters := 0
	for _, call := range f.calls {
		if call == "unregistervm" {
			unregisters++
		}
	}
	if unregisters != unregisterRetries {
		t.Errorf("intentos de unregistervm = %d, se esperaban %d", unregisters, unregisterRetries)
	}
}
