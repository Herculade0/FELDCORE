// Package health reads the machine and turns the readings into green, amber,
// and red checks. Samples for the graphs live in a ring file under the data
// directory, not in Prometheus and not in state.json.
package health

import (
	"time"

	"github.com/Herculade0/feldcore/internal/store"
)

// Level is the colour of one check.
type Level string

const (
	OK      Level = "ok"
	Warn    Level = "warn"
	Crit    Level = "crit"
	Unknown Level = "unknown"
)

// Rank orders levels. Unknown stays below ok so a missing tool does not
// paint the whole machine red.
func Rank(l Level) int {
	switch l {
	case Crit:
		return 3
	case Warn:
		return 2
	case OK:
		return 1
	default:
		return 0
	}
}

// Attribute is one SMART field worth showing.
type Attribute struct {
	Name      string `json:"name"`
	Value     string `json:"value,omitempty"`
	Raw       string `json:"raw,omitempty"`
	Threshold int    `json:"threshold,omitempty"`
}

// Check is one row on the health page.
type Check struct {
	ID         string      `json:"id"`
	Category   string      `json:"category"`
	Name       string      `json:"name"`
	Level      Level       `json:"level"`
	Summary    string      `json:"summary"`
	Value      float64     `json:"value,omitempty"`
	Unit       string      `json:"unit,omitempty"`
	Attributes []Attribute `json:"attributes,omitempty"`
}

// Report is the latest pass.
type Report struct {
	Overall   Level     `json:"overall"`
	Summary   string    `json:"summary"`
	CheckedAt time.Time `json:"checkedAt"`
	Checks    []Check   `json:"checks"`
}

// Thresholds are the marks a reading is compared with. Zero in the stored
// form means "use the default" until it is filled in.
type Thresholds struct {
	CPUWarn, CPUCrit           float64
	MemWarn, MemCrit           float64
	LoadWarn, LoadCrit         float64
	DiskWarn, DiskCrit         float64
	CPUTempWarn, CPUTempCrit   float64
	DiskTempWarn, DiskTempCrit float64
	GPUTempWarn, GPUTempCrit   float64
	CertWarnDays, CertCritDays int
	RestartWarn, RestartCrit   int
	RestartWindow              time.Duration
}

// Defaults are the marks used when an administrator has not chosen any.
func Defaults() Thresholds {
	return Thresholds{
		CPUWarn: 85, CPUCrit: 95,
		MemWarn: 85, MemCrit: 95,
		LoadWarn: 1.5, LoadCrit: 3,
		DiskWarn: 80, DiskCrit: 92,
		CPUTempWarn: 80, CPUTempCrit: 90,
		DiskTempWarn: 50, DiskTempCrit: 60,
		GPUTempWarn: 85, GPUTempCrit: 95,
		CertWarnDays: 30, CertCritDays: 7,
		RestartWarn: 3, RestartCrit: 8,
		RestartWindow: 10 * time.Minute,
	}
}

// Effective fills any unset stored mark with the default.
func Effective(in store.HealthThresholds) Thresholds {
	t := Defaults()
	set := func(dst *float64, v float64) {
		if v > 0 {
			*dst = v
		}
	}
	set(&t.CPUWarn, in.CPUWarn)
	set(&t.CPUCrit, in.CPUCrit)
	set(&t.MemWarn, in.MemWarn)
	set(&t.MemCrit, in.MemCrit)
	set(&t.LoadWarn, in.LoadWarn)
	set(&t.LoadCrit, in.LoadCrit)
	set(&t.DiskWarn, in.DiskWarn)
	set(&t.DiskCrit, in.DiskCrit)
	set(&t.CPUTempWarn, in.CPUTempWarn)
	set(&t.CPUTempCrit, in.CPUTempCrit)
	set(&t.DiskTempWarn, in.DiskTempWarn)
	set(&t.DiskTempCrit, in.DiskTempCrit)
	set(&t.GPUTempWarn, in.GPUTempWarn)
	set(&t.GPUTempCrit, in.GPUTempCrit)
	if in.CertWarnDays > 0 {
		t.CertWarnDays = in.CertWarnDays
	}
	if in.CertCritDays > 0 {
		t.CertCritDays = in.CertCritDays
	}
	if in.RestartWarn > 0 {
		t.RestartWarn = in.RestartWarn
	}
	if in.RestartCrit > 0 {
		t.RestartCrit = in.RestartCrit
	}
	if in.RestartWindowMin > 0 {
		t.RestartWindow = time.Duration(in.RestartWindowMin) * time.Minute
	}
	return t
}

// ToStore is the JSON form of a fully filled threshold set.
func ToStore(t Thresholds) store.HealthThresholds {
	return store.HealthThresholds{
		CPUWarn: t.CPUWarn, CPUCrit: t.CPUCrit,
		MemWarn: t.MemWarn, MemCrit: t.MemCrit,
		LoadWarn: t.LoadWarn, LoadCrit: t.LoadCrit,
		DiskWarn: t.DiskWarn, DiskCrit: t.DiskCrit,
		CPUTempWarn: t.CPUTempWarn, CPUTempCrit: t.CPUTempCrit,
		DiskTempWarn: t.DiskTempWarn, DiskTempCrit: t.DiskTempCrit,
		GPUTempWarn: t.GPUTempWarn, GPUTempCrit: t.GPUTempCrit,
		CertWarnDays: t.CertWarnDays, CertCritDays: t.CertCritDays,
		RestartWarn: t.RestartWarn, RestartCrit: t.RestartCrit,
		RestartWindowMin: int(t.RestartWindow / time.Minute),
	}
}

