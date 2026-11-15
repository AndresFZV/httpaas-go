// Package metrics guarda métricas en memoria y las escribe en el formato de texto de Prometheus,
// sin dependencias externas.
package metrics

import (
	"bufio"
	"fmt"
	"io"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"
)

// ContentType es el tipo de contenido del formato de texto de Prometheus.
const ContentType = "text/plain; version=0.0.4; charset=utf-8"

// Label es una etiqueta de una serie, por ejemplo etapa="publicar".
type Label struct {
	Name  string
	Value string
}

// L crea una etiqueta.
func L(name, value string) Label {
	return Label{Name: name, Value: value}
}

// Sample es un valor de una métrica calculada al momento de consultarla.
type Sample struct {
	Labels []Label
	Value  float64
}

type kind string

const (
	kindCounter kind = "counter"
	kindGauge   kind = "gauge"
	kindSummary kind = "summary"
)

// series es una combinación de etiquetas de una métrica.
// Los contadores usan value; los resúmenes usan sum y count.
type series struct {
	labels []Label
	value  float64
	sum    float64
	count  uint64
}

type family struct {
	name   string
	help   string
	kind   kind
	series map[string]*series
	gauge  func() []Sample // solo para medidores calculados al consultar
}

// Registry guarda las métricas de la aplicación. Un *Registry nil no registra nada,
// así los componentes funcionan igual sin métricas (por ejemplo en las pruebas).
type Registry struct {
	mu       sync.Mutex
	families map[string]*family
}

// New crea un registro vacío.
func New() *Registry {
	return &Registry{families: make(map[string]*family)}
}

// Counter declara un contador: un valor que solo aumenta, como la cantidad de despliegues.
func (r *Registry) Counter(name, help string) {
	r.declare(name, help, kindCounter, nil)
}

// Summary declara un resumen: la suma y la cantidad de observaciones, como las duraciones.
// Con las dos se calcula el promedio en Prometheus: rate(x_sum[5m]) / rate(x_count[5m]).
func (r *Registry) Summary(name, help string) {
	r.declare(name, help, kindSummary, nil)
}

// GaugeFunc declara un medidor cuyo valor se calcula con fn cada vez que se consultan las métricas,
// como la cantidad de instancias por estado.
func (r *Registry) GaugeFunc(name, help string, fn func() []Sample) {
	r.declare(name, help, kindGauge, fn)
}

// declare agrega una métrica. Declararla de nuevo con el mismo tipo no hace nada,
// para que cada componente pueda declarar las métricas que usa.
func (r *Registry) declare(name, help string, k kind, gauge func() []Sample) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	if f, ok := r.families[name]; ok {
		if f.kind != k {
			panic(fmt.Sprintf("metrics: %s ya está declarada como %s", name, f.kind))
		}
		if gauge != nil {
			f.gauge = gauge
		}
		return
	}
	r.families[name] = &family{name: name, help: help, kind: k, series: make(map[string]*series), gauge: gauge}
}

// Inc suma 1 al contador name con las etiquetas dadas.
func (r *Registry) Inc(name string, labels ...Label) {
	r.update(name, kindCounter, labels, func(s *series) { s.value++ })
}

// Observe agrega una observación v al resumen name con las etiquetas dadas.
func (r *Registry) Observe(name string, v float64, labels ...Label) {
	r.update(name, kindSummary, labels, func(s *series) {
		s.sum += v
		s.count++
	})
}

func (r *Registry) update(name string, k kind, labels []Label, apply func(*series)) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	f, ok := r.families[name]
	if !ok || f.kind != k {
		panic(fmt.Sprintf("metrics: %s no está declarada como %s", name, k))
	}
	key := labelString(labels)
	s, ok := f.series[key]
	if !ok {
		s = &series{labels: slices.Clone(labels)}
		f.series[key] = s
	}
	apply(s)
}

// WriteText escribe todas las métricas en el formato de texto de Prometheus, ordenadas por nombre.
func (r *Registry) WriteText(w io.Writer) error {
	if r == nil {
		return nil
	}

	// Los medidores se calculan fuera del candado, porque consultan otros componentes.
	r.mu.Lock()
	names := slices.Sorted(maps.Keys(r.families))
	gauges := make(map[string]func() []Sample)
	for _, name := range names {
		if f := r.families[name]; f.gauge != nil {
			gauges[name] = f.gauge
		}
	}
	r.mu.Unlock()

	samples := make(map[string][]Sample, len(gauges))
	for name, fn := range gauges {
		samples[name] = fn()
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	bw := bufio.NewWriter(w)
	for _, name := range names {
		f := r.families[name]
		fmt.Fprintf(bw, "# HELP %s %s\n", f.name, escapeHelp(f.help))
		fmt.Fprintf(bw, "# TYPE %s %s\n", f.name, f.kind)

		switch f.kind {
		case kindGauge:
			for _, s := range samples[name] {
				fmt.Fprintf(bw, "%s%s %s\n", f.name, labelString(s.Labels), formatValue(s.Value))
			}
		case kindCounter:
			for _, key := range sortedKeys(f.series) {
				fmt.Fprintf(bw, "%s%s %s\n", f.name, key, formatValue(f.series[key].value))
			}
		case kindSummary:
			for _, key := range sortedKeys(f.series) {
				s := f.series[key]
				fmt.Fprintf(bw, "%s_sum%s %s\n", f.name, key, formatValue(s.sum))
				fmt.Fprintf(bw, "%s_count%s %d\n", f.name, key, s.count)
			}
		}
	}
	return bw.Flush()
}

func sortedKeys(m map[string]*series) []string {
	return slices.Sorted(maps.Keys(m))
}

// labelString devuelve las etiquetas con la forma {a="1",b="2"}, o "" si no hay.
func labelString(labels []Label) string {
	if len(labels) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteByte('{')
	for i, l := range labels {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(l.Name)
		b.WriteString(`="`)
		b.WriteString(escapeLabelValue(l.Value))
		b.WriteByte('"')
	}
	b.WriteByte('}')
	return b.String()
}

var labelValueEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)

func escapeLabelValue(v string) string {
	return labelValueEscaper.Replace(v)
}

var helpEscaper = strings.NewReplacer(`\`, `\\`, "\n", `\n`)

func escapeHelp(v string) string {
	return helpEscaper.Replace(v)
}

func formatValue(v float64) string {
	return strconv.FormatFloat(v, 'g', -1, 64)
}