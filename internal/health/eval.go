package health

import (
	"fmt"
	"strings"
	"time"
)

// Eval turns a snapshot into checks. It does not touch the machine.
func Eval(snap Snapshot, th Thresholds, now time.Time) Report {
	if now.IsZero() {
		now = time.Now()
	}
	th = fill(th)
	var checks []Check
	checks = append(checks, snap.Notes...)
	if snap.Host != nil {
		checks = append(checks, evalHost(*snap.Host, th)...)
	}
	for _, d := range snap.Disks {
		checks = append(checks, evalSpace(d, th))
	}
	for _, t := range snap.Temps {
		checks = append(checks, evalTemp(t, th))
	}
	for _, d := range snap.SMART {
		checks = append(checks, evalSMART(d))
	}
	checks = append(checks, evalContainers(snap, th)...)
	checks = append(checks, evalVMs(snap)...)
	checks = append(checks, evalUnits(snap)...)
	checks = append(checks, evalCerts(snap, th, now)...)
	checks = append(checks, evalTunnels(snap)...)
	if checks == nil {
		checks = []Check{}
	}
	return Report{
		Overall:   overall(checks),
		Summary:   summarise(checks),
		CheckedAt: now,
		Checks:    checks,
	}
}

func fill(th Thresholds) Thresholds {
	d := Defaults()
	if th.CPUWarn == 0 {
		return d
	}
	if th.RestartWindow == 0 {
		th.RestartWindow = d.RestartWindow
	}
	return th
}

func evalHost(h HostSample, th Thresholds) []Check {
	cores := h.Cores
	if cores < 1 {
		cores = 1
	}
	per := h.Load1 / float64(cores)
	return []Check{
		percentCheck("cpu", "cpu", "Processor", h.CPUPercent, "%", th.CPUWarn, th.CPUCrit,
			fmt.Sprintf("The processor is at %.0f%%.", h.CPUPercent)),
		percentCheck("memory", "memory", "Memory", h.MemPercent, "%", th.MemWarn, th.MemCrit,
			fmt.Sprintf("Memory is at %.0f%%.", h.MemPercent)),
		percentCheck("load", "load", "Load", per, "per thread", th.LoadWarn, th.LoadCrit,
			fmt.Sprintf("Load is %.2f across %d threads (%.2f per thread).", h.Load1, cores, per)),
	}
}

func percentCheck(id, cat, name string, value float64, unit string, warn, crit float64, calm string) Check {
	c := Check{ID: id, Category: cat, Name: name, Value: round1(value), Unit: unit, Level: OK, Summary: calm}
	switch {
	case value >= crit:
		c.Level = Crit
		c.Summary = fmt.Sprintf("%s is at %.0f%s, past the critical mark of %.0f.", name, value, unitSuffix(unit), crit)
	case value >= warn:
		c.Level = Warn
		c.Summary = fmt.Sprintf("%s is at %.0f%s, past the warning mark of %.0f.", name, value, unitSuffix(unit), warn)
	}
	if unit == "per thread" {
		if c.Level == OK {
			return c
		}
		mark := warn
		if c.Level == Crit {
			mark = crit
		}
		c.Summary = fmt.Sprintf("Load is %.2f per thread, past the %s mark of %.1f.", value, word(c.Level), mark)
	}
	return c
}

func unitSuffix(unit string) string {
	if unit == "%" {
		return "%"
	}
	return ""
}

func evalSpace(d Space, th Thresholds) Check {
	name := d.Name
	if name == "" {
		name = "disk"
	}
	id := d.Kind + ":" + name
	if d.Kind == "" {
		d.Kind = "disk"
	}
	c := Check{
		ID: id, Category: "space", Name: name, Level: OK,
		Value: round1(d.UsedPercent), Unit: "%",
		Summary: fmt.Sprintf("%s is %.0f%% full.", name, d.UsedPercent),
	}
	health := strings.ToUpper(strings.TrimSpace(d.Health))
	switch health {
	case "FAULTED", "UNAVAIL", "REMOVED":
		c.Level = Crit
		c.Summary = fmt.Sprintf("Pool %s is %s.", name, strings.ToLower(health))
		return c
	case "DEGRADED":
		c.Level = Warn
		c.Summary = fmt.Sprintf("Pool %s is degraded and %.0f%% full.", name, d.UsedPercent)
	case "OFFLINE":
		c.Level = Warn
		c.Summary = fmt.Sprintf("Pool %s is offline.", name)
	}
	switch {
	case d.UsedPercent >= th.DiskCrit && Rank(c.Level) < Rank(Crit):
		c.Level = Crit
		c.Summary = fmt.Sprintf("%s is %.0f%% full, past the critical mark of %.0f%%.", name, d.UsedPercent, th.DiskCrit)
	case d.UsedPercent >= th.DiskWarn && Rank(c.Level) < Rank(Warn):
		c.Level = Warn
		c.Summary = fmt.Sprintf("%s is %.0f%% full, past the warning mark of %.0f%%.", name, d.UsedPercent, th.DiskWarn)
	}
	return c
}

