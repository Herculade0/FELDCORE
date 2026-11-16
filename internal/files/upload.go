package files

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path"
	"regexp"
	"sync"
	"time"
)

// partID matches the ids newID returns. It keeps part paths inside .uploads.
var partID = regexp.MustCompile(`^[0-9a-f]{32}$`)

// Created is the result of starting, resuming or finishing an upload.
type Created struct {
	ID       string `json:"id,omitempty"`
	Offset   int64  `json:"offset"`
	Path     string `json:"path"`
	Size     int64  `json:"size"`
	Complete bool   `json:"complete"`
	Skipped  bool   `json:"skipped,omitempty"`
}

type partMeta struct {
	Rel     string    `json:"rel"`
	Size    int64     `json:"size"`
	Created time.Time `json:"created"`
}

type session struct {
	id   string
	root string
	rel  string
	size int64
	mu   sync.Mutex
}

// Uploads stores resumable uploads. The bytes live in the share's hidden
// .uploads folder, so a restart can continue a transfer the client still has.
type Uploads struct {
	mu   sync.Mutex
	live map[string]*session
}

// NewUploads returns an empty upload table.
func NewUploads() *Uploads {
	return &Uploads{live: map[string]*session{}}
}

// Create starts an upload of size bytes at rel. conflict is overwrite, skip,
// rename or fail. A zero-byte upload creates the file immediately.
func (u *Uploads) Create(root, rel string, size int64, conflict string) (Created, error) {
	if size < 0 || size > maxUpload {
		return Created{}, &InputError{Msg: "That file is too large to upload."}
	}
	vol, err := OpenVolume(root)
	if err != nil {
		return Created{}, err
	}
	rel, err = cleanUser(rel)
	if err != nil {
		return Created{}, err
	}
	if rel == "." {
		return Created{}, &InputError{Msg: "Choose a file name."}
	}
	if conflict == "" {
		conflict = "fail"
	}
	final, skip, err := vol.planWrite(rel, conflict)
	if err != nil || skip {
		return Created{Path: final, Size: size, Skipped: skip, Complete: skip}, err
	}
	if err := vol.Mkdir(path.Dir(final)); err != nil {
		return Created{}, err
	}
	if size == 0 {
		if err := vol.writeEmpty(final); err != nil {
			return Created{}, err
		}
		return Created{Path: final, Size: 0, Complete: true}, nil
	}
	id, err := newID()
	if err != nil {
		return Created{}, &OpError{Msg: "The upload could not be started.", Err: err}
	}
	meta := partMeta{Rel: final, Size: size, Created: time.Now().UTC()}
	if err := vol.savePart(id, meta); err != nil {
		return Created{}, err
	}
	u.mu.Lock()
	u.sweepLocked()
	u.live[id] = &session{id: id, root: vol.Root(), rel: final, size: size}
	u.mu.Unlock()
	return Created{ID: id, Path: final, Size: size}, nil
}

