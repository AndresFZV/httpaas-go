package vbox

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const (
	osType            = "Debian_64"
	storageController = "SATA"
	unregisterRetries = 5
	unregisterWait    = time.Second
)

// Config son los parámetros de VirtualBox para crear las instancias.
type Config struct {
	VBoxManagePath string // ruta del ejecutable VBoxManage
	TemplateDisk   string // ruta del disco multiconexión ApacheServer.vdi
	HostOnlyIF     string // adaptador host-only de la red 192.168.10.0/24
	MemoryMB       int
	CPUs           int
}

// runFunc ejecuta VBoxManage con args y devuelve su salida combinada.
type runFunc func(ctx context.Context, args ...string) ([]byte, error)

// Manager crea y elimina máquinas virtuales con VBoxManage.
type Manager struct {
	cfg   Config
	run   runFunc
	sleep func(time.Duration)
}

// NewManager crea un Manager que ejecuta VBoxManage en este equipo.
func NewManager(cfg Config) *Manager {
	m := &Manager{cfg: cfg, sleep: time.Sleep}
	m.run = m.execVBoxManage
	return m
}

// CreateVM crea una VM con el disco multiconexión y la red host-only, y la inicia sin interfaz gráfica.
// Si falla después de registrarla, intenta borrarla para no dejar VMs a medio crear.
func (m *Manager) CreateVM(ctx context.Context, name string) error {
	steps := [][]string{
		{"createvm", "--name", name, "--ostype", osType, "--register"},
		{"modifyvm", name,
			"--memory", strconv.Itoa(m.cfg.MemoryMB),
			"--cpus", strconv.Itoa(m.cfg.CPUs),
			"--nic1", "hostonly",
			"--hostonlyadapter1", m.cfg.HostOnlyIF},
		{"storagectl", name, "--name", storageController, "--add", "sata", "--controller", "IntelAhci"},
		{"storageattach", name, "--storagectl", storageController,
			"--port", "0", "--device", "0", "--type", "hdd", "--medium", m.cfg.TemplateDisk},
		{"startvm", name, "--type", "headless"},
	}

	for i, args := range steps {
		if err := m.exec(ctx, args...); err != nil {
			if i > 0 {
				// Se usa un contexto sin cancelación para limpiar aunque la petición original expire.
				if cleanupErr := m.DeleteVM(context.WithoutCancel(ctx), name); cleanupErr != nil {
					log.Printf("limpiar VM %s después del error: %v", name, cleanupErr)
				}
			}
			return fmt.Errorf("crear VM %s: %w", name, err)
		}
	}
	return nil
}

// DeleteVM apaga la VM si está encendida y la borra junto con su disco diferencial.
func (m *Manager) DeleteVM(ctx context.Context, name string) error {
	// Falla si la VM ya está apagada; en ese caso se continúa con el borrado.
	_ = m.exec(ctx, "controlvm", name, "poweroff")

	// Tras apagarla, VirtualBox tarda un momento en liberar el bloqueo de la VM.
	var err error
	for attempt := 1; attempt <= unregisterRetries; attempt++ {
		if err = m.exec(ctx, "unregistervm", name, "--delete"); err == nil {
			return nil
		}
		m.sleep(unregisterWait)
	}
	return fmt.Errorf("borrar VM %s: %w", name, err)
}

func (m *Manager) exec(ctx context.Context, args ...string) error {
	out, err := m.run(ctx, args...)
	if err != nil {
		return fmt.Errorf("VBoxManage %s: %w: %s", args[0], err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (m *Manager) execVBoxManage(ctx context.Context, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, m.cfg.VBoxManagePath, args...).CombinedOutput()
}
