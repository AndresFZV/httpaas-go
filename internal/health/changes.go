package health

import (
	"fmt"
	"log"
	"maps"
	"slices"
	"strings"
)

// logChanges registra lo que cambió entre dos verificaciones, para tener trazabilidad
// de caídas y recuperaciones aunque nadie esté mirando la interfaz.
func logChanges(prev, curr Snapshot) {
	for _, line := range describeChanges(prev, curr) {
		log.Print(line)
	}
}

// describeChanges devuelve una línea por cada cambio entre prev y curr.
// En la primera verificación devuelve un resumen del estado inicial.
func describeChanges(prev, curr Snapshot) []string {
	if !prev.Checked() {
		return []string{summary(curr)}
	}

	var lines []string

	before := make(map[string]Component, len(prev.Components))
	for _, c := range prev.Components {
		before[c.Name] = c
	}
	for _, c := range curr.Components {
		old, ok := before[c.Name]
		if !ok || old.OK == c.OK {
			continue
		}
		if c.OK {
			lines = append(lines, fmt.Sprintf("[estado] %s: recuperado (%s)", c.Name, c.Detail))
		} else {
			lines = append(lines, fmt.Sprintf("[estado] %s: falla (%s)", c.Name, c.Detail))
		}
	}

	if prev.Operational() != curr.Operational() {
		if curr.Operational() {
			lines = append(lines, "[estado] infraestructura operativa")
		} else {
			lines = append(lines, "[estado] infraestructura con fallas")
		}
	}

	if prev.VMsKnown && curr.VMsKnown {
		for _, name := range unionKeys(prev.VMs, curr.VMs) {
			if prev.VMs[name] == curr.VMs[name] {
				continue
			}
			state := "apagada"
			if curr.VMs[name] {
				state = "encendida"
			}
			lines = append(lines, fmt.Sprintf("[estado] VM %s: %s", name, state))
		}
	}

	for _, name := range slices.Sorted(maps.Keys(curr.Instances)) {
		old, ok := prev.Instances[name]
		now := curr.Instances[name]
		if !ok || old == now {
			continue
		}
		if now == InstanceActive {
			lines = append(lines, fmt.Sprintf("[estado] instancia %s: Apache responde", name))
		} else {
			lines = append(lines, fmt.Sprintf("[estado] instancia %s: Apache no responde", name))
		}
	}

	return lines
}

// summary describe el estado completo, para la primera verificación.
func summary(s Snapshot) string {
	state := "operativa"
	if !s.Operational() {
		var failing []string
		for _, c := range s.Components {
			if !c.OK {
				failing = append(failing, c.Name)
			}
		}
		state = "con fallas (" + strings.Join(failing, ", ") + ")"
	}

	active := 0
	for _, status := range s.Instances {
		if status == InstanceActive {
			active++
		}
	}
	return fmt.Sprintf("[estado] primera verificación: infraestructura %s; %d de %d instancias responden",
		state, active, len(s.Instances))
}

// unionKeys devuelve las claves de los dos mapas, ordenadas y sin repetir.
func unionKeys(a, b map[string]bool) []string {
	keys := make(map[string]bool, len(a)+len(b))
	for k := range a {
		keys[k] = true
	}
	for k := range b {
		keys[k] = true
	}
	return slices.Sorted(maps.Keys(keys))
}
