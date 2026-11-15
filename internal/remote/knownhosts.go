package remote

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// KnownHosts administra el archivo known_hosts propio de la aplicación, donde ssh guarda
// la llave pública de cada máquina con la que se conecta y la compara en las conexiones siguientes.
type KnownHosts struct {
	mu   sync.Mutex
	path string
}

// NewKnownHosts prepara el archivo known_hosts en path y lo crea vacío si no existe.
// La ruta se guarda absoluta, porque ssh no la resuelve respecto a la carpeta de la aplicación.
func NewKnownHosts(path string) (*KnownHosts, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("ruta de known_hosts: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return nil, fmt.Errorf("crear la carpeta de known_hosts: %w", err)
	}
	f, err := os.OpenFile(abs, os.O_RDONLY|os.O_CREATE, 0o644)
	if err != nil {
		return nil, fmt.Errorf("crear known_hosts: %w", err)
	}
	f.Close()
	return &KnownHosts{path: abs}, nil
}

// Path devuelve la ruta absoluta del archivo.
func (k *KnownHosts) Path() string {
	return k.path
}

// Contains indica si el archivo tiene una llave registrada para host.
func (k *KnownHosts) Contains(host string) (bool, error) {
	k.mu.Lock()
	defer k.mu.Unlock()

	data, err := os.ReadFile(k.path)
	if err != nil {
		return false, fmt.Errorf("leer known_hosts: %w", err)
	}

	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		if lineMatchesHost(scanner.Text(), host) {
			return true, nil
		}
	}
	return false, scanner.Err()
}

// Forget borra las llaves registradas para ip. Se usa cuando la IP pasa a otra VM
// (la IP temporal en cada creación, o la IP de una instancia eliminada que se reasigna),
// para que la conexión con la VM nueva no falle por una llave anterior.
// Tiene la misma firma que la limpieza de la caché ARP, porque se hace en el mismo momento.
func (k *KnownHosts) Forget(_ context.Context, ip netip.Addr) error {
	k.mu.Lock()
	defer k.mu.Unlock()

	data, err := os.ReadFile(k.path)
	if err != nil {
		return fmt.Errorf("leer known_hosts: %w", err)
	}

	var kept bytes.Buffer
	removed := false
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := scanner.Text()
		if lineMatchesHost(line, ip.String()) {
			removed = true
			continue
		}
		kept.WriteString(line)
		kept.WriteByte('\n')
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("leer known_hosts: %w", err)
	}
	if !removed {
		return nil
	}

	if err := os.WriteFile(k.path, kept.Bytes(), 0o644); err != nil {
		return fmt.Errorf("actualizar known_hosts: %w", err)
	}
	return nil
}

// lineMatchesHost indica si una línea de known_hosts corresponde a host.
// El primer campo tiene uno o varios nombres separados por comas, con la forma
// "192.168.10.30" o "[192.168.10.30]:2222". Las líneas con nombres cifrados (|1|...)
// no se reconocen; por eso el cliente usa HashKnownHosts=no.
func lineMatchesHost(line, host string) bool {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return false
	}
	// Las líneas marcadas con @cert-authority o @revoked tienen el nombre en el segundo campo.
	fields := strings.Fields(line)
	if strings.HasPrefix(fields[0], "@") {
		fields = fields[1:]
	}
	if len(fields) == 0 {
		return false
	}

	for _, name := range strings.Split(fields[0], ",") {
		if name == host || strings.HasPrefix(name, "["+host+"]:") {
			return true
		}
	}
	return false
}