// InputError is a threshold the page can show as-is.
type InputError struct{ Msg string }

func (e *InputError) Error() string { return e.Msg }

// Validate checks a saved set. Empty fields are the defaults.
func Validate(in store.HealthThresholds) error {
	t := Effective(in)
	pair := func(lo, hi float64, what string, max float64) error {
		if lo <= 0 || hi <= 0 || lo > max || hi > max {
			return &InputError{Msg: what + " must sit between 1 and the top of the scale."}
		}
		if lo >= hi {
			return &InputError{Msg: "The " + what + " warning must be lower than the critical mark."}
		}
		return nil
	}
	if err := pair(t.CPUWarn, t.CPUCrit, "processor", 100); err != nil {
		return err
	}
	if err := pair(t.MemWarn, t.MemCrit, "memory", 100); err != nil {
		return err
	}
	if err := pair(t.LoadWarn, t.LoadCrit, "load", 64); err != nil {
		return err
	}
	if err := pair(t.DiskWarn, t.DiskCrit, "disk", 100); err != nil {
		return err
	}
	if err := pair(t.CPUTempWarn, t.CPUTempCrit, "processor temperature", 125); err != nil {
		return err
	}
	if err := pair(t.DiskTempWarn, t.DiskTempCrit, "disk temperature", 125); err != nil {
		return err
	}
	if err := pair(t.GPUTempWarn, t.GPUTempCrit, "GPU temperature", 125); err != nil {
		return err
	}
	if t.CertCritDays < 1 || t.CertWarnDays < 1 || t.CertWarnDays > 3650 || t.CertCritDays > 3650 {
		return &InputError{Msg: "Certificate windows are between 1 and 3650 days."}
	}
	if t.CertWarnDays <= t.CertCritDays {
		return &InputError{Msg: "Warn about a certificate before it reaches the critical window."}
	}
	if t.RestartWarn < 1 || t.RestartCrit < 1 || t.RestartWarn > 1000 || t.RestartCrit > 1000 {
		return &InputError{Msg: "Restart counts are between 1 and 1000."}
	}
	if t.RestartWarn >= t.RestartCrit {
		return &InputError{Msg: "The restart warning must be lower than the critical count."}
	}
	mins := int(t.RestartWindow / time.Minute)
	if mins < 1 || mins > 1440 {
		return &InputError{Msg: "The restart window is between 1 minute and 1 day."}
	}
	return nil
}

// UnavailableError means a tool is missing or a subsystem is switched off.
// The check is shown as unknown rather than failed.
type UnavailableError struct{ Msg string }

func (e *UnavailableError) Error() string { return e.Msg }

// Unavailable builds an UnavailableError.
func Unavailable(msg string) error { return &UnavailableError{Msg: msg} }

// HostSample is processor, memory, and load.
type HostSample struct {
	CPUPercent float64
	MemPercent float64
	MemUsed    uint64
	MemTotal   uint64
	Load1      float64
	Cores      int
}

// Space is one filesystem or one pool.
type Space struct {
	Name        string
	Kind        string // disk or pool
	Total       uint64
	Used        uint64
	UsedPercent float64
	Health      string // ONLINE, DEGRADED, FAULTED, ...
}

// Temp is one sensor in Celsius.
type Temp struct {
	Name    string
	Kind    string // cpu, disk, gpu
	Celsius float64
}

// DiskSMART is one physical disk after smartctl, or a note that it could not be read.
type DiskSMART struct {
	Device      string
	Model       string
	Available   bool
	Asleep      bool
	Passed      *bool
	Message     string
	TempC       *float64
	Reallocated int64
	Pending     int64
	Offline     int64
	MediaErrors int64
	HasWear     bool
	Wear        int
	HasSpare    bool
	Spare       int
	SpareFloor  int
	Attributes  []Attribute
}

// ContainerSnap is one container's health, not its full config.
type ContainerSnap struct {
	Name             string
	State            string
	Health           string
	RestartsInWindow int
	OOMKilled        bool
}

// VMSnap is one virtual machine.
type VMSnap struct {
	Name  string
	State string
}

// Unit is a failed systemd unit.
type Unit struct {
	Name        string
	Description string
}

// Cert is a certificate file FELDCORE is willing to watch.
type Cert struct {
	Name     string
	NotAfter time.Time
}

// TunnelSnap is a Cloudflare or VPN tunnel that is turned on.
type TunnelSnap struct {
	Name   string
	Kind   string
	State  string
	Detail string
}

// Snapshot is everything one pass collected. Nil slices mean "not asked".
type Snapshot struct {
	Host       *HostSample
	Disks      []Space
	Temps      []Temp
	SMART      []DiskSMART
	Containers []ContainerSnap
	VMs        []VMSnap
	Units      []Unit
	Certs      []Cert
	Tunnels    []TunnelSnap
	Notes      []Check
	// Asked records which optional sections ran, so an empty answer can still
	// produce a green "none" row instead of disappearing.
	Asked map[string]bool
}
