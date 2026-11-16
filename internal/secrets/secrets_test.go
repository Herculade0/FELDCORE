package secrets

import (
	"bytes"
	"log/slog"
	"os"
	"testing"
)

func TestSealRoundTripAndMode(t *testing.T) {
	dir := t.TempDir()
	box, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	const token = "super-secret-api-token-value"
	if err := box.Put("cf-api", token); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{dir + "/secrets", dir + "/secrets/master.key", dir + "/secrets/cf-api"} {
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm()&0o077 != 0 {
			t.Fatalf("%s is %o, want 0600/0700", p, fi.Mode().Perm())
		}
	}
	raw, err := os.ReadFile(dir + "/secrets/cf-api")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(token)) {
		t.Fatal("secret file contains plaintext")
	}
	got, ok, err := box.Get("cf-api")
	if err != nil || !ok || got != token {
		t.Fatalf("get %q %v %v", got, ok, err)
	}

	box2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, ok, err = box2.Get("cf-api")
	if err != nil || !ok || got != token {
		t.Fatalf("reopen %q %v %v", got, ok, err)
	}
}

func TestRedactingHandler(t *testing.T) {
	box, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const token = "super-secret-api-token-value"
	if err := box.Put("cf-api", token); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	log := slog.New(NewRedactingHandler(slog.NewTextHandler(&buf, nil), box))
	log.Info("connecting", "token", token, "msg", "bearer "+token)
	if bytes.Contains(buf.Bytes(), []byte(token)) {
		t.Fatalf("log leaked token: %s", buf.String())
	}
	if !bytes.Contains(buf.Bytes(), []byte("[redacted]")) {
		t.Fatalf("log: %s", buf.String())
	}
}
