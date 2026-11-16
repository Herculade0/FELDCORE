// Package files is the share file browser: listing, preview downloads, edits,
// chunked uploads and URL fetches. Every path is resolved inside one share
// directory. ".." and links that leave that directory are refused.
package files

import (
	"archive/zip"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	// TrashDir is the per-share recycle folder. Deletes land here first.
	TrashDir = ".trash"
	// UploadsDir holds partial uploads. It is hidden from the browser.
	UploadsDir = ".uploads"

	maxList   = 5000
	maxName   = 255
	maxPath   = 1024
	maxDepth  = 40
	maxUpload = 256 << 30
	dirPerm   = 0o755
	filePerm  = 0o644
)

// ErrNotFound means the path is not in the share.
var ErrNotFound = errors.New("not found")

// InputError is a validation failure whose message is safe to show.
type InputError struct{ Msg string }

func (e *InputError) Error() string { return e.Msg }

// OpError is a disk failure whose message is safe to show.
type OpError struct {
	Msg string
	Err error
}

func (e *OpError) Error() string { return e.Msg }
func (e *OpError) Unwrap() error { return e.Err }

// ConflictError means a file or folder with that name is already there.
type ConflictError struct {
	Name string
	Path string
}

func (e *ConflictError) Error() string {
	if e.Name == "" {
		return "Something with that name is already here."
	}
	return fmt.Sprintf("%s is already in this folder.", e.Name)
}

// OffsetError means a chunk did not continue from the bytes already stored.
type OffsetError struct{ Offset int64 }

func (e *OffsetError) Error() string { return "The upload resumed at the wrong place." }

// Entry is one file or folder in a listing.
type Entry struct {
	Name    string    `json:"name"`
	Path    string    `json:"path"`
	Dir     bool      `json:"dir"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"modTime"`
	Mime    string    `json:"mime,omitempty"`
	Link    bool      `json:"link,omitempty"`
	Broken  bool      `json:"broken,omitempty"`
}

// Listing is a folder's contents.
type Listing struct {
	Path      string  `json:"path"`
	Parent    string  `json:"parent,omitempty"`
	Entries   []Entry `json:"entries"`
	Trash     bool    `json:"trash"`
	Truncated bool    `json:"truncated,omitempty"`
}

// Volume is one share directory. The path is the resolved share root.
type Volume struct {
	root string
}

// OpenVolume resolves root and refuses to open anything that is not a directory.
func OpenVolume(root string) (*Volume, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, &OpError{Msg: "The share folder can't be opened.", Err: err}
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, &OpError{Msg: "The share folder is missing.", Err: err}
		}
		return nil, &OpError{Msg: "The share folder can't be opened.", Err: err}
	}
	st, err := os.Stat(resolved)
	if err != nil || !st.IsDir() {
		return nil, &OpError{Msg: "The share folder can't be opened.", Err: err}
	}
	return &Volume{root: resolved}, nil
}

// Root is the resolved share directory.
func (v *Volume) Root() string { return v.root }

// List returns the folder at rel. rel is slash-separated and relative to the share.
func (v *Volume) List(rel string) (Listing, error) {
	rel, err := cleanUser(rel)
	if err != nil {
		return Listing{}, err
	}
	var out Listing
	err = v.with(func(r *os.Root) error {
		f, err := r.Open(osPath(rel))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) && (rel == TrashDir) {
				out = Listing{Path: rel, Parent: ".", Entries: []Entry{}, Trash: true}
				return nil
			}
			return err
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return &InputError{Msg: "That is not a folder."}
		}
		dents, err := f.ReadDir(-1)
		if err != nil {
			return err
		}
		entries := make([]Entry, 0, len(dents))
		for _, d := range dents {
			name := d.Name()
			if name == "" || name == "." || name == ".." || hiddenEntry(rel, name) {
				continue
			}
			entries = append(entries, describe(r, joinRel(rel, name), name, d))
		}
		sortEntries(entries)
		if len(entries) > maxList {
			out.Truncated = true
			entries = entries[:maxList]
		}
		parent := ""
		if rel != "." {
			parent = path.Dir(rel)
			if parent == "" {
				parent = "."
			}
		}
		out.Path = rel
		out.Parent = parent
		out.Entries = entries
		out.Trash = rel == TrashDir || strings.HasPrefix(rel, TrashDir+"/")
		if out.Entries == nil {
			out.Entries = []Entry{}
		}
		return nil
	})
	return out, err
}

