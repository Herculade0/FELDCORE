package backup

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// CLI is the restic binary. DataDir is where the cache and a downloaded
// binary live. Bin overrides the lookup, which tests use.
type CLI struct {
	DataDir string
	Bin     string
}

func (c *CLI) Available() error {
	_, err := c.binary()
	return err
}

func (c *CLI) binary() (string, error) {
	if c != nil && c.Bin != "" {
		if st, err := os.Stat(c.Bin); err == nil && !st.IsDir() {
			return c.Bin, nil
		}
	}
	if p, err := exec.LookPath("restic"); err == nil {
		return p, nil
	}
	if c != nil && c.DataDir != "" {
		p := filepath.Join(c.DataDir, "bin", "restic")
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
	}
	return "", input("restic is not installed. Install it from the Backups page, or install the restic package.")
}

func (c *CLI) Ensure(ctx context.Context, repo Repo) error {
	out, err := c.run(ctx, repo, []string{"snapshots", "--json"}, nil)
	if err == nil || needsInit(out) {
		if err == nil {
			return nil
		}
		_, err = c.run(ctx, repo, []string{"init"}, nil)
		return err
	}
	return err
}

func (c *CLI) Probe(ctx context.Context, repo Repo) error {
	out, err := c.run(ctx, repo, []string{"snapshots", "--json"}, nil)
	if err == nil || needsInit(out) {
		return nil
	}
	return err
}

func (c *CLI) Backup(ctx context.Context, repo Repo, req BackupReq, on ProgressFunc) (Summary, error) {
	if len(req.Paths) == 0 {
		return Summary{}, input("Nothing was found to back up.")
	}
	args := []string{"backup", "--json", "--one-file-system"}
	for _, tag := range req.Tags {
		args = append(args, "--tag", tag)
	}
	for _, ex := range req.Excludes {
		if strings.TrimSpace(ex) != "" {
			args = append(args, "--exclude", ex)
		}
	}
	args = append(args, req.Paths...)
	var sum Summary
	_, err := c.run(ctx, repo, args, func(p Progress) {
		if p.SnapshotID != "" {
			sum.SnapshotID = p.SnapshotID
		}
		if p.BytesDone > 0 {
			sum.Bytes = p.BytesDone
		}
		if p.FilesDone > 0 {
			sum.Files = p.FilesDone
		}
		if on != nil {
			on(p)
		}
	})
	return sum, err
}

func (c *CLI) Forget(ctx context.Context, repo Repo, destID string, policy Retention) error {
	args, ok := forgetArgs(destID, policy)
	if !ok {
		return nil
	}
	_, err := c.run(ctx, repo, args, nil)
	return err
}

func (c *CLI) Check(ctx context.Context, repo Repo, readData bool, on ProgressFunc) error {
	args := []string{"check"}
	if readData {
		args = append(args, "--read-data-subset=10%")
	}
	_, err := c.run(ctx, repo, args, on)
	return err
}

func (c *CLI) Snapshots(ctx context.Context, repo Repo, destID string) ([]Snapshot, error) {
	args := []string{"snapshots", "--json", "--tag", "feldcore"}
	if destID != "" {
		args = append(args, "--tag", "dest:"+destID)
	}
	out, err := c.run(ctx, repo, args, nil)
	if err != nil {
		if needsInit(out) {
			return []Snapshot{}, nil
		}
		return nil, err
	}
	return parseSnapshots(out)
}

func (c *CLI) List(ctx context.Context, repo Repo, snap string) ([]Node, error) {
	if !snapRe.MatchString(snap) {
		return nil, input("That copy was not recognised.")
	}
	var nodes []Node
	_, err := c.run(ctx, repo, []string{"ls", snap, "--json"}, func(p Progress) {
		if p.Line == "" {
			return
		}
		if n, ok := parseNode(p.Line); ok {
			if len(nodes) < 100000 {
				nodes = append(nodes, n)
			}
		}
	})
	if err != nil {
		return nil, err
	}
	return nodes, nil
}

