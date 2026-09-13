// Package audio shares one USB capture/Opus encoder between audio listeners.
package audio

import (
	"context"
	"encoding/binary"
	"errors"
	log "github.com/sirupsen/logrus"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const maxPacketBytes = 1275

type packet struct {
	data  []byte
	index uint64
}
type listener struct {
	frames chan packet
	done   chan struct{}
	once   sync.Once
}

func (l *listener) close() { l.once.Do(func() { close(l.done) }) }

type captureRun struct {
	cancel context.CancelFunc
	done   chan struct{}
}
type hub struct {
	mu      sync.Mutex
	clients map[*listener]struct{}
	run     *captureRun
}

var shared = hub{clients: make(map[*listener]struct{})}

func enabled() bool {
	info, err := os.Lstat("/sys/kernel/config/usb_gadget/g0/configs/c.1/uac1.audio0")
	return err == nil && info.Mode()&os.ModeSymlink != 0
}
func captureCard(root string) (string, error) {
	paths, err := filepath.Glob(filepath.Join(root, "card*", "id"))
	if err != nil {
		return "", err
	}
	for _, path := range paths {
		id, err := os.ReadFile(path)
		if err != nil || strings.TrimSpace(string(id)) != "UAC1Gadget" {
			continue
		}
		card := strings.TrimPrefix(filepath.Base(filepath.Dir(path)), "card")
		if n, err := strconv.Atoi(card); err == nil && n >= 0 && n < 256 {
			return card, nil
		}
	}
	return "", errors.New("USB audio capture card is unavailable")
}
func (h *hub) add() (*listener, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.clients) >= 8 {
		return nil, errors.New("too many audio listeners")
	}
	if len(h.clients) == 0 {
		// Never race an old process that still owns the exclusive ALSA capture fd.
		if h.run != nil {
			return nil, errors.New("USB audio is restarting")
		}
		ctx, cancel := context.WithCancel(context.Background())
		h.run = &captureRun{cancel: cancel, done: make(chan struct{})}
		go h.capture(ctx, h.run)
	} else if h.run == nil {
		return nil, errors.New("USB audio capture stopped")
	}
	client := &listener{frames: make(chan packet, 4), done: make(chan struct{})}
	h.clients[client] = struct{}{}
	return client, nil
}
func (h *hub) remove(client *listener) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.clients, client)
	if len(h.clients) == 0 && h.run != nil {
		h.run.cancel()
	}
}
func (h *hub) publish(frame packet) {
	for client := range h.clients {
		// Keep the most recent four frames (80 ms), independently for each listener.
		select {
		case client.frames <- frame:
		default:
			select {
			case <-client.frames:
			default:
			}
			select {
			case client.frames <- frame:
			default:
			}
		}
	}
}

// Keep listeners attached across capture failures. Resolve the card again on
// each attempt because USB re-enumeration may change its ALSA card number.
func (h *hub) capture(ctx context.Context, run *captureRun) {
	defer func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		for client := range h.clients {
			client.close()
		}
		run.cancel()
		h.run = nil
		close(run.done)
	}()
	var index uint64
	backoff := 100 * time.Millisecond
	for ctx.Err() == nil && enabled() {
		card, err := captureCard("/sys/class/sound")
		if err == nil {
			cmd := exec.CommandContext(ctx, "/kvmapp/system/bin/usb-audio-capture", card)
			cmd.Stderr = os.Stderr
			var pipe io.ReadCloser
			pipe, err = cmd.StdoutPipe()
			if err == nil {
				err = cmd.Start()
				if err == nil {
					for ctx.Err() == nil {
						var frame []byte
						frame, err = readPacket(pipe)
						if err != nil {
							break
						}
						h.mu.Lock()
						if ctx.Err() == nil {
							h.publish(packet{data: frame, index: index})
							index++
						}
						h.mu.Unlock()
						backoff = 100 * time.Millisecond
					}
					_ = cmd.Process.Kill()
					_ = cmd.Wait()
				}
				_ = pipe.Close()
			}
		}
		if ctx.Err() != nil || !enabled() {
			return
		}
		log.Warnf("USB audio capture interrupted, retrying in %s: %v", backoff, err)
		if !waitRetry(ctx, backoff) {
			return
		}
		backoff *= 2
		if backoff > 2*time.Second {
			backoff = 2 * time.Second
		}
	}
}

func waitRetry(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// Stop releases ALSA before a USB rebind, including changes to other functions.
func Stop() error {
	shared.mu.Lock()
	run := shared.run
	for client := range shared.clients {
		client.close()
	}
	if run != nil {
		run.cancel()
	}
	shared.mu.Unlock()
	if run == nil {
		return nil
	}
	select {
	case <-run.done:
		return nil
	case <-time.After(2 * time.Second):
		return errors.New("USB audio capture did not stop")
	}
}
func readPacket(r io.Reader) ([]byte, error) {
	var header [2]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return nil, err
	}
	size := int(binary.BigEndian.Uint16(header[:]))
	if size < 1 || size > maxPacketBytes {
		return nil, errors.New("invalid Opus packet length")
	}
	frame := make([]byte, size)
	_, err := io.ReadFull(r, frame)
	return frame, err
}
