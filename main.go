package main

import (
	"context"
	"log"
	"net/http"
	"net/netip"
	"path/filepath"
	"time"

	"github.com/AndresFZV/httpaas-go/internal/deploy"
	"github.com/AndresFZV/httpaas-go/internal/deployment"
	"github.com/AndresFZV/httpaas-go/internal/dns"
	"github.com/AndresFZV/httpaas-go/internal/health"
	"github.com/AndresFZV/httpaas-go/internal/hostnet"
	"github.com/AndresFZV/httpaas-go/internal/instance"
	"github.com/AndresFZV/httpaas-go/internal/power"
	"github.com/AndresFZV/httpaas-go/internal/provision"
	"github.com/AndresFZV/httpaas-go/internal/remote"
	"github.com/AndresFZV/httpaas-go/internal/vbox"
	"github.com/AndresFZV/httpaas-go/internal/web"
)

// apacheWebRoot es el directorio que Apache sirve en cada instancia.
const apacheWebRoot = "/var/www/html"

// dnsKeyFile es la clave TSIG de Bind9 dentro del servidor ns.
const dnsKeyFile = "/etc/bind/cloud-key.key"

// Tiempos máximos de una conexión HTTP. La lectura admite subir un .zip de 50 MB por una red lenta.
const (
	readTimeout  = 2 * time.Minute
	writeTimeout = 2 * time.Minute
	idleTimeout  = 60 * time.Second
)

// Intervalos de verificación: normal, y mientras haya acciones o despliegues en curso.
const (
	healthInterval     = 5 * time.Second
	healthFastInterval = 3 * time.Second
)

func main() {
	cfg, err := loadConfig()
	if err != nil {
		log.Fatalf("configuración inválida: %v", err)
	}
	cfg.logSummary()

	firstIP := netip.MustParseAddr(firstInstanceIP)
	lastIP := netip.MustParseAddr(lastInstanceIP)

	store, err := instance.NewStore(cfg.DataFile, firstIP, lastIP)
	if err != nil {
		log.Fatalf("no se pudo cargar el registro de instancias: %v", err)
	}

	vms := vbox.NewManager(vbox.Config{
		VBoxManagePath: cfg.VBoxManage,
		TemplateDisk:   cfg.TemplateDisk,
		HostOnlyIF:     cfg.HostOnlyIF,
		MemoryMB:       vmMemoryMB,
		CPUs:           vmCPUs,
	})

	knownHosts, err := remote.NewKnownHosts(cfg.KnownHosts)
	if err != nil {
		log.Fatalf("no se pudo preparar el archivo de llaves de host: %v", err)
	}
	warnIfHostKeyMissing(knownHosts, cfg.DNSServer.String(), cfg.KnownHosts)

	// La llave de ns se registra a mano y se exige; las de las instancias se registran al crearlas.
	ssh := remote.NewClient(remote.Config{
		SSHPath:        cfg.SSHPath,
		User:           cfg.SSHUser,
		KeyFile:        cfg.SSHKey,
		ConnectTimeout: sshConnectTimeout,
		KnownHostsFile: knownHosts.Path(),
		StrictHosts:    []string{cfg.DNSServer.String()},
	})

	dnsUpdater := dns.NewUpdater(ssh, cfg.DNSServer.String(), instance.Domain, dnsKeyFile, dnsTTL)

	provisioner := provision.New(provision.Config{
		TemplateIP:   cfg.TemplateIP,
		BootTimeout:  bootTimeout,
		PollInterval: sshPollInterval,
	}, store, vms, ssh, dnsUpdater, hostnet.NewARPCache(), knownHosts)

	deployer := deploy.NewDeployer(ssh, apacheWebRoot)

	uploadsDir := filepath.Join(filepath.Dir(cfg.DataFile), "uploads")
	deployments, err := deployment.NewManager(uploadsDir, provisioner, deployer, deployment.NewHTTPVerifier())
	if err != nil {
		log.Fatalf("no se pudo preparar los despliegues: %v", err)
	}

	powerCtl := power.NewController(vms)

	monitor := health.NewMonitor(health.Config{
		DNSServer:    cfg.DNSServer,
		Domain:       instance.Domain,
		Interval:     healthInterval,
		FastInterval: healthFastInterval,
	}, store, vms)
	monitor.SetBusyCheck(func() bool {
		return len(powerCtl.Pending()) > 0 ||
			len(deployments.ActiveHostnames()) > 0 ||
			!monitor.Snapshot().Operational()
	})

	// Al terminar un despliegue o una acción de encendido se verifica de inmediato.
	deployments.OnFinished(func(string) {
		go monitor.Check(context.Background())
	})
	powerCtl.OnChange(func() {
		monitor.Check(context.Background())
	})

	monitor.Start(context.Background())

	handler, err := web.NewHandler(web.Config{
		Domain:       instance.Domain,
		DNSServer:    cfg.DNSServer.String(),
		NSVMName:     cfg.NSVMName,
		FirstIP:      firstIP,
		LastIP:       lastIP,
		TemplatesDir: "templates",
		StaticDir:    "static",
	}, web.Deps{
		Store:       store,
		Provisioner: provisioner,
		Deployments: deployments,
		Monitor:     monitor,
		Power:       powerCtl,
	})
	if err != nil {
		log.Fatalf("no se pudo inicializar la aplicación: %v", err)
	}

	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	server := &http.Server{
		Addr: cfg.Addr,
		Handler: web.Secure(mux, web.SecurityConfig{
			AuthUser:     cfg.AuthUser,
			AuthPassword: cfg.AuthPassword,
		}),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}

	log.Printf("servidor escuchando en http://localhost%s", cfg.Addr)
	if err := server.ListenAndServe(); err != nil {
		log.Fatalf("el servidor se detuvo: %v", err)
	}
}

// warnIfHostKeyMissing avisa al arrancar si la llave de host de ns no está registrada,
// porque sin ella la app no se conecta a ns y fallan el registro y la eliminación de nombres.
// file es la ruta tal como se configuró, para mostrar un comando que se pueda copiar en Git Bash.
func warnIfHostKeyMissing(knownHosts *remote.KnownHosts, host, file string) {
	ok, err := knownHosts.Contains(host)
	if err != nil {
		log.Printf("ADVERTENCIA: %v", err)
		return
	}
	if !ok {
		log.Printf("ADVERTENCIA: la llave de host de %s no está en %s; la app no se conectará a ns. "+
			"Regístrela con: ssh-keyscan %s >> %s", host, knownHosts.Path(), host, filepath.ToSlash(file))
	}
}
