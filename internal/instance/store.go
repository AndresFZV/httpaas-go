package instance

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"
)

var (
	ErrHostnameTaken = errors.New("ya existe una instancia con ese nombre del host")
	ErrNotFound      = errors.New("la instancia no existe")
	ErrNoFreeIP      = errors.New("no hay direcciones IP disponibles para nuevas instancias")
)

// Store guarda el registro de instancias en un archivo JSON. Es seguro para uso concurrente.
type Store struct {
	mu        sync.Mutex
	path      string
	firstIP   netip.Addr
	lastIP    netip.Addr
	instances map[string]Instance
}

// NewStore carga el registro desde path y asigna IPs dentro del rango [firstIP, lastIP].
// Si el archivo no existe, empieza con el registro vacío.
func NewStore(path string, firstIP, lastIP netip.Addr) (*Store, error) {
	if !firstIP.Is4() || !lastIP.Is4() || lastIP.Less(firstIP) {
		return nil, fmt.Errorf("rango de IP inválido: %s - %s", firstIP, lastIP)
	}

	s := &Store{
		path:      path,
		firstIP:   firstIP,
		lastIP:    lastIP,
		instances: make(map[string]Instance),
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

// List devuelve las instancias ordenadas por fecha de creación.
func (s *Store) List() []Instance {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sortedLocked()
}

// Get busca una instancia por su nombre del host.
func (s *Store) Get(hostname string) (Instance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	inst, ok := s.instances[hostname]
	if !ok {
		return Instance{}, ErrNotFound
	}
	return inst, nil
}

// Reserve registra una instancia nueva con la primera IP libre del rango.
// Si el aprovisionamiento falla después, se debe llamar a Remove para liberar el nombre y la IP.
func (s *Store) Reserve(hostname string, createdAt time.Time) (Instance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.instances[hostname]; exists {
		return Instance{}, ErrHostnameTaken
	}

	ip, err := s.nextFreeIPLocked()
	if err != nil {
		return Instance{}, err
	}

	inst := Instance{Hostname: hostname, IP: ip, CreatedAt: createdAt}
	s.instances[hostname] = inst
	if err := s.saveLocked(); err != nil {
		delete(s.instances, hostname)
		return Instance{}, err
	}
	return inst, nil
}

// Remove elimina una instancia del registro y libera su IP.
func (s *Store) Remove(hostname string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	inst, ok := s.instances[hostname]
	if !ok {
		return ErrNotFound
	}

	delete(s.instances, hostname)
	if err := s.saveLocked(); err != nil {
		s.instances[hostname] = inst
		return err
	}
	return nil
}

func (s *Store) load() error {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("leer registro de instancias: %w", err)
	}

	var list []Instance
	if err := json.Unmarshal(data, &list); err != nil {
		return fmt.Errorf("decodificar registro de instancias: %w", err)
	}
	for _, inst := range list {
		s.instances[inst.Hostname] = inst
	}
	return nil
}

// saveLocked escribe el registro en un archivo temporal y lo renombra sobre el original.
func (s *Store) saveLocked() error {
	data, err := json.MarshalIndent(s.sortedLocked(), "", "  ")
	if err != nil {
		return fmt.Errorf("codificar registro de instancias: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("crear directorio del registro: %w", err)
	}

	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("escribir registro de instancias: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("reemplazar registro de instancias: %w", err)
	}
	return nil
}

func (s *Store) sortedLocked() []Instance {
	list := make([]Instance, 0, len(s.instances))
	for _, inst := range s.instances {
		list = append(list, inst)
	}
	slices.SortFunc(list, func(a, b Instance) int {
		return a.CreatedAt.Compare(b.CreatedAt)
	})
	return list
}

func (s *Store) nextFreeIPLocked() (netip.Addr, error) {
	used := make(map[netip.Addr]bool, len(s.instances))
	for _, inst := range s.instances {
		used[inst.IP] = true
	}

	for ip := s.firstIP; ip.Compare(s.lastIP) <= 0; ip = ip.Next() {
		if !used[ip] {
			return ip, nil
		}
	}
	return netip.Addr{}, ErrNoFreeIP
}
