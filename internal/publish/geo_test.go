package publish

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"testing"
)

func TestExtractMMDBIgnoresArchivePaths(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	body := []byte("not-a-real-database")
	hdr := &tar.Header{
		Name: "../../GeoLite2-Country.mmdb",
		Mode: 0o644,
		Size: int64(len(body)),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := extractMMDB(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(body) {
		t.Fatalf("got %q", got)
	}
}
