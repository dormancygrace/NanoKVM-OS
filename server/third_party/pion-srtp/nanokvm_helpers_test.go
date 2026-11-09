// SPDX-FileCopyrightText: The Pion community <https://pion.ly>
// SPDX-License-Identifier: MIT

// Test helpers copied from the upstream session_srtp_test.go, which NanoKVM does not keep.

package srtp

import (
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
)

func buildSessionSRTP(t *testing.T) (*SessionSRTP, net.Conn, *Config) { //nolint:dupl
	t.Helper()

	aPipe, bPipe := net.Pipe()
	config := &Config{
		Profile: ProtectionProfileAes128CmHmacSha1_80,
		Keys: SessionKeys{
			[]byte{0xE1, 0xF9, 0x7A, 0x0D, 0x3E, 0x01, 0x8B, 0xE0, 0xD6, 0x4F, 0xA3, 0x2C, 0x06, 0xDE, 0x41, 0x39},
			[]byte{0x0E, 0xC6, 0x75, 0xAD, 0x49, 0x8A, 0xFE, 0xEB, 0xB6, 0x96, 0x0B, 0x3A, 0xAB, 0xE6},
			[]byte{0xE1, 0xF9, 0x7A, 0x0D, 0x3E, 0x01, 0x8B, 0xE0, 0xD6, 0x4F, 0xA3, 0x2C, 0x06, 0xDE, 0x41, 0x39},
			[]byte{0x0E, 0xC6, 0x75, 0xAD, 0x49, 0x8A, 0xFE, 0xEB, 0xB6, 0x96, 0x0B, 0x3A, 0xAB, 0xE6},
		},
	}

	aSession, err := NewSessionSRTP(aPipe, config)
	assert.NoError(t, err)
	assert.NotNil(t, aSession, "NewSessionSRTP did not error, but returned nil session")

	return aSession, bPipe, config
}

func buildSessionSRTPPair(t *testing.T) (*SessionSRTP, *SessionSRTP) { //nolint:dupl
	t.Helper()

	aSession, bPipe, config := buildSessionSRTP(t)
	bSession, err := NewSessionSRTP(bPipe, config)
	assert.NoError(t, err)
	assert.NotNil(t, bSession, "NewSessionSRTP did not error, but returned nil session")

	return aSession, bSession
}
