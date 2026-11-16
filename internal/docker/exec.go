package docker

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
)

// idPattern is a Docker name or id. It keeps the exec path from escaping the API.
var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)

// Console attaches to a new shell in a running container, the same session
// `docker exec -it` opens. bash is used when the image has it, otherwise sh.
// cleanup closes the session and must run when the browser disconnects.
func (m *Manager) Console(ctx context.Context, id string) (io.ReadWriteCloser, func(rows, cols uint16) error, func(), error) {
	if !idPattern.MatchString(id) {
		return nil, nil, nil, invalid("That container id is not valid.")
	}
	var created struct {
		ID string `json:"Id"`
	}
	err := m.c.postJSON(ctx, "/containers/"+esc(id)+"/exec", nil, map[string]any{
		"AttachStdin":  true,
		"AttachStdout": true,
		"AttachStderr": true,
		"Tty":          true,
		// Try bash, then sh, inside the container.
		"Cmd": []string{"sh", "-c", "command -v bash >/dev/null 2>&1 && exec bash || exec sh"},
	}, &created)
	if err != nil {
		return nil, nil, nil, err
	}
	if created.ID == "" {
		return nil, nil, nil, fmt.Errorf("Docker did not start a console")
	}
	conn, err := m.c.hijack(ctx, "/exec/"+esc(created.ID)+"/start", []byte(`{"Detach":false,"Tty":true}`))
	if err != nil {
		return nil, nil, nil, err
	}
	resize := func(rows, cols uint16) error {
		q := url.Values{
			"h": {strconv.Itoa(int(rows))},
			"w": {strconv.Itoa(int(cols))},
		}
		return m.c.postJSON(context.Background(), "/exec/"+esc(created.ID)+"/resize", q, nil, nil)
	}
	cleanup := func() { _ = conn.Close() }
	return conn, resize, cleanup, nil
}

// hijack dials the engine socket and upgrades one request, the way the CLI
// attaches to an exec session. The returned conn is the raw TTY stream.
func (c *Client) hijack(ctx context.Context, path string, payload []byte) (net.Conn, error) {
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "unix", c.socket)
	if err != nil {
		return nil, dialError(err)
	}
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "POST %s HTTP/1.1\r\n", c.prefix(ctx)+path)
	fmt.Fprintf(&buf, "Host: docker\r\nConnection: Upgrade\r\nUpgrade: tcp\r\nContent-Type: application/json\r\nContent-Length: %d\r\n\r\n", len(payload))
	buf.Write(payload)
	if _, err := conn.Write(buf.Bytes()); err != nil {
		conn.Close()
		return nil, err
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		conn.Close()
		return nil, err
	}
	if resp.StatusCode >= 400 {
		msg := errorMessage(resp.Body)
		conn.Close()
		return nil, &APIError{Status: resp.StatusCode, Message: msg}
	}
	if resp.StatusCode != http.StatusSwitchingProtocols && resp.StatusCode != http.StatusOK {
		conn.Close()
		return nil, fmt.Errorf("docker attach returned HTTP %d", resp.StatusCode)
	}
	return &streamConn{Conn: conn, r: br}, nil
}

// streamConn reads leftover buffered response bytes before the socket.
type streamConn struct {
	net.Conn
	r *bufio.Reader
}

func (s *streamConn) Read(p []byte) (int, error) { return s.r.Read(p) }
