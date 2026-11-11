// Package power enciende, apaga y reinicia máquinas virtuales y recuerda las acciones en curso.
package power

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"
)

// Action es una acción sobre el encendido de una VM.
type Action string

const (
	ActionStart   Action = "start"
	ActionStop    Action = "stop"
	ActionRestart Action = "restart"
)

var (
	ErrInProgress    = errors.New("ya hay una acción en curso para esta máquina virtual; espera a que termine")
	ErrUnknownAction = errors.New("acción no válida")
)

// ParseAction convierte el texto recibido en la URL en una acción.
func ParseAction(s string) (Action, error) {
	switch action := Action(s); action {
	case ActionStart, ActionStop, ActionRestart:
		return action, nil
	default:
		return "", fmt.Errorf("%w: %q", ErrUnknownAction, s)
	}
}

// VMs ejecuta las operaciones de encendido en VirtualBox.
type VMs interface {
	StartVM(ctx context.Context, name string) error
	ShutdownVM(ctx context.Context, name string) error
	PowerOffVM(ctx context.Context, name string) error
	ResetVM(ctx context.Context, name string) error
	RunningVMs(ctx context.Context) (map[string]bool, error)
}

// Pending es la última acción sobre una VM y cuándo empezó.
type Pending struct {
	Action    Action
	StartedAt time.Time
}

const (
	commandTimeout = 30 * time.Second
	bootWindow     = 3 * time.Minute // tiempo en que una VM encendida o reiniciada se considera arrancando
)

// Controller ejecuta acciones de encendido, una a la vez por VM.
type Controller struct {
	vms             VMs
	onChange        func()
	shutdownTimeout time.Duration
	pollInterval    time.Duration

	mu     sync.Mutex
	busy   map[string]bool    // VMs con un comando ejecutándose
	recent map[string]Pending // última acción de cada VM, para mostrar la transición
}

// NewController crea un controlador que espera hasta 60 segundos un apagado ordenado.
func NewController(vms VMs) *Controller {
	return &Controller{
		vms:             vms,
		shutdownTimeout: 60 * time.Second,
		pollInterval:    2 * time.Second,
		busy:            make(map[string]bool),
		recent:          make(map[string]Pending),
	}
}

// OnChange registra una función que se llama cuando una acción termina. Debe registrarse al iniciar.
func (c *Controller) OnChange(fn func()) {
	c.onChange = fn
}

// Do ejecuta action sobre la VM name.
// Encender y reiniciar tardan segundos: se ejecutan ya y devuelven el error si lo hay.
// Apagar corre en segundo plano, porque espera a que el sistema operativo termine de apagarse.
func (c *Controller) Do(ctx context.Context, name string, action Action) error {
	if !c.reserve(name) {
		return ErrInProgress
	}

	switch action {
	case ActionStart, ActionRestart:
		err := c.run(ctx, name, action)
		c.finish(name, action, err == nil)
		return err
	case ActionStop:
		c.remember(name, ActionStop)
		go c.stop(name)
		return nil
	default:
		c.release(name)
		return ErrUnknownAction
	}
}

// Pending devuelve las acciones que la interfaz debe mostrar como transición:
// los apagados en curso y los encendidos o reinicios de los últimos 3 minutos.
func (c *Controller) Pending() map[string]Pending {
	c.mu.Lock()
	defer c.mu.Unlock()

	pending := make(map[string]Pending)
	for name, p := range c.recent {
		if p.Action == ActionStop || time.Since(p.StartedAt) < bootWindow {
			pending[name] = p
		}
	}
	return pending
}

func (c *Controller) run(ctx context.Context, name string, action Action) error {
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()

	if action == ActionStart {
		return c.vms.StartVM(ctx, name)
	}
	return c.vms.ResetVM(ctx, name)
}

// stop pide un apagado ordenado y, si la VM no se apaga a tiempo, lo fuerza.
func (c *Controller) stop(name string) {
	ctx, cancel := context.WithTimeout(context.Background(), c.shutdownTimeout+commandTimeout)
	defer cancel()

	if err := c.vms.ShutdownVM(ctx, name); err != nil {
		log.Printf("apagado ordenado de %s: %v", name, err)
	}
	if !c.waitStopped(ctx, name) {
		log.Printf("%s no se apagó en %s; se fuerza el apagado", name, c.shutdownTimeout)
		if err := c.vms.PowerOffVM(ctx, name); err != nil {
			log.Printf("forzar apagado de %s: %v", name, err)
		}
	}

	c.mu.Lock()
	delete(c.recent, name)
	c.mu.Unlock()
	c.release(name)
	c.notify()
}

// waitStopped consulta a VirtualBox hasta que la VM deja de estar encendida o se agota el tiempo.
func (c *Controller) waitStopped(ctx context.Context, name string) bool {
	deadline := time.Now().Add(c.shutdownTimeout)
	for time.Now().Before(deadline) {
		running, err := c.vms.RunningVMs(ctx)
		if err == nil && !running[name] {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(c.pollInterval):
		}
	}
	return false
}

// reserve marca la VM como ocupada; devuelve false si ya lo estaba.
func (c *Controller) reserve(name string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.busy[name] {
		return false
	}
	c.busy[name] = true
	return true
}

func (c *Controller) release(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.busy, name)
}

func (c *Controller) remember(name string, action Action) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.recent[name] = Pending{Action: action, StartedAt: time.Now()}
}

// finish libera la VM y, si la acción salió bien, la registra como reciente.
func (c *Controller) finish(name string, action Action, ok bool) {
	if ok {
		c.remember(name, action)
	}
	c.release(name)
	if ok {
		c.notify()
	}
}

func (c *Controller) notify() {
	if c.onChange != nil {
		go c.onChange()
	}
}
