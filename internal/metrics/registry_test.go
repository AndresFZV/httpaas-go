package metrics

import (
	"strings"
	"testing"
	"time"
)

func render(t *testing.T, r *Registry) string {
	t.Helper()
	var b strings.Builder
	if err := r.WriteText(&b); err != nil {
		t.Fatalf("WriteText() error: %v", err)
	}
	return b.String()
}

func TestCounterAndSummaryText(t *testing.T) {
	r := New()
	r.Counter("app_requests_total", "Peticiones atendidas.")
	r.Summary("app_duration_seconds", "Duración de las peticiones.")

	r.Inc("app_requests_total", L("codigo", "200"))
	r.Inc("app_requests_total", L("codigo", "200"))
	r.Inc("app_requests_total", L("codigo", "404"))
	r.Observe("app_duration_seconds", 0.5)
	r.Observe("app_duration_seconds", 1.5)

	want := `# HELP app_duration_seconds Duración de las peticiones.
# TYPE app_duration_seconds summary
app_duration_seconds_sum 2
app_duration_seconds_count 2
# HELP app_requests_total Peticiones atendidas.
# TYPE app_requests_total counter
app_requests_total{codigo="200"} 2
app_requests_total{codigo="404"} 1
`
	if got := render(t, r); got != want {
		t.Errorf("salida:\n%s\nse esperaba:\n%s", got, want)
	}
}

func TestGaugeFuncIsComputedOnEachWrite(t *testing.T) {
	r := New()
	value := 1.0
	r.GaugeFunc("app_instances", "Instancias.", func() []Sample {
		return []Sample{{Labels: []Label{L("estado", "activa")}, Value: value}}
	})

	if got := render(t, r); !strings.Contains(got, `app_instances{estado="activa"} 1`) {
		t.Errorf("salida inicial:\n%s", got)
	}
	value = 3
	if got := render(t, r); !strings.Contains(got, `app_instances{estado="activa"} 3`) {
		t.Errorf("salida después del cambio:\n%s", got)
	}
}

func TestLabelValuesAreEscaped(t *testing.T) {
	r := New()
	r.Counter("app_errors_total", "Errores.")
	r.Inc("app_errors_total", L("detalle", "dijo \"hola\"\\\nfin"))

	want := `app_errors_total{detalle="dijo \"hola\"\\\nfin"} 1`
	if got := render(t, r); !strings.Contains(got, want) {
		t.Errorf("salida:\n%s\nfalta: %s", got, want)
	}
}

func TestNilRegistryDoesNothing(t *testing.T) {
	var r *Registry
	r.Counter("x_total", "x")
	r.Inc("x_total")
	r.Observe("y", 1)
	if got := render(t, r); got != "" {
		t.Errorf("salida = %q, se esperaba vacía", got)
	}
}

func TestUndeclaredMetricPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("se esperaba un panic al usar una métrica no declarada")
		}
	}()
	New().Inc("no_declarada_total")
}

func TestDeploymentsObserver(t *testing.T) {
	r := New()
	d := NewDeployments(r)

	d.StepCompleted("publicar", 2*time.Second)
	d.DeploymentFinished("creacion", "exito", 150*time.Second)

	got := render(t, r)
	for _, want := range []string{
		`httpaas_deployment_step_duration_seconds_sum{etapa="publicar"} 2`,
		`httpaas_deployments_total{tipo="creacion",resultado="exito"} 1`,
		`httpaas_deployment_duration_seconds_count{tipo="creacion",resultado="exito"} 1`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("falta %q en:\n%s", want, got)
		}
	}
}
