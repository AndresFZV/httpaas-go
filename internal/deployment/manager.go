package deployment

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/netip"
	"os"
	"sync"
	"time"

	"github.com/AndresFZV/httpaas-go/internal/instance"
	"github.com/AndresFZV/httpaas-go/internal/provision"
)

const runTimeout = 10 * time.Minute

// component identifica en el log los eventos de este paquete.
const component = "despliegue"

// Tipos de despliegue, para el log y las métricas.
const (
	KindCreate  = "creacion"    // crea la instancia y, si hay .zip, lo publica
	KindPublish = "publicacion" // publica un .zip en una instancia existente
)

// Resultados de un despliegue, para el log y las métricas.
const (
	ResultSuccess = "exito"
	ResultFailure = "error"
)

var (
	ErrNotFound       = errors.New("el despliegue no existe")
	ErrNotRetryable   = errors.New("solo se puede reintentar un despliegue que falló")
	ErrAlreadyRetried = errors.New("este despliegue ya se reintentó")
	ErrBusy           = errors.New("la instancia tiene un despliegue en curso; espera a que termine")
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

// Observer recibe las mediciones de los despliegues, por ejemplo para exponerlas como métricas.
type Observer interface {
	// StepCompleted se llama al terminar con éxito una etapa, con su identificador (Step.Key).
	StepCompleted(step string, d time.Duration)
	// DeploymentFinished se llama al terminar un despliegue, con su tipo (Kind*) y resultado (Result*).
	DeploymentFinished(kind, result string, d time.Duration)
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
	Republish   bool   `json:"republish"`
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
	republish   bool // publicación en una instancia existente
	startedAt   time.Time
	stepStarted time.Time
}

// kind devuelve el tipo de despliegue para el log y las métricas.
func (j *job) kind() string {
	if j.republish {
		return KindPublish
	}
	return KindCreate
}

// logger devuelve un logger con los datos que identifican el despliegue,
// para que todas sus líneas se puedan filtrar por despliegue_id.
func (j *job) logger() *slog.Logger {
	return slog.With("componente", component, "despliegue_id", j.id, "hostname", j.hostname, "tipo", j.kind())
}

// Manager ejecuta despliegues en segundo plano y guarda su estado en memoria.
type Manager struct {
	mu          sync.Mutex
	jobs        map[string]*job
	uploadsDir  string
	provisioner Provisioner
	deployer    Deployer
	verifier    Verifier
	onFinished  func(hostname string)
	observer    Observer
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

// Publish lanza en segundo plano la publicación de un .zip en una instancia que ya existe.
// Empieza en la etapa de publicación, porque las anteriores ya se hicieron al crear la instancia.
func (m *Manager) Publish(hostname string, ip netip.Addr, archive io.Reader, archiveName string) (string, error) {
	path, err := m.saveArchive(archive)
	if err != nil {
		return "", err
	}

	m.mu.Lock()
	for _, existing := range m.jobs {
		if existing.hostname == hostname && existing.status == StatusRunning {
			m.mu.Unlock()
			os.Remove(path)
			return "", ErrBusy
		}
	}

	j := &job{
		id:          newID(),
		hostname:    hostname,
		archivePath: path,
		archiveName: archiveName,
		hasContent:  true,
		republish:   true,
		status:      StatusRunning,
		step:        StepPublish,
		failedStep:  -1,
		ip:          ip,
	}
	m.jobs[j.id] = j
	m.mu.Unlock()

	go m.run(j, StepPublish)
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
		republish:   old.republish,
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
		Republish:   j.republish,
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

// OnFinished registra una función que se llama cada vez que un despliegue termina, con éxito o con error.
// Debe registrarse antes de iniciar despliegues.
func (m *Manager) OnFinished(fn func(hostname string)) {
	m.onFinished = fn
}

// SetObserver registra quién recibe las mediciones de los despliegues. Debe registrarse antes de iniciarlos.
func (m *Manager) SetObserver(o Observer) {
	m.observer = o
}

// run ejecuta el despliegue desde la etapa from, registra el resultado y avisa que terminó.
func (m *Manager) run(j *job, from int) {
	ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
	defer cancel()

	m.mu.Lock()
	j.startedAt = time.Now()
	j.stepStarted = j.startedAt
	m.mu.Unlock()
	j.logger().Info("despliegue iniciado", "desde", Steps[from].Key, "con_contenido", j.hasContent)

	if err := m.execute(ctx, j, from); err != nil {
		m.fail(j, err)
	} else {
		m.succeed(j)
	}

	if m.onFinished != nil {
		m.onFinished(j.hostname)
	}
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

// setStep avanza el despliegue a step y registra la duración de la etapa que terminó.
func (m *Manager) setStep(j *job, step int, ip netip.Addr) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if step != j.step {
		m.completeStep(j)
		j.step = step
	}
	if ip.IsValid() {
		j.ip = ip
	}
}

// completeStep registra la duración de la etapa actual de j y empieza a medir la siguiente.
// La publicación de un despliegue sin .zip se omite, así que no se mide. Debe llamarse con m.mu tomado.
func (m *Manager) completeStep(j *job) {
	now := time.Now()
	elapsed := now.Sub(j.stepStarted)
	j.stepStarted = now

	if j.step == StepPublish && !j.hasContent {
		return
	}
	key := Steps[j.step].Key
	j.logger().Info("etapa completada", "etapa", key, "duracion_ms", elapsed.Milliseconds())
	if m.observer != nil {
		m.observer.StepCompleted(key, elapsed)
	}
}

// finish registra la duración total del despliegue. Debe llamarse con m.mu tomado.
func (m *Manager) finish(j *job, result string) time.Duration {
	elapsed := time.Since(j.startedAt)
	if m.observer != nil {
		m.observer.DeploymentFinished(j.kind(), result, elapsed)
	}
	return elapsed
}

func (m *Manager) ipOf(j *job) netip.Addr {
	m.mu.Lock()
	defer m.mu.Unlock()
	return j.ip
}

func (m *Manager) fail(j *job, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	elapsed := m.finish(j, ResultFailure)
	j.logger().Error("despliegue fallido", "etapa", Steps[j.step].Key, "duracion_s", elapsed.Round(time.Millisecond).Seconds(), "error", err)
	j.status = StatusFailed
	j.failedStep = j.step
	j.errMsg = userMessage(j.step, err)
}

func (m *Manager) succeed(j *job) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.completeStep(j)
	elapsed := m.finish(j, ResultSuccess)
	j.logger().Info("despliegue completado", "duracion_s", elapsed.Round(time.Millisecond).Seconds())
	j.status = StatusSucceeded
	j.step = len(Steps)
	if j.archivePath != "" {
		if err := os.Remove(j.archivePath); err != nil {
			j.logger().Warn("no se pudo borrar el archivo subido", "error", err)
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
