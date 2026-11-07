package remote

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Config son los parámetros de conexión SSH a las máquinas virtuales.
type Config struct {
	SSHPath        string // ejecutable ssh
	User           string // usuario remoto, por ejemplo "deploy"
	KeyFile        string // llave privada sin passphrase
	ConnectTimeout time.Duration
}

// runFunc ejecuta un programa con stdin y devuelve su salida combinada.
type runFunc func(ctx context.Context, name string, stdin io.Reader, args ...string) ([]byte, error)

// Client ejecuta comandos y copia archivos en máquinas remotas por SSH.
type Client struct {
	cfg Config
	run runFunc
}

// NewClient crea un Client que usa el ejecutable ssh del sistema.
func NewClient(cfg Config) *Client {
	return &Client{cfg: cfg, run: execCommand}
}

// Run ejecuta script con bash en host. El script se envía por stdin, así no hay que escaparlo.
func (c *Client) Run(ctx context.Context, host, script string) error {
	args := append(c.commonArgs(), c.target(host), "bash", "-s")
	out, err := c.run(ctx, c.cfg.SSHPath, strings.NewReader(script), args...)
	if err != nil {
		return fmt.Errorf("ssh %s: %w: %s", host, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Upload escribe el contenido de r en remotePath dentro de host.
// Se envía por stdin de ssh con cat en vez de usar scp, porque el scp de Git Bash
// interpreta las rutas locales de Windows (C:\...) como si C fuera un host remoto.
func (c *Client) Upload(ctx context.Context, host string, r io.Reader, remotePath string) error {
	args := append(c.commonArgs(), c.target(host), "cat > "+shellQuote(remotePath))
	out, err := c.run(ctx, c.cfg.SSHPath, r, args...)
	if err != nil {
		return fmt.Errorf("copiar a %s:%s: %w: %s", host, remotePath, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// WaitForSSH reintenta conectarse a host cada interval hasta lograrlo o hasta que ctx expire.
func (c *Client) WaitForSSH(ctx context.Context, host string, interval time.Duration) error {
	for {
		err := c.Run(ctx, host, "true")
		if err == nil {
			return nil
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("esperar SSH en %s: %w (último error: %v)", host, ctx.Err(), err)
		case <-time.After(interval):
		}
	}
}

func (c *Client) commonArgs() []string {
	return []string{
		"-i", c.cfg.KeyFile,
		"-o", "BatchMode=yes",
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=" + os.DevNull,
		"-o", "LogLevel=ERROR",
		"-o", fmt.Sprintf("ConnectTimeout=%d", int(c.cfg.ConnectTimeout.Seconds())),
	}
}

func (c *Client) target(host string) string {
	return c.cfg.User + "@" + host
}

// shellQuote encierra s en comillas simples para usarlo como un solo argumento en la shell remota.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func execCommand(ctx context.Context, name string, stdin io.Reader, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if stdin != nil {
		cmd.Stdin = stdin
	}
	out, err := cmd.CombinedOutput()

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return out, fmt.Errorf("código de salida %d", exitErr.ExitCode())
	}
	return out, err
}
