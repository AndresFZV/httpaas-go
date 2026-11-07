package deploy

import (
	"archive/zip"
	"bytes"
	"errors"
	"testing"
)

// buildZip crea en memoria un .zip con un archivo por cada nombre recibido.
func buildZip(t *testing.T, names ...string) []byte {
	t.Helper()

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, name := range names {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("crear entrada %q: %v", name, err)
		}
		if _, err := w.Write([]byte("contenido")); err != nil {
			t.Fatalf("escribir entrada %q: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("cerrar zip: %v", err)
	}
	return buf.Bytes()
}

func TestValidateArchive(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		data     []byte
		wantErr  error
	}{
		{"válido", "sitio.zip", buildZip(t, "index.html", "css/style.css"), nil},
		{"extensión en mayúscula", "SITIO.ZIP", buildZip(t, "index.html"), nil},
		{"rutas con barra invertida", "sitio.zip", buildZip(t, "index.html", `css\style.css`), nil},
		{"otra extensión", "sitio.rar", buildZip(t, "index.html"), ErrArchiveExtension},
		{"no es zip", "sitio.zip", []byte("esto no es un zip"), ErrArchiveCorrupt},
		{"index dentro de carpeta", "sitio.zip", buildZip(t, "sitio/index.html"), ErrArchiveNoIndex},
		{"sin index", "sitio.zip", buildZip(t, "pagina.html"), ErrArchiveNoIndex},
		{"ruta con ..", "sitio.zip", buildZip(t, "index.html", "../fuera.html"), ErrArchiveUnsafePath},
		{"ruta absoluta", "sitio.zip", buildZip(t, "index.html", "/etc/passwd"), ErrArchiveUnsafePath},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := bytes.NewReader(tt.data)
			err := ValidateArchive(r, int64(len(tt.data)), tt.filename)
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("ValidateArchive() = %v, se esperaba %v", err, tt.wantErr)
			}
		})
	}
}
