package remote

import (
	"context"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sampleKnownHosts = `192.168.10.10 ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIns
192.168.10.20 ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAItemporal
192.168.10.20 ecdsa-sha2-nistp256 AAAAE2VjZHNhLXNoYTItbmlzdHAyNTYtemporal
[192.168.10.30]:2222 ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIpuerto
192.168.10.31,paisajes.cloud.local ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIvarios
# comentario sobre 192.168.10.20
`

func newTestKnownHosts(t *testing.T, content string) *KnownHosts {
	t.Helper()
	path := filepath.Join(t.TempDir(), "data", "known_hosts")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	k, err := NewKnownHosts(path)
	if err != nil {
		t.Fatalf("NewKnownHosts() error: %v", err)
	}
	return k
}

func TestNewKnownHostsCreatesEmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data", "known_hosts")

	k, err := NewKnownHosts(path)
	if err != nil {
		t.Fatalf("NewKnownHosts() error: %v", err)
	}
	if !filepath.IsAbs(k.Path()) {
		t.Errorf("Path() = %q, se esperaba una ruta absoluta", k.Path())
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("no se creó el archivo: %v", err)
	}
}

func TestKnownHostsContains(t *testing.T) {
	k := newTestKnownHosts(t, sampleKnownHosts)

	tests := []struct {
		host string
		want bool
	}{
		{"192.168.10.10", true},
		{"192.168.10.30", true},
		{"192.168.10.31", true},
		{"192.168.10.3", false},
		{"192.168.10.99", false},
	}
	for _, tt := range tests {
		got, err := k.Contains(tt.host)
		if err != nil {
			t.Fatalf("Contains(%s) error: %v", tt.host, err)
		}
		if got != tt.want {
			t.Errorf("Contains(%s) = %v, se esperaba %v", tt.host, got, tt.want)
		}
	}
}

func TestKnownHostsForgetRemovesOnlyThatIP(t *testing.T) {
	k := newTestKnownHosts(t, sampleKnownHosts)

	if err := k.Forget(context.Background(), netip.MustParseAddr("192.168.10.20")); err != nil {
		t.Fatalf("Forget() error: %v", err)
	}

	data, err := os.ReadFile(k.Path())
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	if strings.Contains(content, "temporal") {
		t.Errorf("quedaron llaves de 192.168.10.20:\n%s", content)
	}
	for _, keep := range []string{"AAAAIns", "AAAAIpuerto", "AAAAIvarios", "# comentario"} {
		if !strings.Contains(content, keep) {
			t.Errorf("se borró una línea que no correspondía (%s):\n%s", keep, content)
		}
	}
}

func TestKnownHostsForgetWithPort(t *testing.T) {
	k := newTestKnownHosts(t, sampleKnownHosts)

	if err := k.Forget(context.Background(), netip.MustParseAddr("192.168.10.30")); err != nil {
		t.Fatalf("Forget() error: %v", err)
	}
	if ok, _ := k.Contains("192.168.10.30"); ok {
		t.Error("la llave de [192.168.10.30]:2222 sigue registrada")
	}
}

func TestKnownHostsForgetUnknownIPLeavesFileUntouched(t *testing.T) {
	k := newTestKnownHosts(t, sampleKnownHosts)

	if err := k.Forget(context.Background(), netip.MustParseAddr("192.168.10.99")); err != nil {
		t.Fatalf("Forget() error: %v", err)
	}
	data, _ := os.ReadFile(k.Path())
	if string(data) != sampleKnownHosts {
		t.Errorf("el archivo cambió:\n%s", data)
	}
}
