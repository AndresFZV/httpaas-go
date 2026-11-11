package deployment

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"time"
)

// HTTPVerifier comprueba que Apache responda en la IP de una instancia.
type HTTPVerifier struct {
	client   *http.Client
	attempts int
	wait     time.Duration
}

// NewHTTPVerifier crea un verificador con 5 intentos separados por 2 segundos.
func NewHTTPVerifier() *HTTPVerifier {
	return &HTTPVerifier{
		client:   &http.Client{Timeout: 5 * time.Second},
		attempts: 5,
		wait:     2 * time.Second,
	}
}

// Verify hace GET a http://ip/ hasta obtener una respuesta exitosa o agotar los intentos.
func (v *HTTPVerifier) Verify(ctx context.Context, ip netip.Addr) error {
	url := "http://" + ip.String() + "/"

	var lastErr error
	for attempt := 1; attempt <= v.attempts; attempt++ {
		if lastErr = v.check(ctx, url); lastErr == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("verificar %s: %w", url, ctx.Err())
		case <-time.After(v.wait):
		}
	}
	return fmt.Errorf("verificar %s: %w", url, lastErr)
}

func (v *HTTPVerifier) check(ctx context.Context, url string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}

	resp, err := v.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))

	if resp.StatusCode >= http.StatusBadRequest {
		return fmt.Errorf("respuesta HTTP %d", resp.StatusCode)
	}
	return nil
}
