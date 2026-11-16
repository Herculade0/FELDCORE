package storage

import (
	"context"
	"strings"
	"time"

	"github.com/Herculade0/feldcore/internal/install"
)

// RunJob queues a scrub, balance or trim on the install job runner.
// A second request for the same pool and kind joins the job already running.
func (s *Service) RunJob(ctx context.Context, pool, kind string) (install.Job, error) {
	pool = strings.TrimSpace(pool)
	kind = strings.ToLower(strings.TrimSpace(kind))
	rec, err := s.prepJob(ctx, pool, kind)
	if err != nil {
		s.note(ctx, "storage."+kind, pool, "error")
		return install.Job{}, err
	}
	jobs := s.jobber()
	if jobs == nil {
		s.note(ctx, "storage."+kind, pool, "error")
		return install.Job{}, input("The job runner is not available.")
	}
	name := jobName(kind, rec.Name)
	if existing, ok := jobs.Latest(name); ok && (existing.State == install.Queued || existing.State == install.Running) {
		return existing, nil
	}
	label := kind + " " + rec.Name
	jobs.Register(name, install.Spec{
		Label: label,
		Run: func(ctx context.Context, rep install.Reporter) error {
			s.opMu.Lock()
			defer s.opMu.Unlock()
			err := s.maintain(ctx, rep, rec, kind)
			if err != nil {
				s.note(ctx, "storage."+kind, rec.Name, "error")
				s.finishSchedule(rec.Name, kind, "error")
				return err
			}
			s.note(ctx, "storage."+kind, rec.Name, "ok")
			s.finishSchedule(rec.Name, kind, "ok")
			return nil
		},
	})
	job, err := jobs.Start(name)
	if err != nil {
		s.note(ctx, "storage."+kind, pool, "error")
		return install.Job{}, err
	}
	return job, nil
}

func (s *Service) prepJob(ctx context.Context, pool, kind string) (poolRec, error) {
	switch kind {
	case KindScrub, KindBalance, KindTrim:
	default:
		return poolRec{}, input("Choose scrub, balance or trim.")
	}
	rec, ok := s.snapshot().pool(pool)
	if !ok {
		return poolRec{}, input("That pool does not exist.")
	}
	if _, _, err := MaintArgs(rec.FS, kind, rec.Name, rec.Mount, s.run.Look("xfs_scrub")); err != nil {
		return poolRec{}, err
	}
	if rec.FS != FSZFS {
		info := s.mountInfo(ctx, rec.Mount)
		if !info.Mounted || !strings.EqualFold(info.UUID, rec.UUID) {
			return poolRec{}, input("Mount the pool before running " + kind + ".")
		}
	}
	return rec, nil
}

func (s *Service) maintain(ctx context.Context, rep install.Reporter, rec poolRec, kind string) error {
	// Re-check under the lock. A pool can be removed between queue and start.
	cur, ok := s.snapshot().pool(rec.Name)
	if !ok {
		return input("That pool does not exist.")
	}
	rec = cur
	if rec.FS != FSZFS {
		info := s.mountInfo(ctx, rec.Mount)
		if !info.Mounted || !strings.EqualFold(info.UUID, rec.UUID) {
			return input("Mount the pool before running " + kind + ".")
		}
	}
	bin, args, err := MaintArgs(rec.FS, kind, rec.Name, rec.Mount, s.run.Look("xfs_scrub"))
	if err != nil {
		return err
	}
	rep.Step(stepTitle(kind, rec.Name))
	timeout := 24 * time.Hour
	if kind == KindTrim && rec.FS != FSZFS {
		timeout = 30 * time.Minute
	}
	if _, err := s.cmd(ctx, timeout, bin, args...); err != nil {
		return err
	}
	if rec.FS == FSZFS && (kind == KindScrub || kind == KindTrim) {
		return s.waitZFS(ctx, rep, rec.Name)
	}
	return nil
}

func stepTitle(kind, pool string) string {
	switch kind {
	case KindScrub:
		return "Scrubbing " + pool
	case KindBalance:
		return "Balancing " + pool
	default:
		return "Trimming " + pool
	}
}