// Open opens a file for reading. The caller closes it. Directories are refused.
func (v *Volume) Open(rel string) (*os.File, os.FileInfo, error) {
	rel, err := cleanUser(rel)
	if err != nil {
		return nil, nil, err
	}
	if rel == "." {
		return nil, nil, &InputError{Msg: "That is a folder."}
	}
	r, err := os.OpenRoot(v.root)
	if err != nil {
		return nil, nil, &OpError{Msg: "The share folder can't be opened.", Err: err}
	}
	defer r.Close()
	f, err := r.Open(osPath(rel))
	if err != nil {
		return nil, nil, mapFS(err)
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, nil, mapFS(err)
	}
	if info.IsDir() {
		f.Close()
		return nil, nil, &InputError{Msg: "That is a folder."}
	}
	if err := confirmInside(v.root, f); err != nil {
		f.Close()
		return nil, nil, err
	}
	return f, info, nil
}

// Mkdir creates a folder, including any missing parents inside the share.
// Creating a folder that already exists is success.
func (v *Volume) Mkdir(rel string) error {
	rel, err := cleanUser(rel)
	if err != nil {
		return err
	}
	if rel == "." {
		return nil
	}
	return v.with(func(r *os.Root) error {
		info, err := r.Lstat(osPath(rel))
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				st, serr := r.Stat(osPath(rel))
				if serr != nil {
					return serr
				}
				if st.IsDir() {
					return nil
				}
				return &ConflictError{Name: path.Base(rel), Path: rel}
			}
			if info.IsDir() {
				return nil
			}
			return &ConflictError{Name: path.Base(rel), Path: rel}
		}
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return r.MkdirAll(osPath(rel), dirPerm)
	})
}

