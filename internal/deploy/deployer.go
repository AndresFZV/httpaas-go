package deploy

import (
	"context"
	"fmt"
	"io"
)

// remoteArchive es la ruta temporal del .zip dentro de la instancia.
const remoteArchive = "/tmp/httpaas-site.zip"

// Remote copia archivos y ejecuta scripts en una máquina remota.
type Remote interface {
	Upload(ctx context.Context, host string, r io.Reader, remotePath string) error
	Run(ctx context.Context, host, script string) error
}

// Deployer publica el contenido de un .zip en el directorio web de una instancia Apache.
type Deployer struct {
	remote  Remote
	webRoot string
}

// NewDeployer crea un Deployer que publica en webRoot, por ejemplo /var/www/html.
func NewDeployer(remote Remote, webRoot string) *Deployer {
	return &Deployer{remote: remote, webRoot: webRoot}
}

// Deploy envía archive a host y reemplaza el contenido del directorio web con el del .zip.
// archive debe haber pasado antes por ValidateArchive.
func (d *Deployer) Deploy(ctx context.Context, host string, archive io.Reader) error {
	if err := d.remote.Upload(ctx, host, archive, remoteArchive); err != nil {
		return fmt.Errorf("enviar contenido a %s: %w", host, err)
	}
	if err := d.remote.Run(ctx, host, publishScript(d.webRoot)); err != nil {
		return fmt.Errorf("publicar contenido en %s: %w", host, err)
	}
	return nil
}

// publishScript descomprime el .zip en una carpeta temporal y reemplaza con ella el directorio web.
func publishScript(webRoot string) string {
	return fmt.Sprintf(`set -eu
ARCHIVE=%[1]s
WEB_ROOT=%[2]s
STAGING=$(mktemp -d)
trap 'rm -rf "$STAGING" "$ARCHIVE"' EXIT

# unzip devuelve 1 cuando solo hay advertencias (por ejemplo, rutas con barra invertida).
unzip -q -o "$ARCHIVE" -d "$STAGING" || [ $? -eq 1 ]

sudo find "$WEB_ROOT" -mindepth 1 -delete
sudo cp -a "$STAGING"/. "$WEB_ROOT"/
sudo chown -R www-data:www-data "$WEB_ROOT"
`, remoteArchive, webRoot)
}
