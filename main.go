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
	"github.com/AndresFZV/httpaas-go/internal/provision"
	"github.com/AndresFZV/httpaas-go/internal/remote"
	"github.com/AndresFZV/httpaas-go/internal/vbox"
	"github.com/AndresFZV/httpaas-go/internal/web"
)

// apacheWebRoot es el directorio que Apache sirve en cada instancia.
const apacheWebRoot = "/var/www/html"

// dnsKeyFile es la clave TSIG de Bind9 dentro del servidor ns.
const dnsKeyFile = "/etc/bind/cloud-key.key"

// healthInterval es cada cuánto se verifica la infraestructura y las instancias.
const healthInterval = 15 * time.Second

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

	ssh := remote.NewClient(remote.Config{
		SSHPath:        cfg.SSHPath,
		User:           cfg.SSHUser,
		KeyFile:        cfg.SSHKey,
		ConnectTimeout: sshConnectTimeout,
	})

	dnsUpdater := dns.NewUpdater(ssh, cfg.DNSServer.String(), instance.Domain, dnsKeyFile, dnsTTL)

	provisioner := provision.New(provision.Config{
		TemplateIP:   cfg.TemplateIP,
		BootTimeout:  bootTimeout,
		PollInterval: sshPollInterval,
	}, store, vms, ssh, dnsUpdater, hostnet.NewARPCache())

	deployer := deploy.NewDeployer(ssh, apacheWebRoot)

	uploadsDir := filepath.Join(filepath.Dir(cfg.DataFile), "uploads")
	deployments, err := deployment.NewManager(uploadsDir, provisioner, deployer, deployment.NewHTTPVerifier())
	if err != nil {
		log.Fatalf("no se pudo preparar los despliegues: %v", err)
	}

	monitor := health.NewMonitor(health.Config{
		DNSServer: cfg.DNSServer,
		Domain:    instance.Domain,
		Interval:  healthInterval,
	}, store, vms)
	monitor.Start(context.Background())

	// Al terminar un despliegue se verifica de inmediato, para que el estado de la instancia nueva
	// no espere a la siguiente verificación periódica.
	deployments.OnFinished(func(string) {
		go monitor.Check(context.Background())
	})

	handler, err := web.NewHandler(web.Config{
		Domain:       instance.Domain,
		DNSServer:    cfg.DNSServer.String(),
		FirstIP:      firstIP,
		LastIP:       lastIP,
		TemplatesDir: "templates",
		StaticDir:    "static",
	}, store, provisioner, deployments, monitor)
	if err != nil {
		log.Fatalf("no se pudo inicializar la aplicación: %v", err)
	}

	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	server := &http.Server{
		Addr:              cfg.Addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	log.Printf("servidor escuchando en http://localhost%s", cfg.Addr)
	if err := server.ListenAndServe(); err != nil {
		log.Fatalf("el servidor se detuvo: %v", err)
	}
}