// Rename changes the base name of one file or folder, staying in its folder.
func (v *Volume) Rename(rel, name string) (string, error) {
	rel, err := cleanUser(rel)
	if err != nil {
		return "", err
	}
	if rel == "." || rel == TrashDir {
		return "", &InputError{Msg: "That can't be renamed."}
	}
	name = strings.TrimSpace(name)
	if err := validComponent(name); err != nil {
		return "", err
	}
	dest := joinRel(path.Dir(rel), name)
	if dest == rel {
		return rel, nil
	}
	err = v.with(func(r *os.Root) error {
		if _, err := r.Lstat(osPath(rel)); err != nil {
			return err
		}
		if _, err := r.Lstat(osPath(dest)); err == nil {
			return &ConflictError{Name: name, Path: dest}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return r.Rename(osPath(rel), osPath(dest))
	})
	if err != nil {
		return "", err
	}
	return dest, nil
}

// Transfer moves or copies each path into destDir.
// conflict is "overwrite", "skip", "rename" or "fail".
func (v *Volume) Transfer(srcs []string, destDir, conflict string, copy bool) ([]string, error) {
	destDir, err := cleanUser(destDir)
	if err != nil {
		return nil, err
	}
	if conflict == "" {
		conflict = "fail"
	}
	switch conflict {
	case "overwrite", "skip", "rename", "fail":
	default:
		return nil, &InputError{Msg: "Choose overwrite, skip or rename."}
	}
	if len(srcs) == 0 {
		return nil, &InputError{Msg: "Choose a file or folder first."}
	}
	cleaned := make([]string, 0, len(srcs))
	for _, s := range srcs {
		rel, err := cleanUser(s)
		if err != nil {
			return nil, err
		}
		if rel == "." || rel == TrashDir || rel == UploadsDir {
			return nil, &InputError{Msg: "That can't be moved."}
		}
		cleaned = append(cleaned, rel)
	}
	var done []string
	err = v.with(func(r *os.Root) error {
		info, err := r.Stat(osPath(destDir))
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return &InputError{Msg: "The destination is not a folder."}
		}
		for _, src := range cleaned {
			base := path.Base(src)
			target := joinRel(destDir, base)
			if !copy && (target == src || isWithin(src, destDir)) {
				return &InputError{Msg: "A folder can't be moved into itself."}
			}
			if copy && target == src {
				return &InputError{Msg: "Choose a different folder to copy into."}
			}
			final, err := place(r, src, target, conflict, copy)
			if err != nil {
				return err
			}
			if final != "" {
				done = append(done, final)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if done == nil {
		done = []string{}
	}
	return done, nil
}

// Delete moves paths into the share's trash folder. Paths already in the
// trash, and the trash folder itself, are removed permanently.
func (v *Volume) Delete(rels []string) ([]string, error) {
	if len(rels) == 0 {
		return nil, &InputError{Msg: "Choose a file or folder first."}
	}
	var gone []string
	err := v.with(func(r *os.Root) error {
		for _, raw := range rels {
			rel, err := cleanUser(raw)
			if err != nil {
				return err
			}
			if rel == "." || rel == UploadsDir {
				return &InputError{Msg: "That can't be deleted."}
			}
			if rel == TrashDir {
				dents, err := readNames(r, TrashDir)
				if errors.Is(err, os.ErrNotExist) {
					gone = append(gone, rel)
					continue
				}
				if err != nil {
					return err
				}
				for _, name := range dents {
					child := joinRel(TrashDir, name)
					if err := r.RemoveAll(osPath(child)); err != nil {
						return err
					}
					gone = append(gone, child)
				}
				continue
			}
			if _, err := r.Lstat(osPath(rel)); err != nil {
				return err
			}
			if rel == TrashDir || strings.HasPrefix(rel, TrashDir+"/") {
				if err := r.RemoveAll(osPath(rel)); err != nil {
					return err
				}
				gone = append(gone, rel)
				continue
			}
			if err := r.MkdirAll(TrashDir, dirPerm); err != nil {
				return err
			}
			dest, err := trashDest(r, path.Base(rel))
			if err != nil {
				return err
			}
			if err := r.Rename(osPath(rel), osPath(dest)); err != nil {
				return err
			}
			gone = append(gone, dest)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return gone, nil
}

// WriteZip streams a zip of the given files and folders to w.
func (v *Volume) WriteZip(w io.Writer, rels []string) error {
	if len(rels) == 0 {
		return &InputError{Msg: "Choose a file or folder to download."}
	}
	cleaned := make([]string, len(rels))
	for i, raw := range rels {
		rel, err := cleanUser(raw)
		if err != nil {
			return err
		}
		if rel == "." {
			return &InputError{Msg: "Open a folder and choose what to download."}
		}
		cleaned[i] = rel
	}
	zw := zip.NewWriter(w)
	err := v.with(func(r *os.Root) error {
		seen := map[string]bool{}
		for _, rel := range cleaned {
			if err := addZip(r.Name(), r, zw, rel, rel, seen, 0); err != nil {
				return err
			}
		}
		return nil
	})
	closeErr := zw.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return &OpError{Msg: "The archive could not be finished.", Err: closeErr}
	}
	return nil
}

// Exists reports whether a user path is present. Errors other than "missing"
// are returned.
func (v *Volume) Exists(rel string) (bool, error) {
	rel, err := cleanUser(rel)
	if err != nil {
		return false, err
	}
	var ok bool
	err = v.with(func(r *os.Root) error {
		_, err := r.Lstat(osPath(rel))
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		ok = true
		return nil
	})
	return ok, err
}

func (v *Volume) with(fn func(*os.Root) error) error {
	r, err := os.OpenRoot(v.root)
	if err != nil {
		return &OpError{Msg: "The share folder can't be opened.", Err: err}
	}
	defer r.Close()
	return mapFS(fn(r))
}

func describe(r *os.Root, child, name string, d os.DirEntry) Entry {
	ent := Entry{Name: name, Path: child, ModTime: time.Now()}
	info, lerr := d.Info()
	if lerr == nil {
		ent.ModTime = info.ModTime()
		ent.Size = info.Size()
		ent.Link = info.Mode()&os.ModeSymlink != 0
	}
	st, err := r.Stat(osPath(child))
	if err != nil {
		ent.Broken = true
		ent.Dir = false
		ent.Size = 0
		return ent
	}
	ent.Dir = st.IsDir()
	ent.Size = st.Size()
	ent.ModTime = st.ModTime()
	if !ent.Dir {
		ent.Mime = mimeByName(name)
	}
	return ent
}

func place(r *os.Root, src, target, conflict string, copy bool) (string, error) {
	if _, err := r.Lstat(osPath(src)); err != nil {
		return "", err
	}
	final := target
	if _, err := r.Lstat(osPath(target)); err == nil {
		switch conflict {
		case "skip":
			return "", nil
		case "rename":
			alt, err := uniqueRel(r, path.Dir(target), path.Base(target))
			if err != nil {
				return "", err
			}
			final = alt
		case "overwrite":
			if err := r.RemoveAll(osPath(target)); err != nil {
				return "", err
			}
		default:
			return "", &ConflictError{Name: path.Base(target), Path: target}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if copy {
		if err := copyPath(r.Name(), r, src, final, 0); err != nil {
			return "", err
		}
		return final, nil
	}
	if err := r.Rename(osPath(src), osPath(final)); err != nil {
		return "", err
	}
	return final, nil
}

func copyPath(root string, r *os.Root, src, dest string, depth int) error {
	if depth > maxDepth {
		return &InputError{Msg: "That folder is nested too deeply to copy."}
	}
	info, err := r.Lstat(osPath(src))
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		link, err := r.Readlink(osPath(src))
		if err != nil {
			return err
		}
		if _, err := safeLink(path.Dir(src), link); err != nil {
			return err
		}
		return r.Symlink(link, osPath(dest))
	}
	if info.IsDir() {
		if err := r.MkdirAll(osPath(dest), dirPerm); err != nil {
			return err
		}
		f, err := r.Open(osPath(src))
		if err != nil {
			return err
		}
		dents, err := f.ReadDir(-1)
		f.Close()
		if err != nil {
			return err
		}
		for _, d := range dents {
			name := d.Name()
			if name == "" || name == "." || name == ".." || name == UploadsDir {
				continue
			}
			if err := copyPath(root, r, joinRel(src, name), joinRel(dest, name), depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	in, err := r.Open(osPath(src))
	if err != nil {
		return err
	}
	defer in.Close()
	if err := confirmInside(root, in); err != nil {
		return err
	}
	out, err := r.OpenFile(osPath(dest), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, filePerm)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func addZip(root string, r *os.Root, zw *zip.Writer, rel, name string, seen map[string]bool, depth int) error {
	if depth > maxDepth {
		return &InputError{Msg: "That folder is nested too deeply to download."}
	}
	if seen[rel] {
		return nil
	}
	seen[rel] = true
	info, err := r.Lstat(osPath(rel))
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		link, err := r.Readlink(osPath(rel))
		if err != nil {
			return err
		}
		followed, err := safeLink(path.Dir(rel), link)
		if err != nil {
			return err
		}
		// Zip the target under the link's name, but only when it stays inside.
		return addZip(root, r, zw, followed, name, seen, depth+1)
	}
	zipName := strings.TrimPrefix(name, "./")
	if info.IsDir() {
		if !strings.HasSuffix(zipName, "/") {
			zipName += "/"
		}
		hdr := &zip.FileHeader{Name: zipName, Method: zip.Store}
		hdr.SetMode(info.Mode())
		hdr.Modified = info.ModTime()
		if _, err := zw.CreateHeader(hdr); err != nil {
			return err
		}
		f, err := r.Open(osPath(rel))
		if err != nil {
			return err
		}
		dents, err := f.ReadDir(-1)
		f.Close()
		if err != nil {
			return err
		}
		for _, d := range dents {
			n := d.Name()
			if n == "" || n == "." || n == ".." || n == UploadsDir {
				continue
			}
			if err := addZip(root, r, zw, joinRel(rel, n), zipName+n, seen, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	f, err := r.Open(osPath(rel))
	if err != nil {
		return err
	}
	defer f.Close()
	if err := confirmInside(root, f); err != nil {
		return err
	}
	hdr := &zip.FileHeader{Name: zipName, Method: zip.Deflate}
	if storedExt(path.Ext(zipName)) {
		hdr.Method = zip.Store
	}
	hdr.SetMode(info.Mode())
	hdr.Modified = info.ModTime()
	w, err := zw.CreateHeader(hdr)
	if err != nil {
		return err
	}
	_, err = io.Copy(w, f)
	return err
}

func storedExt(ext string) bool {
	switch strings.ToLower(ext) {
	case ".zip", ".gz", ".tgz", ".bz2", ".xz", ".7z", ".rar",
		".jpg", ".jpeg", ".png", ".gif", ".webp", ".avif",
		".mp4", ".mov", ".webm", ".mkv", ".mp3", ".m4a", ".flac", ".pdf":
		return true
	default:
		return false
	}
}

func readNames(r *os.Root, rel string) ([]string, error) {
	f, err := r.Open(osPath(rel))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	dents, err := f.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(dents))
	for _, d := range dents {
		if n := d.Name(); n != "" && n != "." && n != ".." {
			names = append(names, n)
		}
	}
	return names, nil
}

func trashDest(r *os.Root, base string) (string, error) {
	if len(base) > 80 {
		base = base[:80]
	}
	stamp := time.Now().UTC().Format("20060102-150405")
	for i := 1; i < 100; i++ {
		name := stamp + "-" + base
		if i > 1 {
			name = fmt.Sprintf("%s-%d-%s", stamp, i, base)
		}
		rel := TrashDir + "/" + name
		_, err := r.Lstat(osPath(rel))
		if errors.Is(err, os.ErrNotExist) {
			return rel, nil
		}
		if err != nil {
			return "", err
		}
	}
	return "", &InputError{Msg: "The trash folder needs a moment. Try again."}
}

func uniqueRel(r *os.Root, dir, name string) (string, error) {
	ext := path.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for i := 2; i < 500; i++ {
		cand := fmt.Sprintf("%s (%d)%s", stem, i, ext)
		if err := validComponent(cand); err != nil {
			return "", err
		}
		rel := joinRel(dir, cand)
		_, err := r.Lstat(osPath(rel))
		if errors.Is(err, os.ErrNotExist) {
			return rel, nil
		}
		if err != nil {
			return "", err
		}
	}
	return "", &InputError{Msg: "Choose a different name."}
}

func sortEntries(entries []Entry) {
	sort.Slice(entries, func(i, j int) bool {
		return entryLess(entries[i], entries[j])
	})
}

func entryLess(a, b Entry) bool {
	if a.Dir != b.Dir {
		return a.Dir
	}
	return strings.ToLower(a.Name) < strings.ToLower(b.Name)
}

// cleanUser accepts a relative slash path and refuses anything that could
// leave the share: absolute paths, drive letters, nulls, ".." and the
// reserved uploads folder.
func cleanUser(p string) (string, error) {
	if strings.ContainsRune(p, 0) {
		return "", &InputError{Msg: "That path is not valid."}
	}
	p = strings.TrimSpace(p)
	if p == "" || p == "." || p == "/" {
		return ".", nil
	}
	p = strings.ReplaceAll(p, "\\", "/")
	if strings.HasPrefix(p, "/") || strings.HasPrefix(p, "~") {
		return "", &InputError{Msg: "That path is outside this share."}
	}
	if len(p) >= 2 && p[1] == ':' {
		return "", &InputError{Msg: "That path is outside this share."}
	}
	for _, part := range strings.Split(p, "/") {
		if part == ".." {
			return "", &InputError{Msg: "That path is outside this share."}
		}
	}
	anchored := path.Clean("/" + p)
	if !strings.HasPrefix(anchored, "/") {
		return "", &InputError{Msg: "That path is outside this share."}
	}
	rel := strings.TrimPrefix(anchored, "/")
	if rel == "" || rel == "." {
		return ".", nil
	}
	if rel == ".." || strings.HasPrefix(rel, "../") {
		return "", &InputError{Msg: "That path is outside this share."}
	}
	parts := strings.Split(rel, "/")
	if len(rel) > maxPath || len(parts) > maxDepth {
		return "", &InputError{Msg: "That path is too long."}
	}
	for _, part := range parts {
		if err := validComponent(part); err != nil {
			return "", err
		}
		if part == UploadsDir {
			return "", &InputError{Msg: "That folder is reserved."}
		}
	}
	return rel, nil
}

func validComponent(part string) error {
	if part == "" || part == "." || part == ".." {
		return &InputError{Msg: "That path is outside this share."}
	}
	if len(part) > maxName {
		return &InputError{Msg: "That name is too long."}
	}
	if strings.ContainsAny(part, "/\\\x00") {
		return &InputError{Msg: "Use a name without slashes."}
	}
	for _, r := range part {
		if r < 32 {
			return &InputError{Msg: "That name is not valid."}
		}
	}
	return nil
}

func joinRel(dir, name string) string {
	if dir == "" || dir == "." {
		return name
	}
	return dir + "/" + name
}

func isWithin(dir, child string) bool {
	if dir == "." {
		return false
	}
	return child == dir || strings.HasPrefix(child, dir+"/")
}

func hiddenEntry(rel, name string) bool {
	if name == UploadsDir {
		return true
	}
	return name == TrashDir && (rel == "." || rel == "")
}

func osPath(rel string) string {
	if rel == "" || rel == "." {
		return "."
	}
	return rel
}

// safeLink resolves a symlink target lexically against dir (a path inside the
// share) and refuses one that leaves the share or is absolute. The returned
// path is relative to the share. os.Root will not follow an escaping link, but
// Symlink itself does not check the target, so this has to.
func safeLink(dir, link string) (string, error) {
	link = strings.ReplaceAll(link, "\\", "/")
	if link == "" || strings.HasPrefix(link, "/") || filepath.IsAbs(link) {
		return "", &InputError{Msg: "That link points outside this share."}
	}
	if len(link) >= 2 && link[1] == ':' || strings.ContainsRune(link, 0) {
		return "", &InputError{Msg: "That link points outside this share."}
	}
	var parts []string
	if dir != "" && dir != "." {
		parts = strings.Split(dir, "/")
	}
	for _, p := range strings.Split(link, "/") {
		switch p {
		case "", ".":
			continue
		case "..":
			if len(parts) == 0 {
				return "", &InputError{Msg: "That link points outside this share."}
			}
			parts = parts[:len(parts)-1]
		default:
			if err := validComponent(p); err != nil {
				return "", &InputError{Msg: "That link points outside this share."}
			}
			parts = append(parts, p)
		}
	}
	if len(parts) == 0 {
		return ".", nil
	}
	return strings.Join(parts, "/"), nil
}

func confirmInside(root string, f *os.File) error {
	link, err := os.Readlink(fmt.Sprintf("/proc/self/fd/%d", f.Fd()))
	if err != nil {
		return nil
	}
	link = strings.TrimSuffix(link, " (deleted)")
	if !inside(root, link) {
		return &InputError{Msg: "That path is outside this share."}
	}
	return nil
}

func inside(root, candidate string) bool {
	root = filepath.Clean(root)
	candidate = filepath.Clean(candidate)
	if candidate == root {
		return true
	}
	rel, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func mapFS(err error) error {
	if err == nil {
		return nil
	}
	var ie *InputError
	var ce *ConflictError
	var oe *OffsetError
	var op *OpError
	if errors.As(err, &ie) || errors.As(err, &ce) || errors.As(err, &oe) || errors.As(err, &op) {
		return err
	}
	if errors.Is(err, ErrNotFound) || errors.Is(err, os.ErrNotExist) || errors.Is(err, fs.ErrNotExist) {
		return ErrNotFound
	}
	if refused(err) {
		return &InputError{Msg: "That path is outside this share."}
	}
	if errors.Is(err, os.ErrPermission) {
		return &OpError{Msg: "FELDCORE does not have permission to change that file.", Err: err}
	}
	return &OpError{Msg: "That file could not be changed.", Err: err}
}

func refused(err error) bool {
	s := err.Error()
	return strings.Contains(s, "escapes from parent") ||
		strings.Contains(s, "path escapes") ||
		strings.Contains(s, "symbolic link") && strings.Contains(s, "outside")
}

func mimeByName(name string) string {
	ext := strings.ToLower(path.Ext(name))
	switch ext {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".svg":
		return "image/svg+xml"
	case ".mp4":
		return "video/mp4"
	case ".webm":
		return "video/webm"
	case ".mov":
		return "video/quicktime"
	case ".mp3":
		return "audio/mpeg"
	case ".wav":
		return "audio/wav"
	case ".flac":
		return "audio/flac"
	case ".m4a":
		return "audio/mp4"
	case ".ogg", ".oga":
		return "audio/ogg"
	case ".pdf":
		return "application/pdf"
	case ".md", ".markdown":
		return "text/markdown"
	case ".json":
		return "application/json"
	case ".log", ".txt", ".csv", ".yaml", ".yml", ".xml", ".html", ".css", ".js", ".ts", ".tsx", ".go", ".py", ".sh":
		return "text/plain"
	default:
		return "application/octet-stream"
	}
}

func newID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
