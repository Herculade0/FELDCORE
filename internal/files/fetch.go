package files

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"
)

const (
	StatusQueued    = "queued"
	StatusRunning   = "running"
	StatusDone      = "done"
	StatusError     = "error"
	StatusCancelled = "cancelled"
	StatusSkipped   = "skipped"

	maxFetch      = 64 << 30
	maxRedirects  = 5
	maxFetchJobs  = 4
	maxKeptJobs   = 30
	fetchLifetime = 6 * time.Hour
	stallTimeout  = 90 * time.Second
)

// Job is one URL download into a share.
type Job struct {
	ID      string    `json:"id"`
	Share   string    `json:"share"`
	Dir     string    `json:"dir"`
	Name    string    `json:"name"`
	Path    string    `json:"path"`
	URL     string    `json:"url"`
	Status  string    `json:"status"`
	Bytes   int64     `json:"bytes"`
	Total   int64     `json:"total"`
	Speed   float64   `json:"speed"`
	Error   string    `json:"error,omitempty"`
	Skipped bool      `json:"skipped,omitempty"`
	Started time.Time `json:"startedAt"`
	Actor   string    `json:"-"`
	TokenID string    `json:"-"`

	root   string
	raw    *url.URL
	cancel context.CancelFunc
	reason string

	mu      sync.Mutex
	samples []sample
}

type sample struct {
	t time.Time
	n int64
}

// View is a snapshot safe to encode while the download is running.
func (j *Job) View() Job {
	j.mu.Lock()
	defer j.mu.Unlock()
	return Job{
		ID: j.ID, Share: j.Share, Dir: j.Dir, Name: j.Name, Path: j.Path, URL: j.URL,
		Status: j.Status, Bytes: j.Bytes, Total: j.Total, Speed: j.Speed, Error: j.Error,
		Skipped: j.Skipped, Started: j.Started, Actor: j.Actor, TokenID: j.TokenID,
	}
}

func (j *Job) setStatus(status, errMsg string) {
	j.mu.Lock()
	j.Status = status
	j.Error = errMsg
	j.mu.Unlock()
}