func evalTemp(t Temp, th Thresholds) Check {
	warn, crit := th.CPUTempWarn, th.CPUTempCrit
	label := "Processor"
	switch t.Kind {
	case "disk":
		warn, crit = th.DiskTempWarn, th.DiskTempCrit
		label = "Disk"
	case "gpu":
		warn, crit = th.GPUTempWarn, th.GPUTempCrit
		label = "GPU"
	}
	name := t.Name
	if name == "" {
		name = label
	}
	c := Check{
		ID: "temp:" + t.Kind + ":" + name, Category: "temp", Name: name, Level: OK,
		Value: round1(t.Celsius), Unit: "°C",
		Summary: fmt.Sprintf("%s is %.0f°C.", name, t.Celsius),
	}
	switch {
	case t.Celsius >= crit:
		c.Level = Crit
		c.Summary = fmt.Sprintf("%s is %.0f°C, past the critical mark of %.0f°C.", name, t.Celsius, crit)
	case t.Celsius >= warn:
		c.Level = Warn
		c.Summary = fmt.Sprintf("%s is %.0f°C, past the warning mark of %.0f°C.", name, t.Celsius, warn)
	}
	return c
}

func evalSMART(d DiskSMART) Check {
	name := d.Device
	if name == "" {
		name = "disk"
	}
	c := Check{ID: "smart:" + name, Category: "smart", Name: name, Attributes: d.Attributes}
	if !d.Available {
		c.Level = Unknown
		c.Summary = d.Message
		if c.Summary == "" {
			c.Summary = "SMART could not be read for " + name + "."
		}
		return c
	}
	if d.Asleep {
		c.Level = Unknown
		c.Summary = name + " is asleep, so SMART was not read. The disk was not woken up."
		return c
	}
	c.Level = OK
	c.Summary = name + " passed SMART."
	if d.Model != "" {
		c.Summary = name + " (" + d.Model + ") passed SMART."
	}
	if d.Passed != nil && !*d.Passed {
		c.Level = Crit
		c.Summary = name + " failed SMART."
	}
	if d.Pending > 0 || d.Offline > 0 || d.MediaErrors > 0 {
		c.Level = Crit
		c.Summary = fmt.Sprintf("%s has pending sectors or media errors (pending %d, offline %d, media %d).", name, d.Pending, d.Offline, d.MediaErrors)
	}
	if d.HasSpare && d.SpareFloor > 0 && d.Spare <= d.SpareFloor && Rank(c.Level) < Rank(Crit) {
		c.Level = Crit
		c.Summary = fmt.Sprintf("%s has %d%% spare, at or below the %d%% floor.", name, d.Spare, d.SpareFloor)
	}
	if d.HasWear && d.Wear >= 98 && Rank(c.Level) < Rank(Crit) {
		c.Level = Crit
		c.Summary = fmt.Sprintf("%s reports %d%% of its endurance used.", name, d.Wear)
	}
	if c.Level == OK && d.Reallocated > 0 {
		c.Level = Warn
		c.Summary = fmt.Sprintf("%s has %d reallocated sectors.", name, d.Reallocated)
	}
	if c.Level == OK && d.HasWear && d.Wear >= 90 {
		c.Level = Warn
		c.Summary = fmt.Sprintf("%s reports %d%% of its endurance used.", name, d.Wear)
	}
	if d.TempC != nil {
		c.Value = round1(*d.TempC)
		c.Unit = "°C"
	}
	return c
}

