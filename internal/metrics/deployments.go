package metrics

import "time"

// Métricas de los despliegues.
const (
	DeploymentsTotal   = "httpaas_deployments_total"
	DeploymentDuration = "httpaas_deployment_duration_seconds"
	StepDuration       = "httpaas_deployment_step_duration_seconds"
)

// Deployments registra las mediciones de los despliegues. Cumple la interfaz deployment.Observer.
type Deployments struct {
	reg *Registry
}

// NewDeployments declara las métricas de los despliegues en reg.
func NewDeployments(reg *Registry) *Deployments {
	reg.Counter(DeploymentsTotal, "Despliegues terminados, por tipo y resultado.")
	reg.Summary(DeploymentDuration, "Duración de los despliegues en segundos, por tipo y resultado.")
	reg.Summary(StepDuration, "Duración de cada etapa completada del despliegue, en segundos.")
	return &Deployments{reg: reg}
}

// StepCompleted registra la duración de una etapa completada.
func (d *Deployments) StepCompleted(step string, dur time.Duration) {
	d.reg.Observe(StepDuration, dur.Seconds(), L("etapa", step))
}

// DeploymentFinished registra un despliegue terminado y su duración.
func (d *Deployments) DeploymentFinished(kind, result string, dur time.Duration) {
	d.reg.Inc(DeploymentsTotal, L("tipo", kind), L("resultado", result))
	d.reg.Observe(DeploymentDuration, dur.Seconds(), L("tipo", kind), L("resultado", result))
}