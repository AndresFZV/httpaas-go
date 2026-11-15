package deploy

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"path"
	"path/filepath"
	"strings"
)

// Límites del .zip del contenido web.
const (
	MaxArchiveSize      int64 = 50 << 20  // tamaño del .zip: 50 MB
	MaxUncompressedSize int64 = 200 << 20 // contenido descomprimido: 200 MB
	MaxArchiveEntries         = 5000      // archivos y carpetas dentro del .zip
)

const indexFile = "index.html"

var (
	ErrArchiveMissing    = errors.New("debe seleccionar un archivo .zip con el contenido web")
	ErrArchiveExtension  = errors.New("el archivo debe tener extensión .zip")
	ErrArchiveTooLarge   = fmt.Errorf("el archivo supera el tamaño máximo de %d MB", MaxArchiveSize>>20)
	ErrArchiveTooBig     = fmt.Errorf("el contenido descomprimido supera %d MB o tiene más de %d archivos", MaxUncompressedSize>>20, MaxArchiveEntries)
	ErrArchiveCorrupt    = errors.New("el archivo no es un .zip válido")
	ErrArchiveUnsafePath = errors.New("el .zip contiene rutas no permitidas (absolutas o con ..)")
	ErrArchiveNoIndex    = errors.New("el .zip debe tener un index.html en la raíz; comprima el contenido de la carpeta, no la carpeta")
)

// ValidateArchive verifica que r sea un .zip seguro de desplegar en el directorio web de Apache.
func ValidateArchive(r io.ReaderAt, size int64, filename string) error {
	if !strings.EqualFold(filepath.Ext(filename), ".zip") {
		return ErrArchiveExtension
	}
	if size > MaxArchiveSize {
		return ErrArchiveTooLarge
	}

	zr, err := zip.NewReader(r, size)
	if errors.Is(err, zip.ErrInsecurePath) {
		return ErrArchiveUnsafePath
	}
	if err != nil {
		return ErrArchiveCorrupt
	}

	if len(zr.File) > MaxArchiveEntries {
		return ErrArchiveTooBig
	}

	hasIndex := false
	var total int64
	for _, f := range zr.File {
		name := normalizeEntryName(f.Name)
		if !isSafeEntry(name) {
			return ErrArchiveUnsafePath
		}
		if name == indexFile {
			hasIndex = true
		}

		n, err := uncompressedSize(f, MaxUncompressedSize-total)
		if err != nil {
			return err
		}
		total += n
	}

	if !hasIndex {
		return ErrArchiveNoIndex
	}
	return nil
}

// uncompressedSize descomprime f sin guardarlo y devuelve cuántos bytes ocupa.
// Se descomprime de verdad porque el tamaño declarado en el .zip se puede falsificar;
// archive/zip devuelve un error si el contenido real no coincide con el declarado.
// Si f ocupa más de limit bytes, devuelve ErrArchiveTooBig sin terminar de leerlo.
func uncompressedSize(f *zip.File, limit int64) (int64, error) {
	if f.FileInfo().IsDir() {
		return 0, nil
	}
	if f.UncompressedSize64 > uint64(limit) {
		return 0, ErrArchiveTooBig
	}

	rc, err := f.Open()
	if err != nil {
		return 0, ErrArchiveCorrupt
	}
	defer rc.Close()

	n, err := io.Copy(io.Discard, io.LimitReader(rc, limit+1))
	if err != nil {
		return 0, ErrArchiveCorrupt
	}
	if n > limit {
		return 0, ErrArchiveTooBig
	}
	return n, nil
}

// normalizeEntryName convierte las barras invertidas a barras normales.
func normalizeEntryName(name string) string {
	return strings.ReplaceAll(name, `\`, "/")
}

// isSafeEntry indica si la ruta queda dentro del directorio de destino al descomprimir.
func isSafeEntry(name string) bool {
	if name == "" || path.IsAbs(name) || strings.Contains(name, ":") {
		return false
	}
	clean := path.Clean(name)
	return clean != ".." && !strings.HasPrefix(clean, "../")
}
