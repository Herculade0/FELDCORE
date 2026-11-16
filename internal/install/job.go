// Package install tracks one-shot package installs as background jobs.
// The work runs after the HTTP request returns, so a refresh can ask for the
// same job and keep showing its log. Apt and dpkg share one lock: a second
// install waits, and a second click on the same install joins the job that
// is already queued or running.
package install

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	Queued    = "queued"
	Running   = "running"
	Succeeded = "succeeded"
	Failed    = "failed"

	maxLogLines = 1000
	tailLines   = 300
)

// ErrUnknown is returned when Start is asked for an install that was not
// registered.
var ErrUnknown = errors.New("unknown install")

// Reporter is how a task names its current step and adds log lines.
type Reporter interface {
	Step(name string)
	Line(text string)
}

// Spec is one kind of install. Apt is true when the task runs apt or dpkg,
// so it takes the package-manager lock.
type Spec struct {
	Label string
	Apt   bool
	Run   func(ctx context.Context, rep Reporter) error
}

// Job is the view a page polls. Log is the tail, not the whole history.
type Job struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Label      string    `json:"label"`
	State      string    `json:"state"`
	Step       string    `json:"step"`
	CreatedAt  time.Time `json:"createdAt"`
	StartedAt  time.Time `json:"startedAt,omitempty"`
	FinishedAt time.Time `json:"finishedAt,omitempty"`
	Error      string    `json:"error,omitempty"`
	Log        []string  `json:"log"`
}

type job struct {
	Job
	apt bool
	run func(context.Context, Reporter) error
}

// AuditFunc records one install event. The HTTP layer writes the audit log.
type AuditFunc func(action, target, result string)

// Deps wires logging. Both may be nil.
type Deps struct {
	Log   *slog.Logger
	Audit AuditFunc
	Now   func() time.Time
}

// Runner is the in-memory set of install jobs for this process.
type Runner struct {
	log   *slog.Logger
	audit AuditFunc
	now   func() time.Time

	mu     sync.Mutex
	apt    sync.Mutex
	jobs   map[string]*job
	latest map[string]string
	specs  map[string]Spec
}

// New returns an empty runner.
func New(d Deps) *Runner {
	if d.Log == nil {
		d.Log = slog.Default()
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	return &Runner{
		log: d.Log, audit: d.Audit, now: d.Now,
		jobs: map[string]*job{}, latest: map[string]string{}, specs: map[string]Spec{},
	}
}

// Register adds or replaces one install. name is what the UI sends.
func (r *Runner) Register(name string, spec Spec) {
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	if spec.Label == "" {
		spec.Label = name
	}
	r.mu.Lock()
	r.specs[name] = spec
	r.mu.Unlock()
}

// Start queues name, or returns the job already queued or running for it.
// A second call does not start a second apt run.
func (r *Runner) Start(name string) (Job, error) {
	name = strings.TrimSpace(name)
	r.mu.Lock()
	defer r.mu.Unlock()
	spec, ok := r.specs[name]
	if !ok {
		return Job{}, ErrUnknown
	}
	if id, ok := r.latest[name]; ok {
		if existing := r.jobs[id]; existing != nil && (existing.State == Queued || existing.State == Running) {
			return existing.view(), nil
		}
	}
	j := &job{
		Job: Job{
			ID:        newID(),
			Name:      name,
			Label:     spec.Label,
			State:     Queued,
			Step:      "Queued",
			CreatedAt: r.now(),
			Log:       []string{},
		},
		apt: spec.Apt,
		run: spec.Run,
	}
	r.jobs[j.ID] = j
	r.latest[name] = j.ID
	go r.execute(j)
	return j.view(), nil
}

// Get returns one job by id.
func (r *Runner) Get(id string) (Job, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	j, ok := r.jobs[id]
	if !ok {
		return Job{}, false
	}
	return j.view(), true
}

// Latest returns the newest job for name, including one that already finished.
func (r *Runner) Latest(name string) (Job, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	id, ok := r.latest[strings.TrimSpace(name)]
	if !ok {
		return Job{}, false
	}
	j := r.jobs[id]
	if j == nil {
		return Job{}, false
	}
	return j.view(), true
}

// Current returns the newest job of each name.
func (r *Runner) Current() []Job {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Job, 0, len(r.latest))
	for _, id := range r.latest {
		if j := r.jobs[id]; j != nil {
			out = append(out, j.view())
		}
	}
	return out
}

