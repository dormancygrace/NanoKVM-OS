package rustdesk

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"time"

	"NanoKVM-Server/service/stream/audio"
)

const maxAudioPacket = 1275

type audioSource interface {
	Read(context.Context) (audio.Packet, error)
	Close()
}

func (b *Bridge) acceptAudio(listener net.Listener) {
	for {
		c, err := listener.Accept()
		if err != nil {
			return
		}
		b.mu.Lock()
		if b.audio != listener || len(b.audioConnections) >= 8 {
			b.mu.Unlock()
			c.Close()
			continue
		}
		b.audioConnections[c] = struct{}{}
		b.mu.Unlock()
		go func() {
			defer func() { c.Close(); b.mu.Lock(); delete(b.audioConnections, c); b.mu.Unlock() }()
			b.serveAudio(c)
		}()
	}
}

// OKAF v1: magic[4], version, codec (0=unavailable, 1=Opus), channels,
// reserved, sample rate u32be, payload length u16be, reserved[2].
// A zero-length Opus packet advertises the format; it is never an audio frame.
func writeAudio(c net.Conn, codec byte, data []byte) error {
	if len(data) > maxAudioPacket {
		return errors.New("audio packet exceeds bound")
	}
	var h [16]byte
	copy(h[:4], "OKAF")
	h[4] = 1
	h[5] = codec
	if codec == 1 {
		h[6] = audio.Channels
		binary.BigEndian.PutUint32(h[8:12], audio.SampleRate)
	}
	binary.BigEndian.PutUint16(h[12:14], uint16(len(data)))
	if err := c.SetWriteDeadline(time.Now().Add(2 * time.Second)); err != nil {
		return err
	}
	for _, part := range [][]byte{h[:], data} {
		for len(part) > 0 {
			n, err := c.Write(part)
			if err != nil {
				return err
			}
			if n <= 0 {
				return io.ErrShortWrite
			}
			part = part[n:]
		}
	}
	return nil
}

func (b *Bridge) serveAudio(c net.Conn) {
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	reader := bufio.NewReaderSize(c, 256)
	line, err := reader.ReadSlice('\n')
	if err != nil || len(line) > 256 {
		return
	}
	var req struct {
		Version int    `json:"version"`
		Audio   string `json:"audio"`
	}
	decoder := json.NewDecoder(bytes.NewReader(line))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&req) != nil || req.Version != 1 || (req.Audio != "info" && req.Audio != "opus") {
		return
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return
	}
	if !b.audioEnabled() {
		_ = writeAudio(c, 0, nil)
		return
	}
	if req.Audio == "info" {
		_ = writeAudio(c, 1, nil)
		return
	}
	sub, err := b.audioSubscribe()
	if err != nil {
		_ = writeAudio(c, 0, nil)
		return
	}
	defer sub.Close()
	if writeAudio(c, 1, nil) != nil {
		return
	}
	_ = c.SetReadDeadline(time.Time{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Peer EOF, mute or app Stop releases this shared capture subscription even
	// when no PCM is arriving. The connection closes before the handler returns.
	done := make(chan struct{})
	go func() { defer close(done); var one [1]byte; _, _ = reader.Read(one[:]); cancel() }()
	defer func() { c.Close(); <-done }()
	for {
		p, err := sub.Read(ctx)
		if err != nil {
			return
		}
		if !b.audioEnabled() || len(p.Data) == 0 || len(p.Data) > maxAudioPacket {
			return
		}
		if writeAudio(c, 1, p.Data) != nil {
			return
		}
	}
}
