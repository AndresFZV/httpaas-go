package dns

import (
	"context"
	"net/netip"
	"strings"
	"testing"
)

type fakeRunner struct {
	host   string
	script string
}

func (f *fakeRunner) Run(_ context.Context, host, script string) error {
	f.host = host
	f.script = script
	return nil
}

func newTestUpdater(f *fakeRunner) *Updater {
	return NewUpdater(f, "192.168.10.10", "cloud.local", "/etc/bind/cloud-key.key", 300)
}

func TestAddRecordSendsNsupdateCommands(t *testing.T) {
	f := &fakeRunner{}
	u := newTestUpdater(f)

	if err := u.AddRecord(context.Background(), "paisajes", netip.MustParseAddr("192.168.10.30")); err != nil {
		t.Fatalf("AddRecord() error: %v", err)
	}

	if f.host != "192.168.10.10" {
		t.Errorf("host = %q, se esperaba el servidor DNS", f.host)
	}
	for _, want := range []string{
		"sudo -n nsupdate -k /etc/bind/cloud-key.key",
		"server 192.168.10.10",
		"zone cloud.local",
		"update delete paisajes.cloud.local. A",
		"update add paisajes.cloud.local. 300 A 192.168.10.30",
		"send",
	} {
		if !strings.Contains(f.script, want) {
			t.Errorf("el script no contiene %q:\n%s", want, f.script)
		}
	}
}

func TestDeleteRecordOnlyDeletes(t *testing.T) {
	f := &fakeRunner{}
	u := newTestUpdater(f)

	if err := u.DeleteRecord(context.Background(), "paisajes"); err != nil {
		t.Fatalf("DeleteRecord() error: %v", err)
	}

	if !strings.Contains(f.script, "update delete paisajes.cloud.local. A") {
		t.Errorf("falta el update delete:\n%s", f.script)
	}
	if strings.Contains(f.script, "update add") {
		t.Errorf("DeleteRecord no debe agregar registros:\n%s", f.script)
	}
}
