package notify

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"time"

	"github.com/Herculade0/feldcore/internal/store"
)

const (
	ModeSMTP   = "smtp"
	ModeBridge = "proton-bridge"
	ModeToken  = "proton-token"

	ProtonSMTPHost = "smtp.protonmail.ch"
	ProtonSMTPPort = 587
	BridgeHost     = "127.0.0.1"
	BridgePort     = 1025
	BridgeImage    = "shenxn/protonmail-bridge:latest"
	BridgeName     = "feldcore-proton-bridge"
)

// Message is one SMTP submission. Password is a secret and must not be logged.
type Message struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
	To       string
	TLS      string // starttls, tls, none
	Insecure bool
	Subject  string
	Body     string
}

// Resolve fills the server address for a Proton mode. Generic SMTP uses the
// fields as saved.
func Resolve(n store.Notify, password, subject, body string) (Message, error) {
	mode := n.Mode
	if mode == "" {
		mode = ModeSMTP
	}
	msg := Message{
		Host: n.Host, Port: n.Port, Username: n.Username, Password: password,
		From: n.From, To: n.To, TLS: n.TLS, Subject: subject, Body: body,
	}
	switch mode {
	case ModeSMTP:
	case ModeBridge:
		msg.Host = first(n.BridgeHost, BridgeHost)
		if n.BridgeManaged || n.BridgePort == 0 {
			msg.Port = BridgePort
			if n.BridgeManaged {
				msg.Host = BridgeHost
			}
		} else {
			msg.Port = n.BridgePort
		}
		if msg.TLS == "" {
			msg.TLS = "starttls"
		}
		// Bridge presents its own certificate. Verification is skipped only
		// for this mode, which is how Bridge is meant to be used.
		msg.Insecure = true
	case ModeToken:
		if msg.Host == "" {
			msg.Host = ProtonSMTPHost
		}
		if msg.Port == 0 {
			msg.Port = ProtonSMTPPort
		}
		if msg.TLS == "" {
			msg.TLS = "starttls"
		}
	default:
		return Message{}, input("Choose generic SMTP, Proton Mail Bridge, or a Proton SMTP token.")
	}
	if msg.TLS == "" {
		msg.TLS = "starttls"
	}
	switch msg.TLS {
	case "starttls", "tls", "none":
	default:
		return Message{}, input("TLS must be STARTTLS, TLS, or none.")
	}
	if msg.Port < 1 || msg.Port > 65535 {
		return Message{}, input("Enter an SMTP port.")
	}
	if strings.TrimSpace(msg.Host) == "" {
		return Message{}, input("Enter the SMTP server.")
	}
	if strings.TrimSpace(msg.To) == "" {
		return Message{}, input("Enter the address that should receive notifications.")
	}
	if strings.TrimSpace(msg.From) == "" {
		msg.From = msg.Username
	}
	if strings.TrimSpace(msg.From) == "" {
		return Message{}, input("Enter the From address.")
	}
	if msg.Password == "" {
		return Message{}, input("Enter the SMTP password or token.")
	}
	return msg, nil
}

// Send delivers one message. Errors are scrubbed of the password.
func Send(ctx context.Context, m Message) error {
	addr := net.JoinHostPort(m.Host, fmt.Sprintf("%d", m.Port))
	var (
		conn net.Conn
		err  error
	)
	dialer := net.Dialer{Timeout: 10 * time.Second}
	tlsCfg := &tls.Config{ServerName: m.Host, InsecureSkipVerify: m.Insecure} //nolint:gosec // Bridge's certificate is self-signed by design; Insecure is set only for that mode.
	if m.TLS == "tls" {
		conn, err = tls.DialWithDialer(&dialer, "tcp", addr, tlsCfg)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return op("Could not reach the mail server.")
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(20 * time.Second))
	client, err := smtp.NewClient(conn, m.Host)
	if err != nil {
		return op("The mail server did not answer.")
	}
	defer client.Close()
	if m.TLS == "starttls" {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return op("That server does not offer STARTTLS.")
		}
		if err := client.StartTLS(tlsCfg); err != nil {
			return op("STARTTLS failed.")
		}
	}
	auth := smtp.PlainAuth("", m.Username, m.Password, m.Host)
	if err := client.Auth(auth); err != nil {
		return op("The mail server refused the username or password.")
	}
	if err := client.Mail(m.From); err != nil {
		return op("The mail server rejected the From address.")
	}
	if err := client.Rcpt(m.To); err != nil {
		return op("The mail server rejected the recipient.")
	}
	w, err := client.Data()
	if err != nil {
		return op("The mail server rejected the message.")
	}
	body := strings.ReplaceAll(m.Body, "\n", "\r\n")
	subject := strings.ReplaceAll(m.Subject, "\r", "")
	subject = strings.ReplaceAll(subject, "\n", "")
	fmt.Fprintf(w, "From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n%s\r\n", m.From, m.To, subject, body)
	if err := w.Close(); err != nil {
		return op("The mail server did not accept the message.")
	}
	return client.Quit()
}

func first(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return strings.TrimSpace(a)
	}
	return b
}
