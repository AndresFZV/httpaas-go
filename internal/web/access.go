package web

import (
	"strings"
)

// AccessConfig describe cómo se entra a las instancias y con qué recursos se crean,
// para mostrarlo en el detalle de cada instancia.
type AccessConfig struct {
	SSHUser        string // usuario de las VMs, por ejemplo deploy
	SSHKey         string // ruta de la llave privada de la aplicación
	KnownHostsFile string // archivo known_hosts de la aplicación
	TemplateDisk   string // disco multiconexión del que salen las instancias
	MemoryMB       int
	CPUs           int
	Home           string // carpeta del usuario, para mostrar las rutas como ~/...
}

// accessView son los datos de acceso que muestra el detalle de una instancia.
type accessView struct {
	SSHUser      string
	SSHPrefix    string // comando ssh sin la IP, por ejemplo "ssh -i ~/.ssh/httpaas ... deploy@"
	TemplateDisk string // solo el nombre del archivo, por ejemplo ApacheServer.vdi
	MemoryMB     int
	CPUs         int
}

func newAccessView(c AccessConfig) accessView {
	v := accessView{
		SSHUser:      c.SSHUser,
		TemplateDisk: baseName(c.TemplateDisk),
		MemoryMB:     c.MemoryMB,
		CPUs:         c.CPUs,
	}
	if c.SSHUser == "" || c.SSHKey == "" {
		return v
	}

	// Se usa el known_hosts de la aplicación, que ya tiene registrada la llave de cada instancia.
	// Así el comando no choca con llaves viejas del known_hosts del usuario cuando una IP se reutiliza.
	parts := []string{"ssh", "-i", shellPath(c.SSHKey, c.Home)}
	if c.KnownHostsFile != "" {
		parts = append(parts, "-o", shellQuote("UserKnownHostsFile="+displayPath(c.KnownHostsFile, c.Home)))
	}
	v.SSHPrefix = strings.Join(parts, " ") + " " + c.SSHUser + "@"
	return v
}

// displayPath escribe una ruta con barras normales, que Git Bash y ssh entienden en Windows,
// y reemplaza la carpeta del usuario por ~ para que el comando sea más corto.
// ssh expande ~ por su cuenta en -i y en UserKnownHostsFile.
func displayPath(path, home string) string {
	p := strings.ReplaceAll(path, `\`, "/")
	h := strings.TrimRight(strings.ReplaceAll(home, `\`, "/"), "/")
	if h != "" && (strings.EqualFold(p, h) || hasPrefixFold(p, h+"/")) {
		return "~" + p[len(h):]
	}
	return p
}

// shellPath es displayPath con comillas si la ruta tiene espacios.
func shellPath(path, home string) string {
	return shellQuote(displayPath(path, home))
}

// shellQuote pone comillas dobles a un argumento con espacios. Dentro de las comillas,
// bash no expande ~, pero ssh sí lo hace al leer la ruta.
func shellQuote(s string) string {
	if !strings.ContainsAny(s, " \t") {
		return s
	}
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}

// baseName devuelve el nombre del archivo de una ruta de Windows o de Unix.
func baseName(path string) string {
	if i := strings.LastIndexAny(path, `/\`); i >= 0 {
		return path[i+1:]
	}
	return path
}

func hasPrefixFold(s, prefix string) bool {
	return len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix)
}
