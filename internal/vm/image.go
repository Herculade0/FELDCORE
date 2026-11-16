package vm

import (
	"encoding/binary"
	"io"
	"os"
)

// imageBytes is the virtual size of a disk image. qcow2 stores it in the
// header; everything else uses the file length.
func imageBytes(path string) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	hdr := make([]byte, 32)
	n, err := io.ReadFull(f, hdr)
	if n >= 4 && string(hdr[:4]) == "QFI\xfb" && n >= 32 {
		return int64(binary.BigEndian.Uint64(hdr[24:32])), nil
	}
	st, statErr := f.Stat()
	if statErr != nil {
		if err != nil {
			return 0, err
		}
		return 0, statErr
	}
	return st.Size(), nil
}

func imageFormat(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return "qcow2"
	}
	defer f.Close()
	hdr := make([]byte, 4)
	if _, err := io.ReadFull(f, hdr); err == nil && string(hdr) == "QFI\xfb" {
		return "qcow2"
	}
	return "raw"
}

func roundGB(n int64) int {
	if n <= 0 {
		return 0
	}
	return int((n + (1 << 30) - 1) / (1 << 30))
}
