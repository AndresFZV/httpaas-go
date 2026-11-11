package deployment

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net/netip"
	"os"
	"sync"
	"time"

	"github.com/AndresFZV/httpaas-go/internal/instance"
	"github.com/AndresFZV/httpaas-go/internal/provision"
)

const runTimeout = 10 * time.Minute

var (
	ErrNotFound       = errors.New("el despliegue no existe")
	ErrNotRetryable   = errors.New("solo se puede reintentar un despliegue que falló")
	ErrAlreadyRetried = errors.New("este despliegue ya se reintentó")
)

// errorsForUser son errores cuyo texto se le muestra tal cual al usuario.
var errorsForUser = []error{
	instance.ErrHostnameTaken,
	instance.ErrNoFreeIP,
	instance.ErrHostnameEmpty,
	instance.ErrHostnameInvalid,
	instance.ErrHostnameReserved,
}

// Provisioner crea la instancia (VM, red y DNS) e informa cada etapa.
type Provisioner interface {
	Provision(ctx context.Context, hostname string, report provision.ReportFunc) (instance.Instance, error)
}

// Deployer publica el contenido de un .zip en una instancia.
type Deployer interface {
	Deploy(ctx context.Context, host string, archive io.Reader) error
}

// Verifier comprueba que la instancia responda por HTTP.
type Verifier interface {
	Verify(ctx context.Context, ip netip.Addr) error
}

// Status es el estado general de un despliegue.
type Status string

const (
	StatusRunning   Status = "running"
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
)

// Snapshot es una copia del estado de un despliegue en un momento dado.
type Snapshot struct {
	ID          string `json:"id"`
	Hostname    string `json:"hostname"`
	FQDN        string `json:"fqdn"`
	URL         string `json:"url"`
	IP          string `json:"ip"`
	Status      Status `json:"status"`
	Step        int    `json:"step"`
	FailedStep  int    `json:"failedStep"`
	Error       string `json:"error"`
	Component   string `json:"component"`
	HasContent  bool   `json:"hasContent"`
	ArchiveName string `json:"archiveName"`
	Percent     int    `json:"percent"`
}

type job struct {
	id          string
	hostname    string
	archivePath string // vacío si no hay contenido o si ya se borró
	archiveName string
	hasContent  bool
	status      Status
	step        int
	failedStep  int
	errMsg      string
	ip          netip.Addr
}

// Manager ejecuta despliegues en segundo plano y guarda su estado en memoria.
type Manager struct {
	mu          sync.Mutex
	jobs        map[string]*job
	uploadsDir  string
	provisioner Provisioner
	deployer    Deployer
	verifier    Verifier
}

// NewManager crea un Manager que guarda los .zip pendientes en uploadsDir.
func NewManager(uploadsDir string, provisioner Provisioner, deployer Deployer, verifier Verifier) (*Manager, error) {
	if err := os.MkdirAll(uploadsDir, 0o755); err != nil {
		return nil, fmt.Errorf("crear directorio de archivos subidos: %w", err)
	}
	return &Manager{
		jobs:        make(map[string]*job),
		uploadsDir:  uploadsDir,
		provisioner: provisioner,
		deployer:    deployer,
		verifier:    verifier,
	}, nil
}

// Start guarda el .zip (archive puede ser nil si no hay contenido) y lanza el despliegue
// en segundo plano. Devuelve el identificador para consultar su estado.
func (m *Manager) Start(hostname string, archive io.Reader, archiveName string) (string, error) {
	j := &job{
		id:         newID(),
		hostname:   hostname,
		status:     StatusRunning,
		step:       StepValidate,
		failedStep: -1,
	}

	if archive != nil {
		path, err := m.saveArchive(archive)
		if err != nil {
			return "", err
		}
		j.archivePath = path
		j.archiveName = archiveName
		j.hasContent = true
	}

	m.mu.Lock()
	m.jobs[j.id] = j
	m.mu.Unlock()

	go m.run(j, StepValidate)
	return j.id, nil
}

// Retry lanza de nuevo un despliegue fallido con el mismo nombre y el mismo .zip.
// Si la instancia ya se había creado, solo repite la publicación y la verificación.
func (m *Manager) Retry(id string) (string, error) {
	m.mu.Lock()
	old, ok := m.jobs[id]
	if !ok {
		m.mu.Unlock()
		return "", ErrNotFound
	}
	if old.status != StatusFailed {
		m.mu.Unlock()
		return "", ErrNotRetryable
	}

	if old.hasContent && old.archivePath == "" {
		m.mu.Unlock()
		return "", ErrAlreadyRetried
	}

	from := StepValidate
	if old.failedStep >= StepPublish {
		from = StepPublish
	}

	j := &job{
		id:          newID(),
		hostname:    old.hostname,
		archivePath: old.archivePath,
		archiveName: old.archiveName,
		hasContent:  old.hasContent,
		status:      StatusRunning,
		step:        from,
		failedStep:  -1,
		ip:          old.ip,
	}
	old.archivePath = "" // el archivo pasa a ser del nuevo despliegue
	m.jobs[j.id] = j
	m.mu.Unlock()

	go m.run(j, from)
	return j.id, nil
}

