package deploy

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
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

// buildZipWithSizes crea en memoria un .zip con index.html y un archivo de ceros por cada tamaño.
// Los ceros se comprimen casi por completo: 100 MB ocupan unos 100 KB dentro del .zip.
func buildZipWithSizes(t *testing.T, sizes ...int64) []byte {
	t.Helper()

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("index.html")
	if err != nil {
		t.Fatalf("crear index.html: %v", err)
	}
	if _, err := w.Write([]byte("<h1>hola</h1>")); err != nil {
		t.Fatalf("escribir index.html: %v", err)
	}

	zeros := make([]byte, 1<<20)
	for i, size := range sizes {
		w, err := zw.Create(fmt.Sprintf("datos-%d.bin", i))
		if err != nil {
			t.Fatalf("crear entrada: %v", err)
		}
		for written := int64(0); written < size; {
			chunk := min(int64(len(zeros)), size-written)
			if _, err := w.Write(zeros[:chunk]); err != nil {
				t.Fatalf("escribir entrada: %v", err)
			}
			written += chunk
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("cerrar zip: %v", err)
	}
	return buf.Bytes()
}

func TestValidateArchiveUncompressedLimit(t *testing.T) {
	tests := []struct {
		name    string
		sizes   []int64
		wantErr error
	}{
		{"dentro del límite", []int64{50 << 20}, nil},
		{"un archivo supera el límite", []int64{MaxUncompressedSize + 1}, ErrArchiveTooBig},
		{"la suma supera el límite", []int64{80 << 20, 80 << 20, 80 << 20}, ErrArchiveTooBig},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := buildZipWithSizes(t, tt.sizes...)
			if int64(len(data)) > MaxArchiveSize {
				t.Fatalf("el .zip de prueba ocupa %d bytes; debe caber en el límite comprimido", len(data))
			}

			err := ValidateArchive(bytes.NewReader(data), int64(len(data)), "sitio.zip")
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("ValidateArchive() = %v, se esperaba %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateArchiveTooManyEntries(t *testing.T) {
	names := make([]string, 0, MaxArchiveEntries+1)
	names = append(names, "index.html")
	for i := range MaxArchiveEntries {
		names = append(names, fmt.Sprintf("img/%d.txt", i))
	}
	data := buildZip(t, names...)

	err := ValidateArchive(bytes.NewReader(data), int64(len(data)), "sitio.zip")
	if !errors.Is(err, ErrArchiveTooBig) {
		t.Errorf("ValidateArchive() = %v, se esperaba %v", err, ErrArchiveTooBig)
	}
}
