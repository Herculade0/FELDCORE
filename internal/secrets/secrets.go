// Package secrets stores short credentials encrypted at rest.
//
// The key file and every secret file are mode 0600. Plaintext is kept in
// memory so the daemon can use it, and so log lines can be scrubbed. It is
// never written to the state file and never intentionally logged.
package secrets

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Box is the secrets directory inside FELDCORE's data dir.
type Box struct {
	dir string
	key []byte

	mu    sync.Mutex
	plain map[string]string
}

// Open creates the secrets directory and loads the encryption key.
func Open(dir string) (*Box, error) {
	root := filepath.Join(dir, "secrets")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create secrets directory: %w", err)
	}
	if err := os.Chmod(root, 0o700); err != nil {
		return nil, err
	}
	keyPath := filepath.Join(root, "master.key")
	key, err := loadKey(keyPath)
	if err != nil {
		return nil, err
	}
	b := &Box{dir: root, key: key, plain: map[string]string{}}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() || e.Name() == "master.key" || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if _, _, err := b.Get(e.Name()); err != nil {
			return nil, err
		}
	}
	return b, nil
}

func loadKey(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, err
		}
		if err := writeFile(path, []byte(hex.EncodeToString(key))); err != nil {
			return nil, err
		}
		return key, nil
	}
	if err != nil {
		return nil, err
	}
	key, err := hex.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil || len(key) != 32 {
		return nil, fmt.Errorf("secrets key at %s is unreadable", path)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return nil, err
	}
	return key, nil
}

// Put encrypts value and stores it under name. An empty value deletes it.
func (b *Box) Put(name, value string) error {
	if err := checkName(name); err != nil {
		return err
	}
	path := filepath.Join(b.dir, name)
	b.mu.Lock()
	defer b.mu.Unlock()
	if value == "" {
		delete(b.plain, name)
		err := os.Remove(path)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	blob, err := seal(b.key, []byte(value))
	if err != nil {
		return err
	}
	if err := writeFile(path, blob); err != nil {
		return err
	}
	b.plain[name] = value
	return nil
}

// Get returns the plaintext. The boolean is false when name is not stored.
func (b *Box) Get(name string) (string, bool, error) {
	if err := checkName(name); err != nil {
		return "", false, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if v, ok := b.plain[name]; ok {
		return v, true, nil
	}
	raw, err := os.ReadFile(filepath.Join(b.dir, name))
	if errors.Is(err, fs.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	plain, err := open(b.key, raw)
	if err != nil {
		return "", false, fmt.Errorf("could not decrypt %s", name)
	}
	b.plain[name] = string(plain)
	return string(plain), true, nil
}

// Has reports whether a secret is stored, without returning it.
func (b *Box) Has(name string) bool {
	v, ok, err := b.Get(name)
	return err == nil && ok && v != ""
}

// Values returns the plaintext secrets currently loaded, for log scrubbing.
func (b *Box) Values() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]string, 0, len(b.plain))
	for _, v := range b.plain {
		if len(v) >= 8 {
			out = append(out, v)
		}
	}
	return out
}

// Redact replaces any of hidden that are long enough to be a credential.
func Redact(msg string, hidden ...string) string {
	for _, h := range hidden {
		if len(h) < 8 {
			continue
		}
		msg = strings.ReplaceAll(msg, h, "[redacted]")
	}
	return msg
}

func checkName(name string) error {
	if name == "" || name == "master.key" || strings.Contains(name, "/") || strings.Contains(name, "..") {
		return fmt.Errorf("invalid secret name")
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
		default:
			return fmt.Errorf("invalid secret name")
		}
	}
	return nil
}

func seal(key, plain []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return append(nonce, gcm.Seal(nil, nonce, plain, nil)...), nil
}

func open(key, blob []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(blob) < gcm.NonceSize() {
		return nil, errors.New("secret file is truncated")
	}
	nonce, ct := blob[:gcm.NonceSize()], blob[gcm.NonceSize():]
	return gcm.Open(nil, nonce, ct, nil)
}

func writeFile(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".secret-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// RedactingHandler drops loaded secrets from log records.
type RedactingHandler struct {
	next slog.Handler
	box  *Box
}

// NewRedactingHandler wraps next so secret values never reach it.
func NewRedactingHandler(next slog.Handler, box *Box) slog.Handler {
	if box == nil {
		return next
	}
	return RedactingHandler{next: next, box: box}
}

func (h RedactingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h RedactingHandler) Handle(ctx context.Context, rec slog.Record) error {
	hidden := h.box.Values()
	out := slog.NewRecord(rec.Time, rec.Level, Redact(rec.Message, hidden...), rec.PC)
	rec.Attrs(func(a slog.Attr) bool {
		out.AddAttrs(scrub(a, hidden))
		return true
	})
	return h.next.Handle(ctx, out)
}

func (h RedactingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	hidden := h.box.Values()
	clean := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		clean[i] = scrub(a, hidden)
	}
	return RedactingHandler{next: h.next.WithAttrs(clean), box: h.box}
}

func (h RedactingHandler) WithGroup(name string) slog.Handler {
	return RedactingHandler{next: h.next.WithGroup(name), box: h.box}
}

func scrub(a slog.Attr, hidden []string) slog.Attr {
	if a.Value.Kind() == slog.KindString {
		a.Value = slog.StringValue(Redact(a.Value.String(), hidden...))
	}
	return a
}
