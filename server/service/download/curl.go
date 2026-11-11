package download

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"NanoKVM-Server/internal/hwcrypto"
)

const imageCurlPath = "/usr/bin/curl"

// A transfer that lost its connection continues from the bytes it has. It
// gives up after imageRetryLimit attempts in a row that added nothing, waiting
// imageRetryDelay, then twice as long each time, at most 30 of them: a minute in all.
const imageRetryLimit = 6

var imageRetryDelay = time.Second

// curl uses the firmware's OpenSSL implementation instead of Go's generic
// RISC-V AES/GHASH. Prefer ChaCha for both TLS versions without disabling
// certificate verification or falling back to unencrypted HTTP. A connection
// that carries nothing for a minute counts as lost.
func curlImageArgs(rawURL, headers string, preferChaCha bool) []string {
	args := []string{"--disable", "--silent", "--show-error", "--fail", "--location", "--max-redirs", "10",
		"--connect-timeout", "10", "--speed-limit", "1", "--speed-time", "60",
		"--proto", "=http,https", "--proto-redir", "=http,https",
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

// curlLostConnection reports curl exit codes of a transfer cut off by the
// network: name lookup, connect, TLS handshake, short body, timeout, empty
// reply, receive error, HTTP/2 stream error. Not a refused status, a failed
// certificate, a server that cannot resume or a full disk.
func curlLostConnection(code int) bool {
	switch code {
	case 6, 7, 18, 28, 35, 52, 56, 92:
		return true
	}
	return false
}

func downloadCurlTransfer(ctx context.Context, rawURL string, dst *os.File, expected []byte, progress func(transferProgress), resume *imageResume) error {
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
	hasher := hwcrypto.NewSHA256()
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
	meter := newTransferMeter(offset)
	// One watcher per attempt: it reads offset and fills resume, which the
	// loop below touches only between attempts.
	watch := func(offset int64) func() {
		stop, stopped := make(chan struct{}), make(chan struct{})
		go func() {
			defer close(stopped)
			ticker := time.NewTicker(time.Second)
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
					if err == nil && progress != nil {
						progress(meter.sample(info.Size(), total, time.Now()))
					}
				}
			}
		}()
		return func() { close(stop); <-stopped }
	}
	preferChaCha, idle := true, 0
	for {
		// Free space changes while a transfer waits; the budget is for the rest.
		budget, err := imageWriteBudget(dst)
		if err != nil {
			return err
		}
		if offset > 0 && resume.Total-offset > budget {
			return errImageSpace
		}
		if err := os.Truncate(headerPath, 0); err != nil {
			return err
		}
		args := curlImageArgs(rawURL, headerPath, preferChaCha)
		args = append(args, "--max-filesize", strconv.FormatInt(budget, 10))
		if offset > 0 {
			args = append(args, "--continue-at", strconv.FormatInt(offset, 10), "--header", "If-Range: "+resume.ETag)
		}
		cmd := exec.CommandContext(ctx, imageCurlPath, args...)
		// Kill the transfer if its owning server dies. Cancellation also kills
		// curl and waits for the FD to close before cleanup or a later transfer.
		cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
		cmd.Stdout = output
		unwatch := watch(offset)
		err = cmd.Start()
		if err == nil {
			_ = syscall.Setpriority(syscall.PRIO_PROCESS, cmd.Process.Pid, 10)
			err = cmd.Wait()
		}
		unwatch()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		meta := readImageHeaders(headerPath)
		// What a resumed response must be before its bytes belong to the image.
		continues := offset == 0 || (meta.Status == 206 && meta.Start == offset && meta.Total == resume.Total && meta.ETag == resume.ETag)
		info, statErr := dst.Stat()
		if err == nil {
			if offset == 0 && meta.Status != 200 {
				return fmt.Errorf("download response status %d", meta.Status)
			}
			if !continues {
				return errors.New("download resume response does not match partial image")
			}
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
		failed := fmt.Errorf("image transfer failed: %w", err)
		var exit *exec.ExitError
		if !errors.As(err, &exit) || statErr != nil {
			return failed
		}
		if exit.ExitCode() == 63 {
			return errImageSpace
		}
		size := info.Size()
		// A server may offer only AES. Retry a negotiation failure before any body
		// bytes arrived, using OpenSSL's verified default suites. Never retry a
		// certificate failure or append a restarted response to a partial image.
		if preferChaCha && exit.ExitCode() == 35 && size == offset {
			preferChaCha = false
			continue
		}
		// A lost connection continues from what is on the card, when the
		// server named the version it was sending: nothing to continue without
		// a body, and no telling a changed file without a strong ETag.
		if !curlLostConnection(exit.ExitCode()) || size == 0 || resume == nil {
			return failed
		}
		if size > offset {
			if !continues {
				return errors.New("download resume response does not match partial image")
			}
			if offset == 0 {
				if meta.Status != 200 || !strongETag(meta.ETag) || meta.Length <= size {
					return failed
				}
				resume.ETag, resume.Total = meta.ETag, meta.Length
				_ = saveImageResume(imageResumePath, resume)
			}
			if size >= resume.Total {
				return failed
			}
			offset, idle = size, 0
		} else if idle++; idle >= imageRetryLimit {
			return failed
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(min(imageRetryDelay<<idle, 30*imageRetryDelay)):
		}
	}
}