func evalContainers(snap Snapshot, th Thresholds) []Check {
	if !snap.Asked["containers"] {
		return nil
	}
	var bad []Check
	running := 0
	for _, c := range snap.Containers {
		if c.Name == "" {
			continue
		}
		if c.State == "running" {
			running++
		}
		health := strings.ToLower(c.Health)
		if health == "unhealthy" {
			bad = append(bad, Check{
				ID: "container:" + c.Name + ":health", Category: "container", Name: c.Name, Level: Crit,
				Summary: c.Name + " is unhealthy.",
			})
		}
		if c.OOMKilled && c.State != "running" {
			bad = append(bad, Check{
				ID: "container:" + c.Name + ":oom", Category: "container", Name: c.Name, Level: Crit,
				Summary: c.Name + " was stopped because it ran out of memory.",
			})
		}
		if c.RestartsInWindow > 0 || health == "restarting" || c.State == "restarting" {
			level := OK
			switch {
			case c.RestartsInWindow >= th.RestartCrit || (c.State == "restarting" && c.RestartsInWindow >= th.RestartCrit):
				level = Crit
			case c.RestartsInWindow >= th.RestartWarn || c.State == "restarting" || health == "restarting":
				level = Warn
			}
			if level != OK {
				bad = append(bad, Check{
					ID: "container:" + c.Name + ":restart", Category: "container", Name: c.Name, Level: level,
					Value: float64(c.RestartsInWindow), Unit: "restarts",
					Summary: fmt.Sprintf("%s restarted %d times in %d minutes.", c.Name, max(c.RestartsInWindow, 1), int(th.RestartWindow/time.Minute)),
				})
			}
		}
	}
	summary := Check{
		ID: "containers", Category: "container", Name: "Containers", Level: OK,
		Summary: fmt.Sprintf("%d running, none unhealthy.", running),
	}
	if len(snap.Containers) == 0 {
		summary.Summary = "Docker is running and there are no containers."
	}
	if len(bad) > 0 {
		summary.Level = worst(bad)
		summary.Summary = fmt.Sprintf("%d container problem%s.", len(bad), plural(len(bad)))
	}
	return append([]Check{summary}, bad...)
}

func evalVMs(snap Snapshot) []Check {
	if !snap.Asked["vms"] {
		return nil
	}
	var out []Check
	for _, v := range snap.VMs {
		if v.Name == "" {
			continue
		}
		state := strings.ToLower(strings.TrimSpace(v.State))
		c := Check{ID: "vm:" + v.Name, Category: "vm", Name: v.Name, Level: OK, Summary: v.Name + " is " + state + "."}
		switch state {
		case "running":
			c.Summary = v.Name + " is running."
		case "shut off", "shutdown", "shutoff":
			c.Summary = v.Name + " is shut off."
		case "paused", "pmsuspended":
			c.Level = Warn
			c.Summary = v.Name + " is paused."
		case "crashed", "blocked":
			c.Level = Crit
			c.Summary = v.Name + " has crashed."
		case "":
			c.Level = Unknown
			c.Summary = v.Name + " did not report a state."
		default:
			c.Summary = v.Name + " is " + state + "."
		}
		out = append(out, c)
	}
	if len(out) == 0 {
		out = append(out, Check{ID: "vms", Category: "vm", Name: "Virtual machines", Level: OK, Summary: "No virtual machines."})
	}
	return out
}

func evalUnits(snap Snapshot) []Check {
	if !snap.Asked["systemd"] {
		return nil
	}
	if len(snap.Units) == 0 {
		return []Check{{ID: "systemd", Category: "systemd", Name: "Services", Level: OK, Summary: "No failed systemd units."}}
	}
	var out []Check
	n := len(snap.Units)
	limit := n
	if limit > 25 {
		limit = 25
	}
	for _, u := range snap.Units[:limit] {
		desc := u.Description
		if desc == "" {
			desc = u.Name
		}
		out = append(out, Check{
			ID: "systemd:" + u.Name, Category: "systemd", Name: u.Name, Level: Crit,
			Summary: u.Name + " failed. " + desc + ".",
		})
	}
	if n > limit {
		out = append(out, Check{
			ID: "systemd:more", Category: "systemd", Name: "Services", Level: Crit,
			Summary: fmt.Sprintf("%d more failed units are not listed.", n-limit),
		})
	}
	return out
}