func (s *Service) waitZFS(ctx context.Context, rep install.Reporter, pool string) error {
	deadline := time.Now().Add(24 * time.Hour)
	for {
		out, err := s.run.Run(ctx, 20*time.Second, "zpool", "status", "-P", pool)
		if err != nil {
			return err
		}
		if !zfsBusy(out) {
			rep.Line("Finished.")
			return nil
		}
		rep.Line("Still running.")
		if time.Now().After(deadline) {
			return op("The ZFS job did not finish within 24 hours.")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

// SetSchedule stores how often a job should run. The loop starts it on the
// job runner when it is due. Disabling a schedule does not stop a job that
// is already running.
func (s *Service) SetSchedule(ctx context.Context, in Schedule) (Schedule, error) {
	in.Pool = strings.TrimSpace(in.Pool)
	in.Kind = strings.ToLower(strings.TrimSpace(in.Kind))
	if err := validateSchedule(in.Kind, in.EveryHours, in.Enabled); err != nil {
		s.note(ctx, "storage.schedule", in.Pool, "error")
		return Schedule{}, err
	}
	st := s.snapshot()
	rec, ok := st.pool(in.Pool)
	if !ok {
		s.note(ctx, "storage.schedule", in.Pool, "error")
		return Schedule{}, input("That pool does not exist.")
	}
	if _, _, err := MaintArgs(rec.FS, in.Kind, rec.Name, rec.Mount, s.run.Look("xfs_scrub")); err != nil {
		s.note(ctx, "storage.schedule", in.Pool, "error")
		return Schedule{}, err
	}
	now := s.now()
	var saved Schedule
	err := s.update(func(f *file) error {
		for i := range f.Schedules {
			if f.Schedules[i].Pool == in.Pool && f.Schedules[i].Kind == in.Kind {
				prev := f.Schedules[i]
				prev.Enabled = in.Enabled
				prev.EveryHours = in.EveryHours
				if in.Enabled && (prev.NextRun.IsZero() || prev.NextRun.Before(now)) {
					prev.NextRun = scheduleNext(now, in.EveryHours)
				}
				f.Schedules[i] = prev
				saved = prev
				return nil
			}
		}
		saved = Schedule{
			Pool: in.Pool, Kind: in.Kind, Enabled: in.Enabled, EveryHours: in.EveryHours,
		}
		if in.Enabled {
			saved.NextRun = scheduleNext(now, in.EveryHours)
		}
		f.Schedules = append(f.Schedules, saved)
		return nil
	})
	if err != nil {
		s.note(ctx, "storage.schedule", in.Pool, "error")
		return Schedule{}, err
	}
	s.note(ctx, "storage.schedule", in.Pool+" "+in.Kind, "ok")
	return saved, nil
}

func (s *Service) finishSchedule(pool, kind, result string) {
	now := s.now()
	_ = s.update(func(f *file) error {
		for i := range f.Schedules {
			if f.Schedules[i].Pool == pool && f.Schedules[i].Kind == kind {
				f.Schedules[i].LastRun = now
				f.Schedules[i].LastResult = result
				if f.Schedules[i].Enabled {
					f.Schedules[i].NextRun = scheduleNext(now, f.Schedules[i].EveryHours)
				}
			}
		}
		return nil
	})
}

// Loop starts due schedules. It returns when ctx is cancelled.
func (s *Service) Loop(ctx context.Context) {
	if s == nil {
		return
	}
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	s.tick(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.tick(ctx)
		}
	}
}

func (s *Service) tick(ctx context.Context) {
	due := dueSchedules(s.now(), s.snapshot().Schedules)
	for _, sch := range due {
		if _, err := s.RunJob(ctx, sch.Pool, sch.Kind); err != nil {
			s.finishSchedule(sch.Pool, sch.Kind, "error")
			if s.log != nil {
				s.log.Warn("scheduled storage job did not start", "pool", sch.Pool, "kind", sch.Kind, "err", err)
			}
		}
	}
}
