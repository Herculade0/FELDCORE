package vm

import (
	"encoding/binary"
	"strings"
)

// isoFile is one file in the root of a small ISO 9660 / Joliet image.
type isoFile struct {
	Name string
	Data []byte
}

type placedFile struct {
	isoFile
	extent uint32
}

// buildISO writes a data CD. The primary volume id is the label (cloud-init
// looks for "cidata"). A Joliet supplementary descriptor carries the long
// file names Linux mounts, so user-data and meta-data keep their hyphens.
// The image is only used for a handful of small text files.
func buildISO(label string, files []isoFile) ([]byte, error) {
	if label == "" {
		label = "cidata"
	}
	const sec = 2048
	// 0-15 system, 16 PVD, 17 Joliet SVD, 18 terminator,
	// 19 L path, 20 M path, 21 Joliet L, 22 Joliet M,
	// 23 ISO root, 24 Joliet root, 25+ payloads.
	const (
		secPVD   = 16
		secSVD   = 17
		secTerm  = 18
		secL     = 19
		secM     = 20
		secJL    = 21
		secJM    = 22
		secRoot  = 23
		secJRoot = 24
		secData  = 25
	)

	next := uint32(secData)
	placed := make([]placedFile, len(files))
	for i, f := range files {
		n := uint32((len(f.Data) + sec - 1) / sec)
		if n == 0 {
			n = 1
		}
		placed[i] = placedFile{isoFile: f, extent: next}
		next += n
	}
	img := make([]byte, int(next)*sec)

	putPVD(img[secPVD*sec:(secPVD+1)*sec], label, next, secL, secM, secRoot, false)
	putPVD(img[secSVD*sec:(secSVD+1)*sec], label, next, secJL, secJM, secJRoot, true)
	img[secTerm*sec] = 255
	copy(img[secTerm*sec+1:], []byte("CD001"))
	img[secTerm*sec+6] = 1

	putPathTable(img[secL*sec:], secRoot, false)
	putPathTable(img[secM*sec:], secRoot, true)
	putPathTable(img[secJL*sec:], secJRoot, false)
	putPathTable(img[secJM*sec:], secJRoot, true)

	for _, f := range placed {
		copy(img[int(f.extent)*sec:], f.Data)
	}
	writeDir(img[secRoot*sec:(secRoot+1)*sec], secRoot, placed, false)
	writeDir(img[secJRoot*sec:(secJRoot+1)*sec], secJRoot, placed, true)
	return img, nil
}

func writeDir(dst []byte, self int, files []placedFile, joliet bool) {
	off := copy(dst, dirRecord([]byte{0}, uint32(self), uint32(len(dst)), true))
	off += copy(dst[off:], dirRecord([]byte{1}, uint32(self), uint32(len(dst)), true))
	for _, f := range files {
		var ident []byte
		if joliet {
			ident = utf16be(f.Name)
		} else {
			ident = []byte(isoName(f.Name))
		}
		off += copy(dst[off:], dirRecord(ident, f.extent, uint32(len(f.Data)), false))
	}
}

func dirRecord(name []byte, extent, length uint32, dir bool) []byte {
	n := 33 + len(name)
	if n%2 == 1 {
		n++
	}
	b := make([]byte, n)
	b[0] = byte(n)
	putBoth32(b[2:], extent)
	putBoth32(b[10:], length)
	// 2026-01-01 00:00:00 GMT. A fixed stamp keeps the image deterministic.
	b[18], b[19], b[20] = 126, 1, 1
	if dir {
		b[25] = 2
	}
	putBoth16(b[28:], 1)
	b[32] = byte(len(name))
	copy(b[33:], name)
	return b
}

func putPathTable(dst []byte, root int, big bool) {
	dst[0] = 1
	if big {
		binary.BigEndian.PutUint32(dst[2:], uint32(root))
		binary.BigEndian.PutUint16(dst[6:], 1)
	} else {
		binary.LittleEndian.PutUint32(dst[2:], uint32(root))
		binary.LittleEndian.PutUint16(dst[6:], 1)
	}
}

func putPVD(b []byte, label string, volume uint32, lpath, mpath, root int, joliet bool) {
	if joliet {
		b[0] = 2
	} else {
		b[0] = 1
	}
	copy(b[1:], []byte("CD001"))
	b[6] = 1
	if joliet {
		putUCS2(b[8:40], "")
		putUCS2(b[40:72], label)
		copy(b[88:91], []byte("%/E"))
	} else {
		pad(b[8:40], "")
		pad(b[40:72], label)
	}
	putBoth32(b[80:], volume)
	putBoth16(b[120:], 1)
	putBoth16(b[124:], 1)
	putBoth16(b[128:], 2048)
	putBoth32(b[132:], 10) // one root path-table record, padded to 10 bytes
	binary.LittleEndian.PutUint32(b[140:], uint32(lpath))
	binary.BigEndian.PutUint32(b[148:], uint32(mpath))
	copy(b[156:], dirRecord([]byte{0}, uint32(root), 2048, true))
	pad(b[190:318], "")
	pad(b[318:446], "FELDCORE")
	pad(b[446:574], "FELDCORE")
	for _, off := range []int{813, 830, 847, 864} {
		copy(b[off:], []byte("0000000000000000"))
	}
	b[881] = 1
}

func putBoth16(b []byte, v uint16) {
	binary.LittleEndian.PutUint16(b, v)
	binary.BigEndian.PutUint16(b[2:], v)
}

func putBoth32(b []byte, v uint32) {
	binary.LittleEndian.PutUint32(b, v)
	binary.BigEndian.PutUint32(b[4:], v)
}

func pad(dst []byte, s string) {
	for i := range dst {
		dst[i] = ' '
	}
	if len(s) > len(dst) {
		s = s[:len(dst)]
	}
	copy(dst, s)
}

func putUCS2(dst []byte, s string) {
	for i := 0; i+1 < len(dst); i += 2 {
		dst[i] = 0
		dst[i+1] = ' '
	}
	runes := []rune(s)
	if len(runes) > len(dst)/2 {
		runes = runes[:len(dst)/2]
	}
	for i, r := range runes {
		dst[i*2] = byte(r >> 8)
		dst[i*2+1] = byte(r)
	}
}

func utf16be(s string) []byte {
	runes := []rune(s)
	out := make([]byte, len(runes)*2)
	for i, r := range runes {
		out[i*2] = byte(r >> 8)
		out[i*2+1] = byte(r)
	}
	return out
}

// isoName is a short ISO 9660 identifier. Joliet carries the real name.
func isoName(name string) string {
	up := strings.ToUpper(name)
	var b strings.Builder
	for _, r := range up {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			b.WriteRune(r)
		}
	}
	s := b.String()
	if s == "" {
		s = "FILE"
	}
	if len(s) > 8 {
		s = s[:8]
	}
	return s + ".;1"
}
