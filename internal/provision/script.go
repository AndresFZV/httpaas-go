package provision

import (
	"fmt"
	"net/netip"

	"github.com/AndresFZV/httpaas-go/internal/instance"
)

// configureScript devuelve el script de bash que cambia el hostname y la IP de una VM recién clonada
// y la reinicia para aplicar los cambios.
func configureScript(hostname string, ip netip.Addr, iface string) string {
	return fmt.Sprintf(`set -eu
NEW_HOSTNAME=%[1]s
FQDN=%[1]s.%[4]s

sudo hostnamectl set-hostname "$NEW_HOSTNAME"

if grep -q '^127\.0\.1\.1' /etc/hosts; then
    sudo sed -i "s/^127\.0\.1\.1.*/127.0.1.1\t$FQDN\t$NEW_HOSTNAME/" /etc/hosts
else
    printf '127.0.1.1\t%%s\t%%s\n' "$FQDN" "$NEW_HOSTNAME" | sudo tee -a /etc/hosts >/dev/null
fi

sudo tee /etc/network/interfaces >/dev/null <<'EOF'
source /etc/network/interfaces.d/*

auto lo
iface lo inet loopback

auto %[3]s
iface %[3]s inet static
    address %[2]s/%[5]d
EOF

# El reinicio se programa a 2 segundos para que esta sesión SSH termine sin error.
sudo systemd-run --on-active=2 /usr/bin/systemctl reboot >/dev/null
`, hostname, ip, iface, instance.Domain, instance.PrefixLen)
}
