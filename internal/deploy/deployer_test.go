package deploy

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

type fakeRemote struct {
	uploadedTo string
	content    string
	ranOn      string
	script     string
	failUpload bool
}

func (f *fakeRemote) Upload(_ context.Context, host string, r io.Reader, remotePath string) error {
	if f.failUpload {
		return errors.New("conexión rechazada")
	}
	data, _ := io.ReadAll(r)
	f.uploadedTo = host + ":" + remotePath
	f.content = string(data)
	return nil
}

func (f *fakeRemote) Run(_ context.Context, host, script string) error {
	f.ranOn = host
	f.script = script
	return nil
}

func TestDeployUploadsAndPublishes(t *testing.T) {
	f := &fakeRemote{}
	d := NewDeployer(f, "/var/www/html")

	if err := d.Deploy(context.Background(), "192.168.10.30", strings.NewReader("zip")); err != nil {
		t.Fatalf("Deploy() error: %v", err)
	}

	if f.uploadedTo != "192.168.10.30:"+remoteArchive || f.content != "zip" {
		t.Errorf("subida = %q con %q", f.uploadedTo, f.content)
	}
	if f.ranOn != "192.168.10.30" {
		t.Errorf("script ejecutado en %q", f.ranOn)
	}
	for _, want := range []string{"unzip", "WEB_ROOT=/var/www/html", "chown -R www-data:www-data"} {
		if !strings.Contains(f.script, want) {
			t.Errorf("el script no contiene %q:\n%s", want, f.script)
		}
	}
}

func TestDeployStopsWhenUploadFails(t *testing.T) {
	f := &fakeRemote{failUpload: true}
	d := NewDeployer(f, "/var/www/html")

	if err := d.Deploy(context.Background(), "192.168.10.30", strings.NewReader("zip")); err == nil {
		t.Fatal("Deploy() no devolvió error")
	}
	if f.script != "" {
		t.Error("no debe ejecutarse el script si la subida falló")
	}
}
