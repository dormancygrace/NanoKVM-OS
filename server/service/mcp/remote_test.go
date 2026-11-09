package mcpservice

import (
	"errors"
)

type recordingHID struct {
	keyboard [][]byte
	relative [][]byte
	absolute [][]byte
	failAt   int
	writes   int
	onWrite  func(int)
}

func (h *recordingHID) record(target *[][]byte, report []byte) error {
	h.writes++
	if h.onWrite != nil {
		h.onWrite(h.writes)
	}
	*target = append(*target, append([]byte(nil), report...))
	if h.failAt > 0 && h.writes == h.failAt {
		return errors.New("write failed")
	}
	return nil
}

func (h *recordingHID) WriteKeyboardReport(report []byte) error {
	return h.record(&h.keyboard, report)
}

func (h *recordingHID) WriteRelativeMouseReport(report []byte) error {
	return h.record(&h.relative, report)
}

func (h *recordingHID) WriteAbsoluteMouseReport(report []byte) error {
	return h.record(&h.absolute, report)
}

func newRemoteWithHID(writer HIDWriter) *Remote {
	return &Remote{hid: writer}
}
