package install

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func waitState(t *testing.T, r *Runner, id, want string) Job {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var last Job
	for time.Now().Before(deadline) {
		job, ok := r.Get(id)
		if ok {
			last = job
			if job.State == want {
				return job
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("job %s state %q, want %q", id, last.State, want)
	return Job{}
}

func TestJobStateMachine(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	var mu sync.Mutex
	var audits []string
	r := New(Deps{
		Log: log,
		Audit: func(action, target, result string) {
			mu.Lock()
			audits = append(audits, action+"|"+target+"|"+result)
			mu.Unlock()
		},
	})
	if _, err := r.Start("missing"); !errors.Is(err, ErrUnknown) {
		t.Fatalf("unknown install: %v", err)
	}

	hold := make(chan struct{})
	r.Register("libvirt", Spec{
		Label: "libvirt / KVM",
		Apt:   true,
		Run: func(ctx context.Context, rep Reporter) error {
			rep.Step("Installing packages")
			rep.Line("Reading package lists")
			rep.Line("Reading package lists")
			<-hold
			return nil
		},
	})
	job, err := r.Start("libvirt")
	if err != nil {
		t.Fatal(err)
	}
	if job.State != Queued && job.State != Running {
		t.Fatalf("start state %s", job.State)
	}
	if job.CreatedAt.IsZero() {
		t.Fatal("missing created time")
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		got, ok := r.Get(job.ID)
		if ok && got.State == Running && contains(got.Log, "Reading package lists") {
			if got.StartedAt.IsZero() {
				t.Fatal("running job has no start time")
			}
			if got.Step != "Installing packages" {
				t.Fatalf("step %q", got.Step)
			}
			n := 0
			for _, line := range got.Log {
				if line == "Reading package lists" {
					n++
				}
			}
			if n != 1 {
				t.Fatalf("duplicate log lines: %v", got.Log)
			}
			break
		}
		if time.Now().After(deadline) {
			got, _ := r.Get(job.ID)
			t.Fatalf("did not reach running with a log: %+v", got)
		}
		time.Sleep(5 * time.Millisecond)
	}
	close(hold)
	done := waitState(t, r, job.ID, Succeeded)
	if done.FinishedAt.IsZero() || done.FinishedAt.Before(done.StartedAt) {
		t.Fatalf("finish time %+v", done)
	}
	if !contains(done.Log, "Finished.") {
		t.Fatalf("log %v", done.Log)
	}

	r.Register("docker", Spec{
		Label: "Docker",
		Run: func(ctx context.Context, rep Reporter) error {
			rep.Line("boom-line")
			return errors.New("could not install docker")
		},
	})
	failedJob, err := r.Start("docker")
	if err != nil {
		t.Fatal(err)
	}
	failed := waitState(t, r, failedJob.ID, Failed)
	if !strings.Contains(failed.Error, "could not install docker") {
		t.Fatalf("error %q", failed.Error)
	}
	if failed.StartedAt.IsZero() {
		t.Fatal("failed job has no start time")
	}
	if !contains(failed.Log, "boom-line") {
		t.Fatalf("log %v", failed.Log)
	}
	again, err := r.Start("docker")
	if err != nil || again.ID == failed.ID {
		t.Fatalf("retry id %s err %v", again.ID, err)
	}

	waitFor(t, func() bool {
		text := buf.String()
		return strings.Contains(text, "install started") &&
			strings.Contains(text, "install finished") &&
			strings.Contains(text, "install failed")
	})
	mu.Lock()
	got := strings.Join(audits, "\n")
	mu.Unlock()
	for _, want := range []string{
		"install started|libvirt|ok",
		"install finished|libvirt|ok",
		"install failed|docker|error",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("audit missing %s\n%s", want, got)
		}
	}
}

func TestDoubleStartAndAptLock(t *testing.T) {
	r := New(Deps{})
	entered := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	r.Register("libvirt", Spec{
		Label: "libvirt",
		Apt:   true,
		Run: func(ctx context.Context, rep Reporter) error {
			calls.Add(1)
			close(entered)
			<-release
			return nil
		},
	})
	second := make(chan struct{})
	r.Register("docker", Spec{
		Label: "Docker",
		Apt:   true,
		Run: func(ctx context.Context, rep Reporter) error {
			close(second)
			rep.Line("docker ran")
			return nil
		},
	})

	first, err := r.Start("libvirt")
	if err != nil {
		t.Fatal(err)
	}
	<-entered
	again, err := r.Start("libvirt")
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != first.ID {
		t.Fatalf("double start created %s and %s", first.ID, again.ID)
	}
	if calls.Load() != 1 {
		t.Fatalf("apt started %d times", calls.Load())
	}

	other, err := r.Start("docker")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-second:
		t.Fatal("second apt ran while the first still held the lock")
	case <-time.After(40 * time.Millisecond):
	}
	if got, _ := r.Get(other.ID); got.State != Queued {
		t.Fatalf("waiting job state %s, want queued", got.State)
	}
	close(release)
	<-second
	if calls.Load() != 1 {
		t.Fatalf("first install ran %d times", calls.Load())
	}
	waitState(t, r, other.ID, Succeeded)
	got, _ := r.Get(other.ID)
	if !contains(got.Log, "docker ran") {
		t.Fatalf("second install log %v", got.Log)
	}
}

func TestNonAptRunsWhileAptIsLocked(t *testing.T) {
	r := New(Deps{})
	hold := make(chan struct{})
	r.Register("libvirt", Spec{
		Apt: true,
		Run: func(ctx context.Context, rep Reporter) error {
			<-hold
			return nil
		},
	})
	ran := make(chan struct{})
	r.Register("cloudflared", Spec{
		Run: func(ctx context.Context, rep Reporter) error {
			close(ran)
			return nil
		},
	})
	if _, err := r.Start("libvirt"); err != nil {
		t.Fatal(err)
	}
	other, err := r.Start("cloudflared")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-ran:
	case <-time.After(time.Second):
		t.Fatal("a download waited behind apt")
	}
	waitState(t, r, other.ID, Succeeded)
	close(hold)
}

