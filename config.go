package main

import (
	"fmt"
	"log"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// Valores fijos del aprovisionamiento.
const (
	firstInstanceIP   = "192.168.10.30"
	lastInstanceIP    = "192.168.10.254"
	vmMemoryMB        = 512
	vmCPUs            = 1
	dnsTTL            = 300
	bootTimeout       = 3 * time.Minute
	sshPollInterval   = 3 * time.Second
	sshConnectTimeout = 5 * time.Second
)

// config reúne la configuración de la aplicación; cada campo se puede cambiar con una variable de entorno.
type config struct {
	Addr         string     // HTTPAAS_ADDR
	DataFile     string     // HTTPAAS_DATA_FILE
	VBoxManage   string     // HTTPAAS_VBOXMANAGE
	TemplateDisk string     // HTTPAAS_TEMPLATE_DISK
	HostOnlyIF   string     // HTTPAAS_HOSTONLY_IF
	Interface    string     // HTTPAAS_VM_INTERFACE
	SSHUser      string     // HTTPAAS_SSH_USER
	SSHKey       string     // HTTPAAS_SSH_KEY
	TemplateIP   netip.Addr // HTTPAAS_TEMPLATE_IP
	DNSServer    netip.Addr // HTTPAAS_DNS_SERVER
}

// loadConfig lee la configuración de las variables de entorno con sus valores por defecto.
func loadConfig() (config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return config{}, fmt.Errorf("obtener la carpeta del usuario: %w", err)
	}

	templateIP, err := getEnvAddr("HTTPAAS_TEMPLATE_IP", "192.168.10.20")
	if err != nil {
		return config{}, err
	}
	dnsServer, err := getEnvAddr("HTTPAAS_DNS_SERVER", "192.168.10.10")
	if err != nil {
		return config{}, err
	}

	return config{
		Addr:         getEnv("HTTPAAS_ADDR", ":8080"),
		DataFile:     getEnv("HTTPAAS_DATA_FILE", filepath.Join("data", "instances.json")),
		VBoxManage:   getEnv("HTTPAAS_VBOXMANAGE", defaultVBoxManage()),
		TemplateDisk: getEnv("HTTPAAS_TEMPLATE_DISK", filepath.Join(home, "VirtualBox VMs", "ApacheServer", "ApacheServer.vdi")),
		HostOnlyIF:   getEnv("HTTPAAS_HOSTONLY_IF", "VirtualBox Host-Only Ethernet Adapter"),
		Interface:    getEnv("HTTPAAS_VM_INTERFACE", "enp0s3"),
		SSHUser:      getEnv("HTTPAAS_SSH_USER", "deploy"),
		SSHKey:       getEnv("HTTPAAS_SSH_KEY", filepath.Join(home, ".ssh", "httpaas")),
		TemplateIP:   templateIP,
		DNSServer:    dnsServer,
	}, nil
}

// logSummary muestra la configuración al arrancar y avisa si falta algún archivo necesario.
func (c config) logSummary() {
	log.Printf("VBoxManage: %s", c.VBoxManage)
	log.Printf("disco plantilla: %s", c.TemplateDisk)
	log.Printf("adaptador host-only: %s", c.HostOnlyIF)
	log.Printf("IP temporal de la plantilla: %s, servidor DNS: %s", c.TemplateIP, c.DNSServer)
	log.Printf("SSH: %s con llave %s", c.SSHUser, c.SSHKey)

	for name, path := range map[string]string{
		"VBoxManage":      c.VBoxManage,
		"disco plantilla": c.TemplateDisk,
		"llave SSH":       c.SSHKey,
	} {
		if _, err := os.Stat(path); err != nil {
			log.Printf("ADVERTENCIA: no se encontró %s en %s; el aprovisionamiento fallará", name, path)
		}
	}
}

func defaultVBoxManage() string {
	if runtime.GOOS == "windows" {
		return `C:\Program Files\Oracle\VirtualBox\VBoxManage.exe`
	}
	return "/usr/bin/VBoxManage"
}

// getEnv devuelve el valor de la variable de entorno key o fallback si no está definida.
func getEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}

// getEnvAddr lee una dirección IPv4 de la variable de entorno key.
func getEnvAddr(key, fallback string) (netip.Addr, error) {
	value := getEnv(key, fallback)
	addr, err := netip.ParseAddr(value)
	if err != nil || !addr.Is4() {
		return netip.Addr{}, fmt.Errorf("%s: %q no es una IPv4 válida", key, value)
	}
	return addr, nil
}
