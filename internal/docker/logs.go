package docker

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net/url"
	"strconv"
)

// Logs returns the last `tail` lines of a container's combined output.
//
// Without a TTY the daemon multiplexes stdout and stderr into a single stream
// of frames, each an 8-byte header (stream id + 4-byte big-endian length)
// followed by that many bytes. With a TTY the stream is raw. We inspect the
// container to know which, then decode accordingly.
func (m *Manager) Logs(ctx context.Context, id string, tail int) (string, error) {
	tty, err := m.hasTTY(ctx, id)
	if err != nil {
		return "", err
	}

	q := url.Values{"stdout": {"1"}, "stderr": {"1"}, "timestamps": {"0"}}
	if tail > 0 {
		q.Set("tail", strconv.Itoa(tail))
	} else {
		q.Set("tail", "all")
	}
	resp, err := m.c.do(ctx, "GET", "/containers/"+esc(id)+"/logs", q, nil, "")
	if err != nil {
		return "", err
	}
	defer drain(resp)

	limited := io.LimitReader(resp.Body, 4<<20) // guard against huge logs
	if tty {
		raw, err := io.ReadAll(limited)
		return string(raw), err
	}
	return demux(limited)
}

// hasTTY reports whether the container was created with a TTY.
func (m *Manager) hasTTY(ctx context.Context, id string) (bool, error) {
	var inspect struct {
		Config struct {
			Tty bool `json:"Tty"`
		} `json:"Config"`
	}
	if err := m.c.getJSON(ctx, "/containers/"+esc(id)+"/json", nil, &inspect); err != nil {
		return false, err
	}
	return inspect.Config.Tty, nil
}

// demux decodes Docker's multiplexed stdout/stderr stream into plain text,
// preserving order. Malformed tails (a truncated final frame) are ignored.
func demux(r io.Reader) (string, error) {
	var out bytes.Buffer
	header := make([]byte, 8)
	for {
		if _, err := io.ReadFull(r, header); err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				return out.String(), nil
			}
			return out.String(), err
		}
		size := binary.BigEndian.Uint32(header[4:])
		if size == 0 {
			continue
		}
		if size > 4<<20 {
			return out.String(), fmt.Errorf("log frame too large")
		}
		if _, err := io.CopyN(&out, r, int64(size)); err != nil {
			return out.String(), nil
		}
	}
}
