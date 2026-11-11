package health

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func baseSnapshot() Snapshot {
	return Snapshot{
		CheckedAt: time.Now(),
		Components: []Component{
			{Name: "Servidor DNS · Bind9", OK: true, Detail: "Resuelve ns.cloud.local"},
			{Name: "VirtualBox", OK: true, Detail: "Versión 7.2.6"},
		},
		Instances: map[string]InstanceStatus{"paisajes": InstanceActive},
		VMs:       map[string]bool{"paisajes": true, "debian ns": true},
		VMsKnown:  true,
	}
}

func TestDescribeChangesFirstCheckIsSummary(t *testing.T) {
	lines := describeChanges(Snapshot{}, baseSnapshot())

	if len(lines) != 1 || !strings.Contains(lines[0], "infraestructura operativa; 1 de 1 instancias responden") {
		t.Errorf("resumen = %v", lines)
	}
}

func TestDescribeChangesWithoutChangesIsEmpty(t *testing.T) {
	if lines := describeChanges(baseSnapshot(), baseSnapshot()); len(lines) != 0 {
		t.Errorf("sin cambios se esperaban 0 líneas, hubo %v", lines)
	}
}

func TestDescribeChangesReportsFailures(t *testing.T) {
	prev := baseSnapshot()
	curr := baseSnapshot()
	curr.Components[0] = Component{Name: "Servidor DNS · Bind9", OK: false, Detail: "No responde consultas"}
	curr.VMs = map[string]bool{"paisajes": false, "debian ns": true}
	curr.Instances = map[string]InstanceStatus{"paisajes": InstanceUnreachable}

	want := []string{
		"[estado] Servidor DNS · Bind9: falla (No responde consultas)",
		"[estado] infraestructura con fallas",
		"[estado] VM paisajes: apagada",
		"[estado] instancia paisajes: Apache no responde",
	}
	if got := describeChanges(prev, curr); !slices.Equal(got, want) {
		t.Errorf("cambios =\n%v\nse esperaba\n%v", got, want)
	}
}

func TestDescribeChangesReportsRecovery(t *testing.T) {
	prev := baseSnapshot()
	prev.Components[0].OK = false
	prev.Instances["paisajes"] = InstanceUnreachable

	got := describeChanges(prev, baseSnapshot())

	for _, want := range []string{
		"[estado] Servidor DNS · Bind9: recuperado (Resuelve ns.cloud.local)",
		"[estado] infraestructura operativa",
		"[estado] instancia paisajes: Apache responde",
	} {
		if !slices.Contains(got, want) {
			t.Errorf("falta %q en %v", want, got)
		}
	}
}
