package main

import (
	"log"
	"net/http"
	"net/netip"
	"time"

	"github.com/AndresFZV/httpaas-go/internal/deploy"
	"github.com/AndresFZV/httpaas-go/internal/dns"
	"github.com/AndresFZV/httpaas-go/internal/instance"
	"github.com/AndresFZV/httpaas-go/internal/provision"
	"github.com/AndresFZV/httpaas-go/internal/remote"
	"github.com/AndresFZV/httpaas-go/internal/vbox"
	"github.com/AndresFZV/httpaas-go/internal/web"
)

// apacheWebRoot es el directorio que Apache sirve en cada instancia.
const apacheWebRoot = "/var/www/html"

func main() {
	cfg, err := loadConfig()
	if err != nil {
		log.Fatalf("configuración inválida: %v", err)
	}
	cfg.logSummary()

	store, err := instance.NewStore(
		cfg.DataFile,
		netip.MustParseAddr(firstInstanceIP),
		netip.MustParseAddr(lastInstanceIP),
	)
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
		SSHPath:        "ssh",
		User:           cfg.SSHUser,
		KeyFile:        cfg.SSHKey,
		ConnectTimeout: sshConnectTimeout,
	})

	dnsUpdater := dns.NewUpdater(ssh, cfg.DNSServer.String(), instance.Domain, dnsTTL)

	provisioner := provision.New(provision.Config{
		TemplateIP:   cfg.TemplateIP,
		Interface:    cfg.Interface,
		BootTimeout:  bootTimeout,
		PollInterval: sshPollInterval,
	}, store, vms, ssh, dnsUpdater)

	deployer := deploy.NewDeployer(ssh, apacheWebRoot)

	handler, err := web.NewHandler(store, provisioner, deployer, "templates", "static")
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
