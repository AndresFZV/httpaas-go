package remote

import (
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"
	"time"
)

// fakeRunner registra la última llamada y falla las primeras failTimes veces.
type fakeRunner struct {
	name      string
	args      []string
	stdin     string
	calls     int
	failTimes int
}

func (f *fakeRunner) run(_ context.Context, name string, stdin io.Reader, args ...string) ([]byte, error) {
	f.calls++
	f.name = name
	f.args = args
	if stdin != nil {
		data, _ := io.ReadAll(stdin)
		f.stdin = string(data)
	}
	if f.calls <= f.failTimes {
		return []byte("Connection refused"), errors.New("código de salida 255")
	}
	return nil, nil
}

func newTestClient(f *fakeRunner) *Client {
	return &Client{
		cfg: Config{
			SSHPath:        "ssh",
			User:           "deploy",
			KeyFile:        "llave",
			ConnectTimeout: 5 * time.Second,
		},
		run: f.run,
	}
}

func TestRunSendsScriptThroughStdin(t *testing.T) {
	f := &fakeRunner{}
	c := newTestClient(f)

	if err := c.Run(context.Background(), "192.168.10.30", "hostname"); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	if f.name != "ssh" {
		t.Errorf("programa = %q, se esperaba ssh", f.name)
	}
	if !slices.Contains(f.args, "deploy@192.168.10.30") || !slices.Contains(f.args, "BatchMode=yes") {
		t.Errorf("argumentos incompletos: %v", f.args)
	}
	if f.stdin != "hostname" {
		t.Errorf("stdin = %q, se esperaba el script", f.stdin)
	}
}

func TestUploadStreamsContentThroughSSH(t *testing.T) {
	f := &fakeRunner{}
	c := newTestClient(f)

	err := c.Upload(context.Background(), "192.168.10.30", strings.NewReader("contenido zip"), "/tmp/sitio.zip")
	if err != nil {
		t.Fatalf("Upload() error: %v", err)
	}

	if f.name != "ssh" {
		t.Errorf("programa = %q, se esperaba ssh", f.name)
	}
	if !slices.Contains(f.args, "cat > '/tmp/sitio.zip'") {
		t.Errorf("falta el comando remoto en %v", f.args)
	}
	if f.stdin != "contenido zip" {
		t.Errorf("stdin = %q, se esperaba el contenido del archivo", f.stdin)
	}
}
func TestWaitForSSHRetriesUntilSuccess(t *testing.T) {
	f := &fakeRunner{failTimes: 2}
	c := newTestClient(f)

	if err := c.WaitForSSH(context.Background(), "192.168.10.20", time.Millisecond); err != nil {
		t.Fatalf("WaitForSSH() error: %v", err)
	}
	if f.calls != 3 {
		t.Errorf("intentos = %d, se esperaban 3", f.calls)
	}
}

func TestWaitForSSHStopsWhenContextExpires(t *testing.T) {
	f := &fakeRunner{failTimes: 1000}
	c := newTestClient(f)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	err := c.WaitForSSH(ctx, "192.168.10.20", 5*time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("WaitForSSH() = %v, se esperaba %v", err, context.DeadlineExceeded)
	}
}