func (r *Runner) execute(j *job) {
	if j.apt {
		if !r.apt.TryLock() {
			r.setStep(j, "Waiting for the package manager")
			r.append(j, "Waiting for another install to finish.")
			r.apt.Lock()
		}
		defer r.apt.Unlock()
	}
	r.markRunning(j)
	defer func() {
		if rec := recover(); rec != nil {
			r.finish(j, fmt.Errorf("the install stopped unexpectedly: %v", rec))
		}
	}()
	if j.run == nil {
		r.finish(j, errors.New("install is not configured"))
		return
	}
	rep := reporter{step: func(s string) { r.setStep(j, s) }, line: func(s string) { r.append(j, s) }}
	ctx := WithHooks(context.Background(), rep.step, rep.line)
	r.finish(j, j.run(ctx, rep))
}

func (r *Runner) markRunning(j *job) {
	r.mu.Lock()
	j.State = Running
	j.StartedAt = r.now()
	if j.Step == "" || j.Step == "Queued" || j.Step == "Waiting for the package manager" {
		j.Step = "Starting"
	}
	r.mu.Unlock()
	r.append(j, "Starting "+j.Label+".")
	r.log.Info("install started", "name", j.Name, "id", j.ID)
	r.note("install started", j.Name, "ok")
}

func (r *Runner) finish(j *job, err error) {
	r.mu.Lock()
	if j.State == Succeeded || j.State == Failed {
		r.mu.Unlock()
		return
	}
	j.FinishedAt = r.now()
	if err != nil {
		j.State = Failed
		j.Error = clip(err.Error())
		j.Step = "Failed"
		msg := j.Error
		name, id := j.Name, j.ID
		r.mu.Unlock()
		r.append(j, msg)
		r.log.Error("install failed", "name", name, "id", id, "err", msg)
		r.note("install failed", name, "error")
		return
	}
	j.State = Succeeded
	j.Step = "Finished"
	name, id := j.Name, j.ID
	r.mu.Unlock()
	r.append(j, "Finished.")
	r.log.Info("install finished", "name", name, "id", id)
	r.note("install finished", name, "ok")
}

func (r *Runner) note(action, target, result string) {
	if r.audit != nil {
		r.audit(action, target, result)
	}
}

func (r *Runner) setStep(j *job, step string) {
	step = strings.TrimSpace(step)
	if step == "" {
		return
	}
	r.mu.Lock()
	j.Step = step
	r.mu.Unlock()
}

func (r *Runner) append(j *job, line string) {
	line = cleanLine(line)
	if line == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if n := len(j.Log); n > 0 && j.Log[n-1] == line {
		return
	}
	j.Log = append(j.Log, line)
	if len(j.Log) > maxLogLines {
		j.Log = append([]string(nil), j.Log[len(j.Log)-maxLogLines:]...)
	}
}

func (j *job) view() Job {
	out := j.Job
	if len(j.Log) > tailLines {
		out.Log = append([]string(nil), j.Log[len(j.Log)-tailLines:]...)
	} else {
		out.Log = append([]string(nil), j.Log...)
	}
	if out.Log == nil {
		out.Log = []string{}
	}
	return out
}

type reporter struct {
	step func(string)
	line func(string)
}

func (r reporter) Step(name string) { r.step(name) }
func (r reporter) Line(text string) { r.line(text) }

func newID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func clip(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 400 {
		return "…" + s[len(s)-399:]
	}
	return s
}

var ansiRE = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)

func cleanLine(s string) string {
	s = ansiRE.ReplaceAllString(s, "")
	s = strings.TrimSpace(s)
	if len(s) > 2000 {
		s = s[:2000]
	}
	return s
}