func (j *Job) note(n int) {
	if n <= 0 {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.Bytes += int64(n)
	now := time.Now()
	j.samples = append(j.samples, sample{t: now, n: j.Bytes})
	cut := now.Add(-3 * time.Second)
	i := 0
	for i < len(j.samples) && j.samples[i].t.Before(cut) {
		i++
	}
	j.samples = j.samples[i:]
	if len(j.samples) >= 2 {
		first, last := j.samples[0], j.samples[len(j.samples)-1]
		dt := last.t.Sub(first.t).Seconds()
		if dt >= 0.05 {
			j.Speed = float64(last.n-first.n) / dt
		}
	}
}

// Request starts a download.
type Request struct {
	Root     string
	Share    string
	Dir      string
	URL      string
	Filename string
	Conflict string
	Actor    string
	TokenID  string
}

// Fetcher runs URL downloads in the background.
type Fetcher struct {
	allowLAN func() bool
	lookup   func(ctx context.Context, host string) ([]net.IP, error)
	dial     func(ctx context.Context, network, addr string) (net.Conn, error)
	OnSettle func(*Job)

	mu   sync.Mutex
	jobs []*Job
}

// NewFetcher returns a fetcher. allowLAN is read when each connection is dialed.
func NewFetcher(allowLAN func() bool) *Fetcher {
	if allowLAN == nil {
		allowLAN = func() bool { return false }
	}
	return &Fetcher{allowLAN: allowLAN}
}

// SetTestHooks replaces DNS and dialling. Tests use this so a download can
// succeed without touching a real network. Production leaves both nil.
func (f *Fetcher) SetTestHooks(lookup func(context.Context, string) ([]net.IP, error), dial func(context.Context, string, string) (net.Conn, error)) {
	f.lookup = lookup
	f.dial = dial
}

// Start validates the URL, applies the conflict policy, and downloads in the
// background. A blocked URL or a conflict returns before anything is written.
func (f *Fetcher) Start(req Request) (*Job, error) {
	u, err := ValidateURL(req.URL)
	if err != nil {
		return nil, err
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil {
		if err := CheckIP(ip, f.allowLAN()); err != nil {
			return nil, err
		}
	}
	dir, err := cleanUser(req.Dir)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(req.Filename)
	if name == "" {
		name = nameFromURL(u)
	}
	if err := validComponent(name); err != nil {
		return nil, &InputError{Msg: "Choose a file name without slashes."}
	}
	if name == UploadsDir || name == TrashDir {
		return nil, &InputError{Msg: "That name is reserved."}
	}
	rel := joinRel(dir, name)
	vol, err := OpenVolume(req.Root)
	if err != nil {
		return nil, err
	}
	if err := vol.Mkdir(dir); err != nil && dir != "." {
		return nil, err
	}
	final, skip, err := vol.planWrite(rel, orConflict(req.Conflict))
	if err != nil || skip {
		if skip {
			now := time.Now()
			return &Job{
				Share: req.Share, Dir: dir, Name: path.Base(final), Path: final,
				URL: SafeURL(u), Status: StatusSkipped, Skipped: true, Started: now,
				Total: -1, Actor: req.Actor, TokenID: req.TokenID,
			}, nil
		}
		return nil, err
	}
	f.mu.Lock()
	running := 0
	for _, j := range f.jobs {
		if j.Status == StatusRunning || j.Status == StatusQueued {
			running++
		}
	}
	if running >= maxFetchJobs {
		f.mu.Unlock()
		return nil, &InputError{Msg: "Too many downloads are already running. Cancel one first."}
	}
	id, err := newID()
	if err != nil {
		f.mu.Unlock()
		return nil, &OpError{Msg: "The download could not be started.", Err: err}
	}
	ctx, cancel := context.WithTimeout(context.Background(), fetchLifetime)
	job := &Job{
		ID: id, Share: req.Share, Dir: dir, Name: path.Base(final), Path: final,
		URL: SafeURL(u), Status: StatusQueued, Total: -1, Started: time.Now(),
		Actor: req.Actor, TokenID: req.TokenID,
		root: vol.Root(), raw: u, cancel: cancel,
	}
	f.jobs = append(f.jobs, job)
	f.trimLocked()
	f.mu.Unlock()
	go f.run(ctx, job)
	return job.ViewPtr(), nil
}

// ViewPtr returns the live job. Callers that need a stable snapshot use View.
func (j *Job) ViewPtr() *Job { return j }

// Get returns one job for a share.
func (f *Fetcher) Get(share, id string) (Job, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, j := range f.jobs {
		if j.ID == id && j.Share == share {
			return j.View(), true
		}
	}
	return Job{}, false
}

// List returns the jobs for one share, newest first.
func (f *Fetcher) List(share string) []Job {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Job, 0, len(f.jobs))
	for i := len(f.jobs) - 1; i >= 0; i-- {
		if f.jobs[i].Share == share {
			out = append(out, f.jobs[i].View())
		}
	}
	return out
}

// Cancel stops a running download and removes the partial file.
func (f *Fetcher) Cancel(share, id string) error {
	f.mu.Lock()
	var job *Job
	for _, j := range f.jobs {
		if j.ID == id && j.Share == share {
			job = j
			break
		}
	}
	f.mu.Unlock()
	if job == nil {
		return ErrNotFound
	}
	job.mu.Lock()
	status := job.Status
	job.reason = "cancelled"
	cancel := job.cancel
	job.mu.Unlock()
	if status == StatusDone || status == StatusError || status == StatusCancelled || status == StatusSkipped {
		return nil
	}
	if cancel != nil {
		cancel()
	}
	return nil
}

func (f *Fetcher) trimLocked() {
	if len(f.jobs) <= maxKeptJobs {
		return
	}
	drop := len(f.jobs) - maxKeptJobs
	kept := make([]*Job, 0, maxKeptJobs)
	for _, j := range f.jobs {
		active := j.Status == StatusRunning || j.Status == StatusQueued
		if !active && drop > 0 {
			drop--
			continue
		}
		kept = append(kept, j)
	}
	f.jobs = kept
}

func (f *Fetcher) run(ctx context.Context, job *Job) {
	defer func() {
		if f.OnSettle != nil {
			snap := job.View()
			f.OnSettle(&snap)
		}
	}()
	job.setStatus(StatusRunning, "")
	err := f.download(ctx, job)
	job.mu.Lock()
	reason := job.reason
	job.mu.Unlock()
	if reason == "stalled" {
		job.setStatus(StatusError, "The download stalled before it finished.")
		f.removePartial(job)
		return
	}
	if reason == "cancelled" || errors.Is(err, context.Canceled) {
		job.setStatus(StatusCancelled, "Cancelled.")
		f.removePartial(job)
		return
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		job.setStatus(StatusError, "The download took too long and was stopped.")
		f.removePartial(job)
		return
	}
	if err != nil {
		msg := "The download failed. The server could not fetch that URL."
		var forbidden *ForbiddenURL
		var input *InputError
		if errors.As(err, &forbidden) || errors.As(err, &input) {
			msg = err.Error()
		}
		job.setStatus(StatusError, msg)
		f.removePartial(job)
		return
	}
	job.setStatus(StatusDone, "")
}

func (f *Fetcher) download(ctx context.Context, job *Job) error {
	vol, err := OpenVolume(job.root)
	if err != nil {
		return err
	}
	id := job.ID
	meta := partMeta{Rel: job.Path, Size: maxFetch, Created: time.Now().UTC()}
	if err := vol.savePart(id, meta); err != nil {
		return err
	}
	client := &http.Client{
		Transport: &http.Transport{
			Proxy:                 nil,
			DialContext:           f.dialContext,
			TLSHandshakeTimeout:   15 * time.Second,
			ResponseHeaderTimeout: 45 * time.Second,
			DisableKeepAlives:     true,
			// A redirect to a blocked address is checked in CheckRedirect and
			// again by DialContext, so a public name cannot bounce to metadata.
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return &ForbiddenURL{Msg: "That URL redirected too many times."}
			}
			if _, err := ValidateURL(req.URL.String()); err != nil {
				return err
			}
			if ip := net.ParseIP(req.URL.Hostname()); ip != nil {
				if err := CheckIP(ip, f.allowLAN()); err != nil {
					return err
				}
			}
			return nil
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, job.raw.String(), nil)
	if err != nil {
		return &InputError{Msg: "That URL is not valid."}
	}
	req.Header.Set("User-Agent", "FELDCORE")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return &InputError{Msg: "The server at that URL did not return the file."}
	}
	total := int64(-1)
	if resp.ContentLength >= 0 {
		if resp.ContentLength > maxFetch {
			return &InputError{Msg: "That file is too large to download."}
		}
		total = resp.ContentLength
	}
	job.mu.Lock()
	job.Total = total
	job.Name = path.Base(job.Path)
	job.mu.Unlock()

	stall, stopStall := context.WithCancel(ctx)
	defer stopStall()
	go f.watchStall(stall, job)

	body := &progressReader{r: resp.Body, note: job.note}
	var offset int64
	buf := make([]byte, 256<<10)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, rerr := body.Read(buf)
		if n > 0 {
			wrote, werr := vol.appendPart(id, offset, bytes.NewReader(buf[:n]), int64(n), maxFetch)
			if werr != nil {
				return werr
			}
			offset = wrote
			if offset > maxFetch {
				return &InputError{Msg: "That file is too large to download."}
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return rerr
		}
	}
	if total >= 0 && offset != total {
		return &InputError{Msg: "The download ended before the whole file arrived."}
	}
	_, err = vol.commitPart(id, job.Path)
	return err
}

