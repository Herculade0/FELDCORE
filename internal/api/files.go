package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/Herculade0/feldcore/internal/files"
	"github.com/Herculade0/feldcore/internal/shares"
	"github.com/Herculade0/feldcore/internal/store"
)

func (s *Server) openShare(name string) (*files.Volume, string, error) {
	if s.shares == nil {
		return nil, "", shares.ErrNotFound
	}
	name = strings.TrimSpace(name)
	for _, folder := range s.shares.Folders() {
		if folder.Name == name {
			vol, err := files.OpenVolume(folder.Path)
			return vol, name, err
		}
	}
	return nil, "", shares.ErrNotFound
}

func (s *Server) shareVol(w http.ResponseWriter, r *http.Request) (*files.Volume, string, bool) {
	vol, name, err := s.openShare(chi.URLParam(r, "name"))
	if err != nil {
		s.fail(w, r, err)
		return nil, "", false
	}
	return vol, name, true
}

func (s *Server) handleFileList(w http.ResponseWriter, r *http.Request) {
	vol, _, ok := s.shareVol(w, r)
	if !ok {
		return
	}
	list, err := vol.List(r.URL.Query().Get("path"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleFileContent(w http.ResponseWriter, r *http.Request) {
	vol, _, ok := s.shareVol(w, r)
	if !ok {
		return
	}
	f, info, err := vol.Open(r.URL.Query().Get("path"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	defer f.Close()
	ctype, inline := sniffFile(info.Name(), r.URL.Query().Get("preview") == "1", f, info.Size())
	disp := "attachment"
	if inline && r.URL.Query().Get("download") != "1" {
		disp = "inline"
	}
	h := w.Header()
	h.Set("Content-Type", ctype)
	h.Set("Content-Disposition", disp+`; filename="`+headerFilename(info.Name())+`"`)
	h.Set("Cache-Control", "private, no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, info.Name(), info.ModTime(), f)
}

func (s *Server) handleFileZip(w http.ResponseWriter, r *http.Request) {
	vol, name, ok := s.shareVol(w, r)
	if !ok {
		return
	}
	paths, err := zipPaths(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Choose a file or folder to download.")
		return
	}
	h := w.Header()
	h.Set("Content-Type", "application/zip")
	h.Set("Content-Disposition", `attachment; filename="`+headerFilename(name)+`.zip"`)
	h.Set("Cache-Control", "private, no-store")
	if err := vol.WriteZip(w, paths); err != nil && s.log != nil {
		s.log.Warn("zip failed", "share", name, "err", err)
	}
}

func (s *Server) handleFileMkdir(w http.ResponseWriter, r *http.Request) {
	vol, name, ok := s.shareVol(w, r)
	if !ok {
		return
	}
	var body struct {
		Path string `json:"path"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if err := vol.Mkdir(body.Path); err != nil {
		s.auditFile(r, "file mkdir", name+"/"+body.Path, "error")
		s.fail(w, r, err)
		return
	}
	s.auditFile(r, "file mkdir", name+"/"+body.Path, "ok")
	writeJSON(w, http.StatusOK, map[string]string{"path": body.Path})
}

func (s *Server) handleFileRename(w http.ResponseWriter, r *http.Request) {
	vol, name, ok := s.shareVol(w, r)
	if !ok {
		return
	}
	var body struct {
		Path string `json:"path"`
		Name string `json:"name"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	dest, err := vol.Rename(body.Path, body.Name)
	if err != nil {
		s.auditFile(r, "file rename", name+"/"+body.Path, "error")
		s.fail(w, r, err)
		return
	}
	s.auditFile(r, "file rename", name+"/"+dest, "ok")
	writeJSON(w, http.StatusOK, map[string]string{"path": dest})
}

func (s *Server) handleFileMove(w http.ResponseWriter, r *http.Request) {
	s.transfer(w, r, false)
}

func (s *Server) handleFileCopy(w http.ResponseWriter, r *http.Request) {
	s.transfer(w, r, true)
}

func (s *Server) transfer(w http.ResponseWriter, r *http.Request, copy bool) {
	vol, name, ok := s.shareVol(w, r)
	if !ok {
		return
	}
	var body struct {
		Paths    []string `json:"paths"`
		Dest     string   `json:"dest"`
		Conflict string   `json:"conflict"`
	}
	if !readJSONLimit(w, r, &body, 1<<20) {
		return
	}
	action := "file move"
	if copy {
		action = "file copy"
	}
	done, err := vol.Transfer(body.Paths, body.Dest, body.Conflict, copy)
	if err != nil {
		s.auditFile(r, action, name+"/"+body.Dest, "error")
		s.fail(w, r, err)
		return
	}
	for _, p := range done {
		s.auditFile(r, action, name+"/"+p, "ok")
	}
	writeJSON(w, http.StatusOK, map[string]any{"paths": done})
}

func (s *Server) handleFileDelete(w http.ResponseWriter, r *http.Request) {
	vol, name, ok := s.shareVol(w, r)
	if !ok {
		return
	}
	var body struct {
		Paths []string `json:"paths"`
	}
	if !readJSONLimit(w, r, &body, 1<<20) {
		return
	}
	gone, err := vol.Delete(body.Paths)
	if err != nil {
		s.auditFile(r, "file delete", name, "error")
		s.fail(w, r, err)
		return
	}
	for _, p := range gone {
		s.auditFile(r, "file delete", name+"/"+p, "ok")
	}
	writeJSON(w, http.StatusOK, map[string]any{"paths": gone})
}

func (s *Server) handleUploadCreate(w http.ResponseWriter, r *http.Request) {
	vol, name, ok := s.shareVol(w, r)
	if !ok {
		return
	}
	var body struct {
		Path     string `json:"path"`
		Size     int64  `json:"size"`
		Conflict string `json:"conflict"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	created, err := s.uploads.Create(vol.Root(), body.Path, body.Size, body.Conflict)
	if err != nil {
		// A conflict is the prompt the browser asked for, not a failed write.
		var conflict *files.ConflictError
		if !errors.As(err, &conflict) {
			s.auditFile(r, "file upload", name+"/"+body.Path, "error")
		}
		s.fail(w, r, err)
		return
	}
	if created.Complete && !created.Skipped {
		s.auditFile(r, "file upload", name+"/"+created.Path, "ok")
	}
	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) handleUploadPatch(w http.ResponseWriter, r *http.Request) {
	vol, name, ok := s.shareVol(w, r)
	if !ok {
		return
	}
	offset, err := parseOffset(r.Header.Get("Upload-Offset"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Say where this piece of the file continues from.")
		return
	}
	body := http.MaxBytesReader(w, r.Body, files.MaxChunk+1)
	buf, err := io.ReadAll(body)
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) || len(buf) > files.MaxChunk {
			writeError(w, http.StatusRequestEntityTooLarge, "That piece of the upload is too large.")
			return
		}
		s.fail(w, r, err)
		return
	}
	if len(buf) > files.MaxChunk {
		writeError(w, http.StatusRequestEntityTooLarge, "That piece of the upload is too large.")
		return
	}
	id := chi.URLParam(r, "id")
	done, err := s.uploads.Write(vol.Root(), id, offset, bytes.NewReader(buf), len(buf))
	if err != nil {
		var off *files.OffsetError
		if errors.As(err, &off) {
			writeJSON(w, http.StatusConflict, map[string]any{"error": off.Error(), "offset": off.Offset})
			return
		}
		s.auditFile(r, "file upload", name+"/"+id, "error")
		s.fail(w, r, err)
		return
	}
	if done.Complete {
		s.auditFile(r, "file upload", name+"/"+done.Path, "ok")
	}
	writeJSON(w, http.StatusOK, done)
}

func (s *Server) handleUploadStatus(w http.ResponseWriter, r *http.Request) {
	vol, _, ok := s.shareVol(w, r)
	if !ok {
		return
	}
	st, err := s.uploads.Status(vol.Root(), chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) handleUploadAbort(w http.ResponseWriter, r *http.Request) {
	vol, name, ok := s.shareVol(w, r)
	if !ok {
		return
	}
	id := chi.URLParam(r, "id")
	if err := s.uploads.Abort(vol.Root(), id); err != nil {
		s.fail(w, r, err)
		return
	}
	s.auditFile(r, "file upload", name+"/"+id, "cancelled")
	writeJSON(w, http.StatusOK, map[string]string{"status": "cancelled"})
}

func (s *Server) handleFetchStart(w http.ResponseWriter, r *http.Request) {
	vol, name, ok := s.shareVol(w, r)
	if !ok {
		return
	}
	var body struct {
		URL      string `json:"url"`
		Dir      string `json:"dir"`
		Filename string `json:"filename"`
		Conflict string `json:"conflict"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	actor, tokenID := actorOf(r)
	job, err := s.fetcher.Start(files.Request{
		Root: vol.Root(), Share: name, Dir: body.Dir, URL: body.URL,
		Filename: body.Filename, Conflict: body.Conflict, Actor: actor, TokenID: tokenID,
	})
	if err != nil {
		s.auditFile(r, "file fetch", clip(name+" "+body.URL, 200), "error")
		s.fail(w, r, err)
		return
	}
	view := job.View()
	if view.Skipped {
		writeJSON(w, http.StatusOK, view)
		return
	}
	s.auditFile(r, "file fetch start", clip(name+"/"+view.Path+" "+view.URL, 200), "ok")
	writeJSON(w, http.StatusAccepted, view)
}

func (s *Server) handleFetchList(w http.ResponseWriter, r *http.Request) {
	_, name, ok := s.shareVol(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": s.fetcher.List(name)})
}

func (s *Server) handleFetchGet(w http.ResponseWriter, r *http.Request) {
	_, name, ok := s.shareVol(w, r)
	if !ok {
		return
	}
	job, found := s.fetcher.Get(name, chi.URLParam(r, "id"))
	if !found {
		writeError(w, http.StatusNotFound, "That download is no longer running.")
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (s *Server) handleFetchCancel(w http.ResponseWriter, r *http.Request) {
	_, name, ok := s.shareVol(w, r)
	if !ok {
		return
	}
	if err := s.fetcher.Cancel(name, chi.URLParam(r, "id")); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "cancelled"})
}

func (s *Server) handleFileSettingsGet(w http.ResponseWriter, r *http.Request) {
	if s.auth == nil {
		writeError(w, http.StatusNotFound, "Settings are not available.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"allowLANURLs": s.auth.AllowLANURLs()})
}

func (s *Server) handleFileSettingsPut(w http.ResponseWriter, r *http.Request) {
	if s.auth == nil {
		writeError(w, http.StatusNotFound, "Settings are not available.")
		return
	}
	var body struct {
		AllowLANURLs bool `json:"allowLANURLs"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if err := s.auth.SetAllowLANURLs(body.AllowLANURLs); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"allowLANURLs": body.AllowLANURLs})
}

func (s *Server) settleFetch(job *files.Job) {
	if s.auth == nil || job == nil {
		return
	}
	result := "ok"
	switch job.Status {
	case files.StatusError:
		result = "error"
	case files.StatusCancelled:
		result = "cancelled"
	}
	name := job.Actor
	if name == "" {
		name = "admin"
	}
	if err := s.auth.Record(job.TokenID, store.AuditEvent{
		TokenName: name,
		Action:    "file fetch",
		Target:    clip(job.Share+"/"+job.Path+" "+job.URL, 200),
		Result:    result,
	}); err != nil && s.log != nil {
		s.log.Warn("audit log write failed", "err", err)
	}
}

func zipPaths(r *http.Request) ([]string, error) {
	if strings.Contains(r.Header.Get("Content-Type"), "application/json") {
		var body struct {
			Paths []string `json:"paths"`
		}
		dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&body); err != nil || len(body.Paths) == 0 {
			return nil, errors.New("empty")
		}
		return body.Paths, nil
	}
	if err := r.ParseForm(); err != nil {
		return nil, err
	}
	paths := r.Form["path"]
	if len(paths) == 0 {
		return nil, errors.New("empty")
	}
	return paths, nil
}

func parseOffset(s string) (int64, error) {
	if s == "" {
		return 0, errors.New("missing")
	}
	var n int64
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, errors.New("bad")
		}
		n = n*10 + int64(c-'0')
	}
	return n, nil
}

func sniffFile(name string, preview bool, r io.ReadSeeker, size int64) (string, bool) {
	// SVG is an image, but serving it as image/svg+xml would run scripts if
	// someone opened the URL directly. The browser previews it from a blob.
	if strings.EqualFold(path.Ext(name), ".svg") {
		return "text/plain; charset=utf-8", true
	}
	switch kindOf(name) {
	case "image", "video", "audio", "pdf":
		return mediaType(name), true
	case "text":
		return "text/plain; charset=utf-8", true
	}
	if preview && size >= 0 && size <= 2<<20 && looksText(r) {
		return "text/plain; charset=utf-8", true
	}
	return "application/octet-stream", false
}

func kindOf(name string) string {
	switch strings.ToLower(path.Ext(name)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".avif", ".bmp", ".ico":
		return "image"
	case ".mp4", ".webm", ".mov", ".m4v", ".ogv":
		return "video"
	case ".mp3", ".wav", ".flac", ".m4a", ".aac", ".ogg", ".oga", ".opus":
		return "audio"
	case ".pdf":
		return "pdf"
	case ".txt", ".md", ".markdown", ".log", ".json", ".yaml", ".yml", ".xml", ".csv",
		".js", ".mjs", ".cjs", ".ts", ".tsx", ".jsx", ".go", ".py", ".rs", ".css", ".html", ".htm",
		".sh", ".bash", ".conf", ".ini", ".toml", ".sql", ".c", ".h", ".cpp", ".java", ".rb", ".php",
		".env", ".gitignore", ".dockerfile":
		return "text"
	default:
		if strings.EqualFold(path.Base(name), "dockerfile") || strings.HasSuffix(strings.ToLower(name), ".log") {
			return "text"
		}
		return ""
	}
}

func mediaType(name string) string {
	switch strings.ToLower(path.Ext(name)) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".avif":
		return "image/avif"
	case ".bmp":
		return "image/bmp"
	case ".ico":
		return "image/x-icon"
	case ".mp4", ".m4v":
		return "video/mp4"
	case ".webm":
		return "video/webm"
	case ".mov":
		return "video/quicktime"
	case ".ogv":
		return "video/ogg"
	case ".mp3":
		return "audio/mpeg"
	case ".wav":
		return "audio/wav"
	case ".flac":
		return "audio/flac"
	case ".m4a", ".aac":
		return "audio/mp4"
	case ".ogg", ".oga", ".opus":
		return "audio/ogg"
	case ".pdf":
		return "application/pdf"
	default:
		return "application/octet-stream"
	}
}

func looksText(r io.ReadSeeker) bool {
	buf := make([]byte, 8192)
	n, err := r.Read(buf)
	_, _ = r.Seek(0, io.SeekStart)
	if n == 0 || (err != nil && err != io.EOF) {
		return false
	}
	buf = buf[:n]
	if !utf8.Valid(buf) {
		return false
	}
	for _, b := range buf {
		if b == 0 {
			return false
		}
	}
	return true
}

func headerFilename(name string) string {
	base := path.Base(strings.ReplaceAll(name, "\\", "/"))
	base = strings.Map(func(r rune) rune {
		if r < 32 || r == '"' || r == '\\' || r == '/' {
			return '_'
		}
		return r
	}, base)
	if base == "" || base == "." || base == ".." {
		return "download"
	}
	return base
}