func (c *CLI) Restore(ctx context.Context, repo Repo, snap string, paths []string, target string, on ProgressFunc) error {
	if !snapRe.MatchString(snap) {
		return input("That copy was not recognised.")
	}
	args := []string{"restore", snap, "--target", target}
	for _, p := range paths {
		args = append(args, "--include", p)
	}
	_, err := c.run(ctx, repo, args, on)
	return err
}

func (c *CLI) run(ctx context.Context, repo Repo, args []string, on ProgressFunc) (string, error) {
	bin, err := c.binary()
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(repo.Password) == "" {
		return "", input("This backup has no repository password yet.")
	}
	argv := []string{bin}
	if repo.SFTPArgs != "" {
		argv = append(argv, "-o", "sftp.args="+repo.SFTPArgs)
	}
	argv = append(argv, args...)
	env := c.env(repo)
	var buf strings.Builder
	hidden := append([]string{repo.Password}, repo.Hidden...)
	runErr := stream(ctx, argv, env, func(line string) {
		line = secretsRedact(line, hidden)
		buf.WriteString(line)
		buf.WriteByte('\n')
		if on == nil {
			return
		}
		if p, ok := parseProgress(line); ok {
			on(p)
			return
		}
		on(Progress{Line: line})
	})
	out := secretsRedact(buf.String(), hidden)
	if runErr != nil {
		return out, op(friendlyRestic(out))
	}
	return out, nil
}

func (c *CLI) env(repo Repo) []string {
	env := []string{
		"RESTIC_PASSWORD=" + repo.Password,
		"RESTIC_REPOSITORY=" + repo.Location,
		"RESTIC_PROGRESS_FPS=1",
	}
	if c != nil && c.DataDir != "" {
		env = append(env, "RESTIC_CACHE_DIR="+filepath.Join(c.DataDir, "restic-cache"))
	}
	if repo.RcloneConfig != "" {
		env = append(env, "RCLONE_CONFIG="+repo.RcloneConfig)
	}
	return env
}

func stream(ctx context.Context, argv, env []string, onLine func(string)) error {
	if len(argv) == 0 {
		return op("restic is not installed.")
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Env = mergeEnv(os.Environ(), env)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	var wg sync.WaitGroup
	var lineMu sync.Mutex
	scan := func(r io.Reader) {
		defer wg.Done()
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			lineMu.Lock()
			onLine(sc.Text())
			lineMu.Unlock()
		}
	}
	wg.Add(2)
	go scan(stdout)
	go scan(stderr)
	wg.Wait()
	return cmd.Wait()
}

func mergeEnv(base, extra []string) []string {
	drop := map[string]bool{}
	for _, e := range extra {
		k, _, _ := strings.Cut(e, "=")
		drop[k] = true
	}
	out := make([]string, 0, len(base)+len(extra))
	for _, e := range base {
		k, _, _ := strings.Cut(e, "=")
		if drop[k] {
			continue
		}
		out = append(out, e)
	}
	return append(out, extra...)
}

func needsInit(out string) bool {
	if strings.Contains(out, "wrong password") || strings.Contains(out, "no key found") {
		return false
	}
	return strings.Contains(out, "repository does not exist") || strings.Contains(out, "Is there a repository")
}

func friendlyRestic(out string) string {
	if msg := lastJSONMessage(out); msg != "" {
		return clipErr(msg)
	}
	if line := lastNonEmpty(out); line != "" {
		return clipErr(line)
	}
	return "restic failed"
}

func lastJSONMessage(out string) string {
	lines := strings.Split(out, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var m struct {
			Message string `json:"message"`
			Error   struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal([]byte(line), &m) != nil {
			continue
		}
		if m.Error.Message != "" {
			return m.Error.Message
		}
		if m.Message != "" {
			return m.Message
		}
	}
	return ""
}

func lastNonEmpty(out string) string {
	lines := strings.Split(out, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			return line
		}
	}
	return ""
}