func evalCerts(snap Snapshot, th Thresholds, now time.Time) []Check {
	if !snap.Asked["certs"] {
		return nil
	}
	if len(snap.Certs) == 0 {
		return []Check{{ID: "certs", Category: "cert", Name: "Certificates", Level: OK, Summary: "No certificates are stored for FELDCORE to watch yet."}}
	}
	var out []Check
	for _, cert := range snap.Certs {
		days := int(cert.NotAfter.Sub(now).Hours() / 24)
		c := Check{
			ID: "cert:" + cert.Name, Category: "cert", Name: cert.Name, Level: OK,
			Value: float64(days), Unit: "days",
			Summary: fmt.Sprintf("%s expires in %d days.", cert.Name, days),
		}
		switch {
		case cert.NotAfter.Before(now):
			c.Level = Crit
			c.Summary = cert.Name + " has expired."
			c.Value = 0
		case days <= th.CertCritDays:
			c.Level = Crit
			c.Summary = fmt.Sprintf("%s expires in %d days, inside the critical window of %d.", cert.Name, days, th.CertCritDays)
		case days <= th.CertWarnDays:
			c.Level = Warn
			c.Summary = fmt.Sprintf("%s expires in %d days, inside the warning window of %d.", cert.Name, days, th.CertWarnDays)
		}
		out = append(out, c)
	}
	return out
}

func evalTunnels(snap Snapshot) []Check {
	if !snap.Asked["tunnels"] {
		return nil
	}
	if len(snap.Tunnels) == 0 {
		return []Check{{ID: "tunnels", Category: "tunnel", Name: "Tunnels", Level: OK, Summary: "No tunnels are turned on."}}
	}
	var out []Check
	for _, tun := range snap.Tunnels {
		name := tun.Name
		if name == "" {
			name = tun.Kind
		}
		state := strings.ToLower(strings.TrimSpace(tun.State))
		c := Check{ID: "tunnel:" + tun.Kind + ":" + name, Category: "tunnel", Name: name, Level: OK}
		switch state {
		case "up", "healthy":
			c.Summary = name + " is connected."
		case "starting":
			c.Level = Warn
			c.Summary = name + " is still starting."
		case "off":
			c.Summary = name + " is off."
		case "unknown":
			c.Level = Unknown
			c.Summary = name + " could not be checked."
		default:
			c.Level = Crit
			c.Summary = name + " is down."
		}
		if tun.Detail != "" && c.Level != OK {
			c.Summary = c.Summary + " " + oneLine(tun.Detail, 180)
		}
		out = append(out, c)
	}
	return out
}

func overall(checks []Check) Level {
	best := Unknown
	seen := false
	for _, c := range checks {
		if c.Level == Unknown || c.Level == "" {
			continue
		}
		seen = true
		if Rank(c.Level) > Rank(best) {
			best = c.Level
		}
	}
	if !seen {
		return Unknown
	}
	return best
}

func summarise(checks []Check) string {
	crit, warn, unknown := 0, 0, 0
	for _, c := range checks {
		switch c.Level {
		case Crit:
			crit++
		case Warn:
			warn++
		case Unknown:
			unknown++
		}
	}
	switch {
	case crit > 0 && warn > 0:
		return fmt.Sprintf("%d critical, %d warning.", crit, warn)
	case crit > 0:
		return fmt.Sprintf("%d critical.", crit)
	case warn > 0:
		return fmt.Sprintf("%d warning.", warn)
	case unknown > 0:
		return fmt.Sprintf("Nothing is over its limit. %d check%s could not be read.", unknown, plural(unknown))
	default:
		return "All watched checks are healthy."
	}
}

func worst(checks []Check) Level {
	l := OK
	for _, c := range checks {
		if Rank(c.Level) > Rank(l) {
			l = c.Level
		}
	}
	return l
}

func word(l Level) string {
	switch l {
	case Crit:
		return "critical"
	case Warn:
		return "warning"
	default:
		return "healthy"
	}
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func round1(v float64) float64 {
	if v < 0 {
		return float64(int(v*10-0.5)) / 10
	}
	return float64(int(v*10+0.5)) / 10
}

func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		return s[:n]
	}
	return s
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
