package notify

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"slices"
	"strings"
	"time"
)

// sendEmail sends the event over SMTP: implicit TLS (usually port 465),
// STARTTLS (587) or plain (25, an internal relay such as Exchange's that
// takes mail without a login). A login is sent only over TLS.
func sendEmail(ctx context.Context, ch Channel, r Rendered) error {
	host := ch.get("host")
	addr := net.JoinHostPort(host, ch.get("port"))
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(sendTimeout)
	}
	dialer := &net.Dialer{Deadline: deadline}
	config := tlsConfig(ch)
	config.ServerName = host

	var conn net.Conn
	var err error
	if ch.get("security") == "tls" {
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, config)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return err
	}
	_ = conn.SetDeadline(deadline)
	client, err := smtp.NewClient(conn, host)
	if err != nil {
		conn.Close()
		return err
	}
	defer client.Close()
	if err = client.Hello(helloName()); err != nil {
		return err
	}
	if ch.get("security") == "starttls" {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return permanent(errors.New("the server does not offer STARTTLS"))
		}
		if err = client.StartTLS(config); err != nil {
			return err
		}
	}
	if user := ch.get("username"); user != "" {
		if ch.get("security") == "none" {
			return permanent(errors.New("a login needs STARTTLS or TLS"))
		}
		if err = client.Auth(smtpAuth(client, host, user, ch.get("password"))); err != nil {
			return smtpError(err)
		}
	}
	// "From" may carry a display name: the envelope takes the address alone.
	from := ch.get("from")
	sender, err := mail.ParseAddress(from)
	if err != nil {
		return permanent(err)
	}
	if err = client.Mail(sender.Address); err != nil {
		return smtpError(err)
	}
	to := splitList(ch.get("to"))
	for _, rcpt := range to {
		if err = client.Rcpt(rcpt); err != nil {
			return smtpError(err)
		}
	}
	w, err := client.Data()
	if err != nil {
		return smtpError(err)
	}
	if _, err = w.Write(emailMessage(sender, to, r)); err != nil {
		return err
	}
	if err = w.Close(); err != nil {
		return smtpError(err)
	}
	return client.Quit()
}

// smtpAuth picks PLAIN, or LOGIN for servers that offer only it (older
// Exchange). Both refuse to send the password without TLS.
func smtpAuth(client *smtp.Client, host, user, password string) smtp.Auth {
	_, mechanisms := client.Extension("AUTH")
	if !slices.Contains(strings.Fields(mechanisms), "PLAIN") && slices.Contains(strings.Fields(mechanisms), "LOGIN") {
		return &loginAuth{host: host, user: user, password: password}
	}
	return smtp.PlainAuth("", user, password, host)
}

type loginAuth struct{ host, user, password string }

func (a *loginAuth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	if !server.TLS && server.Name != "localhost" {
		return "", nil, errors.New("unencrypted connection")
	}
	return "LOGIN", nil, nil
}

func (a *loginAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	switch strings.ToLower(strings.TrimSpace(string(fromServer))) {
	case "username:":
		return []byte(a.user), nil
	case "password:":
		return []byte(a.password), nil
	}
	return nil, fmt.Errorf("unexpected server challenge %q", fromServer)
}

// smtpError marks the server's 5xx answers (a refused login, sender or
// recipient) as permanent; 4xx are temporary by the protocol.
func smtpError(err error) error {
	var protoErr *textproto.Error
	if errors.As(err, &protoErr) && protoErr.Code >= 500 {
		return permanent(err)
	}
	return err
}

func helloName() string {
	name := deviceName()
	if strings.ContainsAny(name, " \t") {
		return "nanokvm"
	}
	return name
}

func emailMessage(from *mail.Address, to []string, r Rendered) []byte {
	var buf bytes.Buffer
	header := func(key, value string) { fmt.Fprintf(&buf, "%s: %s\r\n", key, value) }
	domain := "nanokvm"
	if at := strings.LastIndexByte(from.Address, '@'); at >= 0 {
		domain = from.Address[at+1:]
	}
	header("From", from.String())
	header("To", strings.Join(to, ", "))
	header("Subject", mime.QEncoding.Encode("utf-8", "["+r.Device+"] "+r.Title))
	header("Date", r.Time.Format(time.RFC1123Z))
	header("Message-ID", "<"+newID()+"."+fmt.Sprint(r.Time.Unix())+"@"+domain+">")
	// Out-of-office replies are not sent to it (RFC 3834).
	header("Auto-Submitted", "auto-generated")
	header("MIME-Version", "1.0")
	text := quotedPrintable(r.Text())
	if r.Image == nil {
		header("Content-Type", "text/plain; charset=utf-8")
		header("Content-Transfer-Encoding", "quoted-printable")
		buf.WriteString("\r\n")
		buf.Write(text)
		return buf.Bytes()
	}
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	header("Content-Type", "multipart/mixed; boundary="+w.Boundary())
	buf.WriteString("\r\n")
	part, _ := w.CreatePart(textproto.MIMEHeader{
		"Content-Type":              {"text/plain; charset=utf-8"},
		"Content-Transfer-Encoding": {"quoted-printable"},
	})
	_, _ = part.Write(text)
	part, _ = w.CreatePart(textproto.MIMEHeader{
		"Content-Type":              {"image/jpeg"},
		"Content-Transfer-Encoding": {"base64"},
		"Content-Disposition":       {`attachment; filename="screen.jpg"`},
	})
	encoded := base64.StdEncoding.EncodeToString(r.Image)
	for len(encoded) > 76 {
		_, _ = part.Write([]byte(encoded[:76] + "\r\n"))
		encoded = encoded[76:]
	}
	_, _ = part.Write([]byte(encoded + "\r\n"))
	_ = w.Close()
	buf.Write(body.Bytes())
	return buf.Bytes()
}

func quotedPrintable(text string) []byte {
	var buf bytes.Buffer
	w := quotedprintable.NewWriter(&buf)
	_, _ = w.Write([]byte(strings.ReplaceAll(text, "\n", "\r\n")))
	_ = w.Close()
	return buf.Bytes()
}
