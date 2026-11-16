package osupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
)

const defaultManifestURL = "https://github.com/Herculade0/FELDCORE/releases/latest/download/feldcore-os.json"

func (s *Service) manifestURL() string {
	if strings.TrimSpace(s.ManifestURL) != "" {
		return strings.TrimSpace(s.ManifestURL)
	}
	if s.Store != nil {
		if u := strings.TrimSpace(s.Store.OSConfig().ManifestURL); u != "" {
			return u
		}
	}
	return defaultManifestURL
}

func (s *Service) urlLocked() bool { return strings.TrimSpace(s.ManifestURL) != "" }

func validateUpdateURL(raw string, allowLocal bool) error {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 2048 {
		return input("The update address is not valid.")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil {
		return input("The update address is not valid.")
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
	case "http":
		if !allowLocal {
			return input("The update address has to be https.")
		}
	default:
		return input("The update address has to be https.")
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if host == "" || blockedName(host) {
		return input("That update address is blocked.")
	}
	if ip := net.ParseIP(host); ip != nil {
		if err := checkIP(ip, allowLocal); err != nil {
			return err
		}
	}
	return nil
}

func blockedName(host string) bool {
	switch host {
	case "localhost", "localhost.localdomain", "metadata", "metadata.google.internal", "metadata.goog":
		return true
	}
	return strings.HasSuffix(host, ".localhost")
}

func checkIP(ip net.IP, allowLocal bool) error {
	ip = ip.To16()
	if ip == nil {
		return input("That update address is blocked.")
	}
	if ip.IsLoopback() || ip.IsUnspecified() {
		if allowLocal && ip.IsLoopback() {
			return nil
		}
		return input("That update address is blocked.")
	}
	if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() {
		return input("That update address is blocked.")
	}
	// Cloud metadata sits in link-local, already refused. Keep the well-known
	// address explicit in case a future dialer treats it as global.
	if ip.Equal(net.ParseIP("169.254.169.254")) {
		return input("That update address is blocked.")
	}
	return nil
}

func (s *Service) httpClient() *http.Client {
	if s.Client != nil {
		return s.Client
	}
	dialer := &net.Dialer{Timeout: 20 * time.Second, KeepAlive: 30 * time.Second}
	allow := s.AllowLocal
	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return dialChecked(ctx, dialer, network, addr, allow)
		},
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		IdleConnTimeout:       30 * time.Second,
	}
	return &http.Client{
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			return validateUpdateURL(req.URL.String(), allow)
		},
	}
}

func dialChecked(ctx context.Context, d *net.Dialer, network, addr string, allowLocal bool) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	if ip := net.ParseIP(host); ip != nil {
		if err := checkIP(ip, allowLocal); err != nil {
			return nil, err
		}
		return d.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	if len(ips) == 0 {
		return nil, input("The update address did not resolve.")
	}
	for _, ip := range ips {
		if err := checkIP(ip.IP, allowLocal); err != nil {
			return nil, err
		}
	}
	var last error
	for _, ip := range ips {
		c, err := d.DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
		if err == nil {
			return c, nil
		}
		last = err
	}
	if last == nil {
		last = input("The update address did not connect.")
	}
	return nil, last
}

func artifactURL(manifestURL, name string) (string, error) {
	base, err := url.Parse(manifestURL)
	if err != nil {
		return "", input("The update address is not valid.")
	}
	ref, err := url.Parse(name)
	if err != nil || ref.IsAbs() || strings.Contains(name, "/") {
		return "", input("The release file name is not valid.")
	}
	return base.ResolveReference(ref).String(), nil
}

type progressFunc func(done, total int64)

// download writes the decoded body to w. When exact is set, want is the
// signed size and the transfer must match it. Otherwise want is a cap.
func (s *Service) download(ctx context.Context, rawURL, encoding string, w io.Writer, want int64, exact bool, progress progressFunc) (string, int64, error) {
	if err := validateUpdateURL(rawURL, s.AllowLocal); err != nil {
		return "", 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", 0, op("Could not request the release.")
	}
	req.Header.Set("User-Agent", "feldcore-os-update")
	req.Header.Set("Accept", "*/*")
	resp, err := s.httpClient().Do(req)
	if err != nil {
		return "", 0, op("Could not download the release.")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", 0, op("The release server returned " + resp.Status + ".")
	}
	var src io.Reader = resp.Body
	if encoding == "zstd" {
		dec, err := zstd.NewReader(resp.Body)
		if err != nil {
			return "", 0, op("Could not decode the release.")
		}
		defer dec.Close()
		src = dec
	}
	// One extra byte tells us the file is longer than the signed size.
	limit := want
	if limit <= 0 {
		limit = maxManifest
	}
	src = io.LimitReader(src, limit+1)
	h := sha256.New()
	dst := io.MultiWriter(h, w)
	var done int64
	buf := make([]byte, 256<<10)
	var last int64
	for {
		n, rerr := src.Read(buf)
		if n > 0 {
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return "", done, op("Could not write the release.")
			}
			done += int64(n)
			if done > limit {
				if exact {
					return "", done, input("The release is larger than the signed size.")
				}
				return "", done, input("The download is too large.")
			}
			if progress != nil && (done-last >= 4<<20 || rerr == io.EOF) {
				progress(done, want)
				last = done
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return "", done, op("The download stopped before it finished.")
		}
	}
	if progress != nil {
		progress(done, want)
	}
	if exact && done != want {
		return "", done, input("The release is not the signed size.")
	}
	return hex.EncodeToString(h.Sum(nil)), done, nil
}
