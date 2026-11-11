package hostnet

import (
	"context"
	"errors"
	"net/netip"
	"slices"
	"testing"
)

func TestForgetRunsArpDelete(t *testing.T) {
	var got []string
	c := &ARPCache{run: func(_ context.Context, args ...string) ([]byte, error) {
		got = args
		return nil, nil
	}}

	if err := c.Forget(context.Background(), netip.MustParseAddr("192.168.10.20")); err != nil {
		t.Fatalf("Forget() error: %v", err)
	}
	if want := []string{"-d", "192.168.10.20"}; !slices.Equal(got, want) {
		t.Errorf("argumentos = %v, se esperaba %v", got, want)
	}
}

func TestForgetReturnsError(t *testing.T) {
	c := &ARPCache{run: func(_ context.Context, _ ...string) ([]byte, error) {
		return []byte("se requiere elevación"), errors.New("exit status 1")
	}}

	if err := c.Forget(context.Background(), netip.MustParseAddr("192.168.10.20")); err == nil {
		t.Fatal("Forget() no devolvió error")
	}
}
