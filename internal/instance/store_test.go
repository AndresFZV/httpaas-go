package instance

import (
	"errors"
	"net/netip"
	"path/filepath"
	"testing"
	"time"
)

func newTestStore(t *testing.T, path, first, last string) *Store {
	t.Helper()

	s, err := NewStore(path, netip.MustParseAddr(first), netip.MustParseAddr(last))
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}
	return s
}

func mustReserve(t *testing.T, s *Store, hostname string, createdAt time.Time) Instance {
	t.Helper()

	inst, err := s.Reserve(hostname, createdAt)
	if err != nil {
		t.Fatalf("Reserve(%q) error: %v", hostname, err)
	}
	return inst
}

func tempPath(t *testing.T) string {
	return filepath.Join(t.TempDir(), "instances.json")
}

func TestReserveAssignsConsecutiveIPs(t *testing.T) {
	s := newTestStore(t, tempPath(t), "192.168.10.30", "192.168.10.254")
	now := time.Now()

	paisajes := mustReserve(t, s, "paisajes", now)
	negocios := mustReserve(t, s, "negocios", now.Add(time.Minute))

	if got, want := paisajes.IP.String(), "192.168.10.30"; got != want {
		t.Errorf("IP de paisajes = %s, se esperaba %s", got, want)
	}
	if got, want := negocios.IP.String(), "192.168.10.31"; got != want {
		t.Errorf("IP de negocios = %s, se esperaba %s", got, want)
	}
}

func TestReserveRejectsDuplicateHostname(t *testing.T) {
	s := newTestStore(t, tempPath(t), "192.168.10.30", "192.168.10.254")
	mustReserve(t, s, "paisajes", time.Now())

	_, err := s.Reserve("paisajes", time.Now())
	if !errors.Is(err, ErrHostnameTaken) {
		t.Errorf("Reserve() duplicado = %v, se esperaba %v", err, ErrHostnameTaken)
	}
}

func TestReserveFailsWhenRangeIsFull(t *testing.T) {
	s := newTestStore(t, tempPath(t), "192.168.10.30", "192.168.10.31")
	mustReserve(t, s, "uno", time.Now())
	mustReserve(t, s, "dos", time.Now())

	_, err := s.Reserve("tres", time.Now())
	if !errors.Is(err, ErrNoFreeIP) {
		t.Errorf("Reserve() con rango lleno = %v, se esperaba %v", err, ErrNoFreeIP)
	}
}

func TestRemoveFreesIPForReuse(t *testing.T) {
	s := newTestStore(t, tempPath(t), "192.168.10.30", "192.168.10.254")
	now := time.Now()
	mustReserve(t, s, "paisajes", now)
	mustReserve(t, s, "negocios", now)

	if err := s.Remove("paisajes"); err != nil {
		t.Fatalf("Remove() error: %v", err)
	}

	nueva := mustReserve(t, s, "nueva", now)
	if got, want := nueva.IP.String(), "192.168.10.30"; got != want {
		t.Errorf("IP reutilizada = %s, se esperaba %s", got, want)
	}
}

func TestRemoveUnknownHostname(t *testing.T) {
	s := newTestStore(t, tempPath(t), "192.168.10.30", "192.168.10.254")

	if err := s.Remove("inexistente"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Remove() = %v, se esperaba %v", err, ErrNotFound)
	}
}

func TestListIsOrderedByCreation(t *testing.T) {
	s := newTestStore(t, tempPath(t), "192.168.10.30", "192.168.10.254")
	now := time.Now()
	mustReserve(t, s, "negocios", now.Add(time.Minute))
	mustReserve(t, s, "paisajes", now)

	list := s.List()
	if len(list) != 2 || list[0].Hostname != "paisajes" || list[1].Hostname != "negocios" {
		t.Errorf("List() = %+v, se esperaba paisajes y luego negocios", list)
	}
}

func TestStorePersistsBetweenLoads(t *testing.T) {
	path := tempPath(t)
	first := newTestStore(t, path, "192.168.10.30", "192.168.10.254")
	mustReserve(t, first, "paisajes", time.Now())

	second := newTestStore(t, path, "192.168.10.30", "192.168.10.254")
	inst, err := second.Get("paisajes")
	if err != nil {
		t.Fatalf("Get() después de recargar: %v", err)
	}
	if got, want := inst.IP.String(), "192.168.10.30"; got != want {
		t.Errorf("IP recargada = %s, se esperaba %s", got, want)
	}
}

func TestNewStoreRejectsInvalidRange(t *testing.T) {
	_, err := NewStore(tempPath(t), netip.MustParseAddr("192.168.10.50"), netip.MustParseAddr("192.168.10.30"))
	if err == nil {
		t.Error("NewStore() con rango invertido no devolvió error")
	}
}
