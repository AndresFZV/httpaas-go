package vbox

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"
)

// fakeRunner registra los subcomandos ejecutados y falla en el subcomando failOn con la salida failOutput.
type fakeRunner struct {
	calls      []string
	failOn     string
	failOutput string
}

func (f *fakeRunner) run(_ context.Context, args ...string) ([]byte, error) {
	f.calls = append(f.calls, args[0])
	if args[0] == f.failOn {
		output := f.failOutput
		if output == "" {
			output = "error simulado"
		}
		return []byte(output), errors.New("exit status 1")
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

func TestDeleteVMIgnoresMissingVM(t *testing.T) {
	f := &fakeRunner{
		failOn:     "unregistervm",
		failOutput: "error: Could not find a registered machine named 'paisajes' (VBOX_E_OBJECT_NOT_FOUND)",
	}
	m := newTestManager(f)

	if err := m.DeleteVM(context.Background(), "paisajes"); err != nil {
		t.Fatalf("DeleteVM() de una VM inexistente = %v, se esperaba nil", err)
	}

	if want := []string{"controlvm", "unregistervm"}; !slices.Equal(f.calls, want) {
		t.Errorf("comandos = %v, se esperaba un solo intento de unregistervm", f.calls)
	}
}

func TestVersionReturnsTrimmedOutput(t *testing.T) {
	m := &Manager{run: func(context.Context, ...string) ([]byte, error) {
		return []byte("7.2.6r172322\r\n"), nil
	}}

	version, err := m.Version(context.Background())
	if err != nil {
		t.Fatalf("Version() error: %v", err)
	}
	if version != "7.2.6r172322" {
		t.Errorf("Version() = %q", version)
	}
}

func TestRunningVMsParsesNamesWithSpaces(t *testing.T) {
	m := &Manager{run: func(context.Context, ...string) ([]byte, error) {
		return []byte("\"debian ns\" {fb289a7c-9461-42a6-a186-6c91e0f2d3c7}\r\n" +
			"\"paisajes\" {f3976f6d-79a8-4f92-92f7-e8389d7d8f53}\r\n"), nil
	}}

	running, err := m.RunningVMs(context.Background())
	if err != nil {
		t.Fatalf("RunningVMs() error: %v", err)
	}
	if len(running) != 2 || !running["debian ns"] || !running["paisajes"] {
		t.Errorf("RunningVMs() = %v", running)
	}
}

func TestPowerCommandsUseExpectedArguments(t *testing.T) {
	var got [][]string
	m := &Manager{run: func(_ context.Context, args ...string) ([]byte, error) {
		got = append(got, args)
		return nil, nil
	}}
	ctx := context.Background()

	_ = m.StartVM(ctx, "paisajes")
	_ = m.ShutdownVM(ctx, "paisajes")
	_ = m.PowerOffVM(ctx, "paisajes")
	_ = m.ResetVM(ctx, "paisajes")

	want := [][]string{
		{"startvm", "paisajes", "--type", "headless"},
		{"controlvm", "paisajes", "acpipowerbutton"},
		{"controlvm", "paisajes", "poweroff"},
		{"controlvm", "paisajes", "reset"},
	}
	for i := range want {
		if !slices.Equal(got[i], want[i]) {
			t.Errorf("comando %d = %v, se esperaba %v", i, got[i], want[i])
		}
	}
}