// Write appends one chunk. offset is how many bytes the client has already
// sent; it must match what is stored.
func (u *Uploads) Write(root, id string, offset int64, r io.Reader, n int) (Created, error) {
	sess, err := u.session(root, id)
	if err != nil {
		return Created{}, err
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()
	vol, err := OpenVolume(sess.root)
	if err != nil {
		return Created{}, err
	}
	got, err := vol.appendPart(id, offset, r, int64(n), sess.size)
	if err != nil {
		return Created{}, err
	}
	done := Created{ID: id, Offset: got, Path: sess.rel, Size: sess.size}
	if got > sess.size {
		_ = vol.discardPart(id)
		u.drop(id)
		return Created{}, &InputError{Msg: "That upload is larger than it claimed to be."}
	}
	if got == sess.size {
		final, err := vol.commitPart(id, sess.rel)
		if err != nil {
			return Created{}, err
		}
		u.drop(id)
		done.Path = final
		done.Offset = got
		done.Complete = true
	}
	return done, nil
}

// Status reports a stored upload, including one whose process restarted.
func (u *Uploads) Status(root, id string) (Created, error) {
	sess, err := u.session(root, id)
	if err != nil {
		return Created{}, err
	}
	vol, err := OpenVolume(sess.root)
	if err != nil {
		return Created{}, err
	}
	off, err := vol.partSize(id)
	if err != nil {
		return Created{}, err
	}
	return Created{ID: id, Offset: off, Path: sess.rel, Size: sess.size, Complete: off == sess.size && sess.size == 0}, nil
}

// Abort deletes a partial upload.
func (u *Uploads) Abort(root, id string) error {
	sess, err := u.session(root, id)
	if err != nil {
		return err
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()
	vol, err := OpenVolume(sess.root)
	if err != nil {
		return err
	}
	u.drop(id)
	return vol.discardPart(id)
}

func (u *Uploads) session(root, id string) (*session, error) {
	if !partID.MatchString(id) {
		return nil, ErrNotFound
	}
	vol, err := OpenVolume(root)
	if err != nil {
		return nil, err
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if s, ok := u.live[id]; ok {
		if s.root != vol.Root() {
			return nil, ErrNotFound
		}
		return s, nil
	}
	meta, err := vol.loadPart(id)
	if err != nil {
		return nil, err
	}
	s := &session{id: id, root: vol.Root(), rel: meta.Rel, size: meta.Size}
	u.live[id] = s
	return s, nil
}

func (u *Uploads) drop(id string) {
	u.mu.Lock()
	delete(u.live, id)
	u.mu.Unlock()
}

func (u *Uploads) sweepLocked() {
	if len(u.live) < 200 {
		return
	}
	// The map is only a cache. Disk meta is the resume record.
	u.live = map[string]*session{}
}

// planWrite applies the conflict policy and returns the path to write.
// skip is true when an existing file should be left alone.
func (v *Volume) planWrite(rel, conflict string) (final string, skip bool, err error) {
	switch conflict {
	case "overwrite", "skip", "rename", "fail":
	default:
		return "", false, &InputError{Msg: "Choose overwrite, skip or rename."}
	}
	exists := false
	err = v.with(func(r *os.Root) error {
		info, err := r.Lstat(osPath(rel))
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		exists = true
		if info.IsDir() && conflict == "overwrite" {
			return &InputError{Msg: "A folder with that name is already here."}
		}
		return nil
	})
	if err != nil || !exists {
		return rel, false, err
	}
	switch conflict {
	case "skip":
		return rel, true, nil
	case "overwrite":
		return rel, false, nil
	case "fail":
		return rel, false, &ConflictError{Name: path.Base(rel), Path: rel}
	default:
		var alt string
		err = v.with(func(r *os.Root) error {
			alt, err = uniqueRel(r, path.Dir(rel), path.Base(rel))
			return err
		})
		return alt, false, err
	}
}

func (v *Volume) writeEmpty(rel string) error {
	return v.with(func(r *os.Root) error {
		f, err := r.OpenFile(osPath(rel), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, filePerm)
		if err != nil {
			return err
		}
		return f.Close()
	})
}

func (v *Volume) savePart(id string, meta partMeta) error {
	if !partID.MatchString(id) {
		return &InputError{Msg: "That upload is not valid."}
	}
	raw, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	return v.with(func(r *os.Root) error {
		if err := r.MkdirAll(UploadsDir, 0o700); err != nil {
			return err
		}
		f, err := r.OpenFile(UploadsDir+"/"+id+".part", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}
		mf, err := r.OpenFile(UploadsDir+"/"+id+".json", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			return err
		}
		_, werr := mf.Write(raw)
		cerr := mf.Close()
		if werr != nil {
			return werr
		}
		return cerr
	})
}

func (v *Volume) loadPart(id string) (partMeta, error) {
	var meta partMeta
	if !partID.MatchString(id) {
		return meta, ErrNotFound
	}
	err := v.with(func(r *os.Root) error {
		b, err := r.ReadFile(UploadsDir + "/" + id + ".json")
		if err != nil {
			return err
		}
		return json.Unmarshal(b, &meta)
	})
	if err != nil {
		return partMeta{}, err
	}
	if _, err := cleanUser(meta.Rel); err != nil {
		return partMeta{}, err
	}
	return meta, nil
}

func (v *Volume) partSize(id string) (int64, error) {
	var n int64
	err := v.with(func(r *os.Root) error {
		info, err := r.Lstat(UploadsDir + "/" + id + ".part")
		if err != nil {
			return err
		}
		n = info.Size()
		return nil
	})
	return n, err
}

func (v *Volume) appendPart(id string, offset int64, src io.Reader, n, total int64) (int64, error) {
	if n < 0 || n > MaxChunk {
		return 0, &InputError{Msg: "That piece of the upload is too large."}
	}
	var got int64
	err := v.with(func(r *os.Root) error {
		f, err := r.OpenFile(UploadsDir+"/"+id+".part", os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil {
			return err
		}
		if info.Size() != offset {
			return &OffsetError{Offset: info.Size()}
		}
		if offset > total || n > total-offset {
			return &InputError{Msg: "That upload is larger than it claimed to be."}
		}
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			return err
		}
		written, err := io.Copy(f, io.LimitReader(src, n))
		if err != nil {
			return err
		}
		if written != n {
			// A short chunk is still stored; the client continues from the new offset.
		}
		if err := f.Sync(); err != nil {
			return err
		}
		got = offset + written
		return nil
	})
	return got, err
}

func (v *Volume) commitPart(id, rel string) (string, error) {
	rel, err := cleanUser(rel)
	if err != nil {
		return "", err
	}
	err = v.with(func(r *os.Root) error {
		parent := path.Dir(rel)
		if parent != "." {
			if err := r.MkdirAll(osPath(parent), dirPerm); err != nil {
				return err
			}
		}
		if info, err := r.Lstat(osPath(rel)); err == nil && info.IsDir() {
			return &InputError{Msg: "A folder with that name is already here."}
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		// rename replaces a regular file. The part stays put if this fails.
		if err := r.Rename(UploadsDir+"/"+id+".part", osPath(rel)); err != nil {
			return err
		}
		_ = r.Remove(UploadsDir + "/" + id + ".json")
		return nil
	})
	if err != nil {
		return "", err
	}
	return rel, nil
}

func (v *Volume) discardPart(id string) error {
	if !partID.MatchString(id) {
		return ErrNotFound
	}
	return v.with(func(r *os.Root) error {
		_ = r.Remove(UploadsDir + "/" + id + ".part")
		err := r.Remove(UploadsDir + "/" + id + ".json")
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	})
}

// MaxChunk is the most bytes one upload request may carry.
const MaxChunk = 32 << 20
