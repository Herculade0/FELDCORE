package snapshots

import (
	"archive/tar"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/klauspost/compress/zstd"
)

func writeTarZst(src, dest string) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return 0, err
	}
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	zw, err := zstd.NewWriter(f)
	if err != nil {
		return 0, err
	}
	tw := tar.NewWriter(zw)
	err = filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		mode := info.Mode()
		if mode&os.ModeSocket != 0 || mode&os.ModeDevice != 0 || mode&os.ModeNamedPipe != 0 {
			return nil
		}
		link := ""
		if mode&os.ModeSymlink != 0 {
			link, err = os.Readlink(p)
			if err != nil {
				return err
			}
		}
		hdr, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return err
		}
		hdr.Name = filepath.ToSlash(rel)
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if !mode.IsRegular() {
			return nil
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		_, err = io.Copy(tw, in)
		in.Close()
		return err
	})
	if err != nil {
		tw.Close()
		zw.Close()
		return 0, err
	}
	if err := tw.Close(); err != nil {
		zw.Close()
		return 0, err
	}
	if err := zw.Close(); err != nil {
		return 0, err
	}
	if err := f.Close(); err != nil {
		return 0, err
	}
	fi, err := os.Stat(dest)
	if err != nil {
		return 0, err
	}
	return fi.Size(), nil
}

func extractTarZst(archive, dest string) error {
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	zr, err := zstd.NewReader(f)
	if err != nil {
		return err
	}
	defer zr.Close()
	tr := tar.NewReader(zr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		target, err := safeJoin(dest, hdr.Name)
		if err != nil {
			return err
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(hdr.Mode)&0o777); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			_ = os.Remove(target)
			if err := os.Symlink(hdr.Linkname, target); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(hdr.Mode)&0o777)
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				return err
			}
			if err := out.Close(); err != nil {
				return err
			}
		default:
			// Skip special files. A container's appdata is ordinary files.
		}
	}
}

func safeJoin(root, name string) (string, error) {
	clean := pathClean(name)
	if clean == "" || clean == "." || strings.HasPrefix(clean, "..") || strings.Contains(clean, "/../") {
		return "", fmt.Errorf("the archive contains an unsafe path")
	}
	dest := filepath.Join(root, filepath.FromSlash(clean))
	root = filepath.Clean(root)
	if dest != root && !strings.HasPrefix(dest, root+string(os.PathSeparator)) {
		return "", fmt.Errorf("the archive contains an unsafe path")
	}
	return dest, nil
}

func pathClean(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	name = strings.TrimPrefix(name, "/")
	return pathSlashClean(name)
}

func pathSlashClean(name string) string {
	parts := strings.Split(name, "/")
	var out []string
	for _, p := range parts {
		if p == "" || p == "." {
			continue
		}
		if p == ".." {
			return "../" + name
		}
		out = append(out, p)
	}
	return strings.Join(out, "/")
}

// restoreTree replaces dest with the archive, and puts the previous folder
// back if extracting fails.
func restoreTree(archive, dest string) error {
	prev := dest + ".feldcore-prev"
	_ = os.RemoveAll(prev)
	moved := false
	if _, err := os.Stat(dest); err == nil {
		if err := os.Rename(dest, prev); err != nil {
			return fmt.Errorf("could not move %s aside: %v", dest, err)
		}
		moved = true
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		if moved {
			_ = os.Rename(prev, dest)
		}
		return err
	}
	if err := extractTarZst(archive, dest); err != nil {
		_ = os.RemoveAll(dest)
		if moved {
			_ = os.Rename(prev, dest)
		}
		return err
	}
	_ = os.RemoveAll(prev)
	return nil
}