func parseProgress(line string) (Progress, bool) {
	var m struct {
		MessageType string  `json:"message_type"`
		PercentDone float64 `json:"percent_done"`
		TotalFiles  int64   `json:"total_files"`
		FilesDone   int64   `json:"files_done"`
		TotalBytes  int64   `json:"total_bytes"`
		BytesDone   int64   `json:"bytes_done"`
		SnapshotID  string  `json:"snapshot_id"`
		Message     string  `json:"message"`
		Item        string  `json:"item"`
		Error       struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal([]byte(line), &m) != nil || m.MessageType == "" {
		return Progress{}, false
	}
	p := Progress{
		Percent: m.PercentDone * 100, BytesDone: m.BytesDone, BytesTotal: m.TotalBytes,
		FilesDone: m.FilesDone, FilesTotal: m.TotalFiles, SnapshotID: m.SnapshotID, Line: line,
	}
	if m.MessageType == "summary" {
		p.Percent = 100
		if p.BytesDone == 0 {
			p.BytesDone = m.TotalBytes
		}
	}
	if m.MessageType == "error" || m.MessageType == "exit_error" {
		p.Err = m.Error.Message
		if p.Err == "" {
			p.Err = m.Message
		}
		if m.Item != "" && p.Err != "" {
			p.Err = p.Err + " (" + m.Item + ")"
		}
	}
	return p, true
}

func parseNode(line string) (Node, bool) {
	var n struct {
		Name       string `json:"name"`
		Type       string `json:"type"`
		Path       string `json:"path"`
		Size       int64  `json:"size"`
		Mtime      string `json:"mtime"`
		StructType string `json:"struct_type"`
	}
	if json.Unmarshal([]byte(line), &n) != nil {
		return Node{}, false
	}
	if n.StructType == "snapshot" || n.Path == "" {
		return Node{}, false
	}
	if n.Type != "file" && n.Type != "dir" {
		return Node{}, false
	}
	node := Node{Name: n.Name, Type: n.Type, Path: n.Path, Size: n.Size}
	if t, err := parseResticTime(n.Mtime); err == nil {
		node.Mtime = t
	}
	return node, true
}

func parseSnapshots(out string) ([]Snapshot, error) {
	start := strings.Index(out, "[")
	if start < 0 {
		return []Snapshot{}, nil
	}
	var raw []struct {
		ID       string   `json:"id"`
		ShortID  string   `json:"short_id"`
		Time     string   `json:"time"`
		Hostname string   `json:"hostname"`
		Paths    []string `json:"paths"`
		Tags     []string `json:"tags"`
	}
	if err := json.Unmarshal([]byte(out[start:]), &raw); err != nil {
		return nil, op("Could not read the list of copies.")
	}
	outSnaps := make([]Snapshot, 0, len(raw))
	for _, r := range raw {
		s := Snapshot{ID: r.ID, ShortID: r.ShortID, Hostname: r.Hostname, Paths: r.Paths, Tags: r.Tags}
		if t, err := parseResticTime(r.Time); err == nil {
			s.Time = t
		}
		outSnaps = append(outSnaps, s)
	}
	return outSnaps, nil
}

func parseResticTime(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t, nil
	}
	return time.Parse(time.RFC3339, s)
}

func forgetArgs(destID string, p Retention) ([]string, bool) {
	if p.Daily <= 0 && p.Weekly <= 0 && p.Monthly <= 0 {
		return nil, false
	}
	args := []string{"forget", "--prune", "--json", "--tag", "feldcore", "--tag", "dest:" + destID}
	if p.Daily > 0 {
		args = append(args, "--keep-daily", strconv.Itoa(p.Daily))
	}
	if p.Weekly > 0 {
		args = append(args, "--keep-weekly", strconv.Itoa(p.Weekly))
	}
	if p.Monthly > 0 {
		args = append(args, "--keep-monthly", strconv.Itoa(p.Monthly))
	}
	return args, true
}

func secretsRedact(msg string, hidden []string) string {
	for _, h := range hidden {
		if len(h) < 8 {
			continue
		}
		msg = strings.ReplaceAll(msg, h, "[redacted]")
	}
	return msg
}
