package web

import (
	"fmt"
	"html/template"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// iconNamePattern limita los nombres de ícono a minúsculas, porque icon devuelve HTML sin escapar.
var iconNamePattern = regexp.MustCompile(`^[a-z]+$`)

var spanishMonths = [...]string{"ene", "feb", "mar", "abr", "may", "jun", "jul", "ago", "sep", "oct", "nov", "dic"}

var templateFuncs = template.FuncMap{
	"icon":        icon,
	"displayName": displayName,
	"formatDate":  formatDate,
	"inc":         func(i int) int { return i + 1 },
}

// loadPages crea un conjunto de plantillas por página: layout + partials + la página.
// Cada página define su propio bloque "content", por eso no pueden compartir un solo conjunto.
func loadPages(dir string) (map[string]*template.Template, error) {
	shared := []string{
		filepath.Join(dir, "layout.html"),
		filepath.Join(dir, "partials.html"),
	}

	pageFiles, err := filepath.Glob(filepath.Join(dir, "pages", "*.html"))
	if err != nil {
		return nil, fmt.Errorf("buscar páginas: %w", err)
	}
	if len(pageFiles) == 0 {
		return nil, fmt.Errorf("no hay páginas en %s", filepath.Join(dir, "pages"))
	}

	pages := make(map[string]*template.Template, len(pageFiles))
	for _, file := range pageFiles {
		name := strings.TrimSuffix(filepath.Base(file), ".html")
		files := append(slices.Clone(shared), file)

		tmpl, err := template.New(name).Funcs(templateFuncs).ParseFiles(files...)
		if err != nil {
			return nil, fmt.Errorf("cargar página %s: %w", name, err)
		}
		pages[name] = tmpl
	}
	return pages, nil
}

// icon dibuja un ícono de static/icons.svg con el tamaño indicado.
func icon(name string, size int) template.HTML {
	if !iconNamePattern.MatchString(name) {
		return ""
	}
	return template.HTML(fmt.Sprintf(
		`<svg class="icon" width="%d" height="%d" aria-hidden="true"><use href="/static/icons.svg#%s"></use></svg>`,
		size, size, name,
	))
}

// displayName convierte un hostname en un nombre para mostrar: "paisajes" → "Paisajes".
func displayName(hostname string) string {
	first, size := utf8.DecodeRuneInString(hostname)
	if first == utf8.RuneError {
		return hostname
	}
	return string(unicode.ToUpper(first)) + hostname[size:]
}

// formatDate muestra una fecha en hora local con el mes abreviado en español: "11 nov 2026, 00:12".
func formatDate(t time.Time) string {
	t = t.Local()
	return fmt.Sprintf("%d %s %d, %02d:%02d", t.Day(), spanishMonths[t.Month()-1], t.Year(), t.Hour(), t.Minute())
}
