package download

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const imageCurlPath = "/usr/bin/curl"

// curl uses the firmware's OpenSSL implementation instead of Go's generic
// RISC-V AES/GHASH. Prefer ChaCha for both TLS versions without disabling
// certificate verification or falling back to unencrypted HTTP.
func curlImageArgs(rawURL, headers string, preferChaCha bool) []string {
	args := []string{"--disable", "--silent", "--show-error", "--fail", "--location", "--max-redirs", "10",
		"--connect-timeout", "10", "--proto", "=http,https", "--proto-redir", "=http,https",
		"--tlsv1.2", "--dump-header", headers}
	if strings.HasPrefix(strings.ToLower(rawURL), "https:") {
		args = append(args, "--proto-redir", "=https")
	}
	if preferChaCha {
		args = append(args, "--tls13-ciphers", "TLS_CHACHA20_POLY1305_SHA256",
			"--ciphers", "ECDHE-RSA-CHACHA20-POLY1305:ECDHE-ECDSA-CHACHA20-POLY1305")
	}
	return append(args, "--url", rawURL)
}

func downloadCurlImage(ctx context.Context, rawURL string, dst *os.File, expected []byte, progress func(string)) error {
	return downloadCurlTransfer(ctx, rawURL, dst, expected, progress, nil)
}

func downloadCurlTransfer(ctx context.Context, rawURL string, dst *os.File, expected []byte, progress func(string), resume *imageResume) error {
	offset, err := dst.Seek(0, io.SeekCurrent)
	if err != nil {
		return err
	}
	if offset > 0 && (resume == nil || !strongETag(resume.ETag) || resume.Total <= offset) {
		return errors.New("partial image has no valid resume validator")
	}
	headers, err := os.CreateTemp("", "nanokvm-image-headers-*")
	if err != nil {
		return err
	}
	headerPath := headers.Name()
	headers.Close()
	defer os.Remove(headerPath)
	// Without a requested checksum, curl writes directly to the already-open
	// destination FD. No pipe or Go buffer copy is needed for the image body.
	var output io.Writer = dst
	hasher := sha256.New()
	if expected != nil {
		if offset > 0 {
			if _, err := io.Copy(hasher, &imageContextReader{ctx, io.NewSectionReader(dst, 0, offset)}); err != nil {
				return err
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
		}
		output = io.MultiWriter(dst, hasher)
	}
	stop, stopped := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(2500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				meta := readImageHeaders(headerPath)
				total := meta.Length
				if offset > 0 {
					total = resume.Total
				} else if resume != nil && meta.Status == 200 && strongETag(meta.ETag) && meta.Length > 0 && resume.ETag == "" {
					resume.ETag, resume.Total = meta.ETag, meta.Length
					_ = saveImageResume(imageResumePath, resume)
				}
				info, err := dst.Stat()
				if err == nil && total > 0 && progress != nil {
					progress(fmt.Sprintf("%.2f%%", float64(info.Size())/float64(total)*100))
				}
			}
		}
	}()
	defer func() { close(stop); <-stopped }()
	for attempt := 0; attempt < 2; attempt++ {
		args := curlImageArgs(rawURL, headerPath, attempt == 0)
		if offset > 0 {
			args = append(args, "--continue-at", strconv.FormatInt(offset, 10), "--header", "If-Range: "+resume.ETag)
		}
		cmd := exec.CommandContext(ctx, imageCurlPath, args...)
		// Kill the transfer if its owning server dies. Cancellation also kills
		// curl and waits for the FD to close before cleanup or a later transfer.
		cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
		cmd.Stdout = output
		err = cmd.Start()
		if err == nil {
			_ = syscall.Setpriority(syscall.PRIO_PROCESS, cmd.Process.Pid, 10)
			err = cmd.Wait()
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err == nil {
			meta := readImageHeaders(headerPath)
			if offset == 0 && meta.Status != 200 {
				return fmt.Errorf("download response status %d", meta.Status)
			}
			if offset > 0 && (meta.Status != 206 || meta.Start != offset || meta.Total != resume.Total || meta.ETag != resume.ETag) {
				return errors.New("download resume response does not match partial image")
			}
			info, statErr := dst.Stat()
			if statErr != nil {
				return statErr
			}
			if offset > 0 && info.Size() != resume.Total {
				return errors.New("incomplete resumed image")
			}
			if expected != nil && !bytes.Equal(hasher.Sum(nil), expected) {
				return errSHA256Mismatch
			}
			return nil
		}
		// A server may offer only AES. Retry a negotiation failure before any body
		// bytes arrived, using OpenSSL's verified default suites. Never retry a
		// certificate failure or append a restarted response to a partial image.
		var exit *exec.ExitError
		info, statErr := dst.Stat()
		if attempt == 0 && errors.As(err, &exit) && exit.ExitCode() == 35 && statErr == nil && info.Size() == offset {
			continue
		}
		return fmt.Errorf("image transfer failed: %w", err)
	}
	return err
}