func (f *Fetcher) watchStall(ctx context.Context, job *Job) {
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	var last int64
	quiet := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			job.mu.Lock()
			n := job.Bytes
			job.mu.Unlock()
			if n == last {
				quiet++
				if quiet >= int(stallTimeout/(15*time.Second)) {
					job.mu.Lock()
					job.reason = "stalled"
					cancel := job.cancel
					job.mu.Unlock()
					if cancel != nil {
						cancel()
					}
					return
				}
			} else {
				quiet = 0
				last = n
			}
		}
	}
}

func (f *Fetcher) removePartial(job *Job) {
	vol, err := OpenVolume(job.root)
	if err != nil {
		return
	}
	_ = vol.discardPart(job.ID)
}

func (f *Fetcher) dialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, &ForbiddenURL{Msg: msgBad}
	}
	ips, err := f.resolve(ctx, host)
	if err != nil || len(ips) == 0 {
		return nil, &ForbiddenURL{Msg: "That address could not be looked up."}
	}
	allow := f.allowLAN()
	for _, ip := range ips {
		if err := CheckIP(ip, allow); err != nil {
			return nil, err
		}
	}
	target := net.JoinHostPort(ips[0].String(), port)
	if f.dial != nil {
		return f.dial(ctx, network, target)
	}
	var d net.Dialer
	d.Timeout = 30 * time.Second
	return d.DialContext(ctx, network, target)
}

func (f *Fetcher) resolve(ctx context.Context, host string) ([]net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		return []net.IP{ip}, nil
	}
	if f.lookup != nil {
		return f.lookup(ctx, host)
	}
	return net.DefaultResolver.LookupIP(ctx, "ip", host)
}

func orConflict(c string) string {
	if c == "" {
		return "fail"
	}
	return c
}

func nameFromURL(u *url.URL) string {
	if u == nil {
		return "download"
	}
	base := path.Base(u.Path)
	base = strings.TrimSpace(base)
	if base == "" || base == "." || base == "/" {
		return "download"
	}
	if err := validComponent(base); err != nil {
		return "download"
	}
	return base
}

type progressReader struct {
	r    io.Reader
	note func(int)
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	if n > 0 && p.note != nil {
		p.note(n)
	}
	return n, err
}