// Snapshot devuelve el estado actual de un despliegue.
func (m *Manager) Snapshot(id string) (Snapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	j, ok := m.jobs[id]
	if !ok {
		return Snapshot{}, ErrNotFound
	}

	inst := instance.Instance{Hostname: j.hostname}
	snap := Snapshot{
		ID:          j.id,
		Hostname:    j.hostname,
		FQDN:        inst.FQDN(),
		URL:         inst.URL(),
		Status:      j.status,
		Step:        j.step,
		FailedStep:  j.failedStep,
		Error:       j.errMsg,
		HasContent:  j.hasContent,
		ArchiveName: j.archiveName,
		Percent:     j.step * 100 / len(Steps),
	}
	if j.ip.IsValid() {
		snap.IP = j.ip.String()
	}
	if j.status == StatusSucceeded {
		snap.Percent = 100
	}
	if j.status == StatusFailed {
		snap.Component = Steps[j.failedStep].Component
	}
	return snap, nil
}

// ActiveHostnames devuelve los hostnames que tienen un despliegue en curso.
func (m *Manager) ActiveHostnames() map[string]bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	active := make(map[string]bool)
	for _, j := range m.jobs {
		if j.status == StatusRunning {
			active[j.hostname] = true
		}
	}
	return active
}

// run ejecuta el despliegue desde la etapa from y registra el resultado.
func (m *Manager) run(j *job, from int) {
	ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
	defer cancel()

	if err := m.execute(ctx, j, from); err != nil {
		m.fail(j, err)
		return
	}
	m.succeed(j)
}

func (m *Manager) execute(ctx context.Context, j *job, from int) error {
	if from == StepValidate {
		report := func(stage provision.Stage, inst instance.Instance) {
			m.setStep(j, stepForStage(stage), inst.IP)
		}
		inst, err := m.provisioner.Provision(ctx, j.hostname, report)
		if err != nil {
			return err
		}
		m.setStep(j, StepPublish, inst.IP)
	}

	ip := m.ipOf(j)
	if j.hasContent {
		m.setStep(j, StepPublish, ip)
		if err := m.publish(ctx, j, ip); err != nil {
			return err
		}
	}

	m.setStep(j, StepVerify, ip)
	return m.verifier.Verify(ctx, ip)
}

func (m *Manager) publish(ctx context.Context, j *job, ip netip.Addr) error {
	file, err := os.Open(j.archivePath)
	if err != nil {
		return fmt.Errorf("abrir el archivo guardado: %w", err)
	}
	defer file.Close()

	return m.deployer.Deploy(ctx, ip.String(), file)
}

func (m *Manager) setStep(j *job, step int, ip netip.Addr) {
	m.mu.Lock()
	defer m.mu.Unlock()

	j.step = step
	if ip.IsValid() {
		j.ip = ip
	}
}

func (m *Manager) ipOf(j *job) netip.Addr {
	m.mu.Lock()
	defer m.mu.Unlock()
	return j.ip
}

func (m *Manager) fail(j *job, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	log.Printf("despliegue de %s falló en la etapa %q: %v", j.hostname, Steps[j.step].Label, err)
	j.status = StatusFailed
	j.failedStep = j.step
	j.errMsg = userMessage(j.step, err)
}

func (m *Manager) succeed(j *job) {
	m.mu.Lock()
	defer m.mu.Unlock()

	log.Printf("despliegue de %s completado", j.hostname)
	j.status = StatusSucceeded
	j.step = len(Steps)
	if j.archivePath != "" {
		if err := os.Remove(j.archivePath); err != nil {
			log.Printf("borrar archivo de %s: %v", j.hostname, err)
		}
		j.archivePath = ""
	}
}

// saveArchive copia el .zip a un archivo propio, porque el del formulario se borra al terminar la petición.
func (m *Manager) saveArchive(archive io.Reader) (string, error) {
	file, err := os.CreateTemp(m.uploadsDir, "deploy-*.zip")
	if err != nil {
		return "", fmt.Errorf("crear archivo temporal: %w", err)
	}

	if _, err := io.Copy(file, archive); err != nil {
		file.Close()
		os.Remove(file.Name())
		return "", fmt.Errorf("guardar el archivo: %w", err)
	}
	if err := file.Close(); err != nil {
		os.Remove(file.Name())
		return "", fmt.Errorf("guardar el archivo: %w", err)
	}
	return file.Name(), nil
}

// userMessage devuelve el texto del error tal cual si es para el usuario, o el mensaje de la etapa si es técnico.
func userMessage(step int, err error) string {
	for _, target := range errorsForUser {
		if errors.Is(err, target) {
			return err.Error()
		}
	}
	return Steps[step].FailMessage
}

func stepForStage(stage provision.Stage) int {
	switch stage {
	case provision.StageCreateVM:
		return StepCreateVM
	case provision.StageWaitBoot:
		return StepWaitBoot
	case provision.StageConfigure:
		return StepConfigure
	case provision.StageWaitFinalIP:
		return StepWaitFinalIP
	case provision.StageRegisterDNS:
		return StepRegisterDNS
	default:
		return StepValidate
	}
}

// newID genera un identificador aleatorio de 16 caracteres hexadecimales.
func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
