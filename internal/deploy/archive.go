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

// MaxArchiveSize es el tamaño máximo permitido para el .zip del contenido web.
const MaxArchiveSize int64 = 50 << 20 // 50 MB

const indexFile = "index.html"

var (
	ErrArchiveMissing    = errors.New("debe seleccionar un archivo .zip con el contenido web")
	ErrArchiveExtension  = errors.New("el archivo debe tener extensión .zip")
	ErrArchiveTooLarge   = fmt.Errorf("el archivo supera el tamaño máximo de %d MB", MaxArchiveSize>>20)
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

	hasIndex := false
	for _, f := range zr.File {
		name := normalizeEntryName(f.Name)
		if !isSafeEntry(name) {
			return ErrArchiveUnsafePath
		}
		if name == indexFile {
			hasIndex = true
		}
	}

	if !hasIndex {
		return ErrArchiveNoIndex
	}
	return nil
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