func TestAptLockReleasedAfterFailure(t *testing.T) {
	r := New(Deps{})
	r.Register("samba", Spec{
		Apt: true,
		Run: func(ctx context.Context, rep Reporter) error {
			return errors.New("apt failed")
		},
	})
	r.Register("nfs", Spec{
		Apt: true,
		Run: func(ctx context.Context, rep Reporter) error {
			rep.Line("nfs ran")
			return nil
		},
	})
	first, err := r.Start("samba")
	if err != nil {
		t.Fatal(err)
	}
	waitState(t, r, first.ID, Failed)
	next, err := r.Start("nfs")
	if err != nil {
		t.Fatal(err)
	}
	done := waitState(t, r, next.ID, Succeeded)
	if !contains(done.Log, "nfs ran") {
		t.Fatalf("lock was not released: %+v", done)
	}
}

func TestRunSplitsLines(t *testing.T) {
	var mu sync.Mutex
	var lines []string
	ctx := WithHooks(context.Background(), nil, func(s string) {
		mu.Lock()
		lines = append(lines, s)
		mu.Unlock()
	})
	text, err := Run(ctx, 5*time.Second, "sh", "-c", "printf 'alpha\\nbeta\\rgamma\\n'")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "alpha") || !strings.Contains(text, "gamma") {
		t.Fatalf("combined %q", text)
	}
	mu.Lock()
	joined := strings.Join(lines, "\n")
	mu.Unlock()
	if !strings.Contains(joined, "alpha") || !strings.Contains(joined, "beta") || !strings.Contains(joined, "gamma") {
		t.Fatalf("streamed %q", joined)
	}
}

func TestStartSpecQuietSkipsGenericAudit(t *testing.T) {
	var mu sync.Mutex
	var audits []string
	r := New(Deps{Audit: func(action, target, result string) {
		mu.Lock()
		audits = append(audits, action+"|"+target+"|"+result)
		mu.Unlock()
	}})
	job, err := r.StartSpec("stack:demo:deploy", Spec{
		Label: "Deploy demo",
		Quiet: true,
		Run: func(ctx context.Context, rep Reporter) error {
			rep.Step("Starting the stack")
			rep.Line("demo is up.")
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	done := waitState(t, r, job.ID, Succeeded)
	if done.Step != "Finished" || !contains(done.Log, "demo is up.") {
		t.Fatalf("job %+v", done)
	}
	again, err := r.StartSpec("stack:demo:stop", Spec{
		Quiet: true,
		Run:   func(context.Context, Reporter) error { return errors.New("did not stop") },
	})
	if err != nil {
		t.Fatal(err)
	}
	failed := waitState(t, r, again.ID, Failed)
	if failed.Error == "" {
		t.Fatal("missing error")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(audits) != 0 {
		t.Fatalf("quiet job wrote generic install audit: %v", audits)
	}
}

func contains(lines []string, want string) bool {
	for _, line := range lines {
		if line == want {
			return true
		}
	}
	return false
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met")
}
