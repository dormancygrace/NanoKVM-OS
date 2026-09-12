package hid

import (
	log "github.com/sirupsen/logrus"
)

func (h *Hid) Mouse(queue <-chan []byte) {
	legacy := make(chan QueuedReport)
	go func() {
		defer close(legacy)
		for report := range queue {
			legacy <- QueuedReport{Data: report}
		}
	}()
	h.MouseReports(legacy)
}

func (h *Hid) MouseReports(queue <-chan QueuedReport) {
	h.mouseReports(queue, HID1, HID2)
}

func (h *Hid) mouseReports(queue <-chan QueuedReport, relativePath string, absolutePath string) {
	var execute func(func() error) error
	var resetRelativeMouse func()
	var resetAbsoluteMouse func()
	relativeButtonsActive := false
	absoluteButtonsActive := false
	absoluteReleaseReport := absoluteMouseReleaseReport(nil)
	defer func() {
		if relativeButtonsActive {
			if err := runCleanup(execute, func() error {
				return h.writeHID(h.relativeMouseDevice(relativePath), relativeMouseReleaseReport())
			}); err != nil {
				log.Errorf("release relative mouse on queue close failed: %s", err)
			} else if resetRelativeMouse != nil {
				resetRelativeMouse()
			}
		}
		if absoluteButtonsActive {
			if err := runCleanup(execute, func() error {
				return h.writeHID(h.absoluteMouseDevice(absolutePath), absoluteReleaseReport)
			}); err != nil {
				log.Errorf("release absolute mouse on queue close failed: %s", err)
			} else if resetAbsoluteMouse != nil {
				resetAbsoluteMouse()
			}
		}
	}()

	for event := range queue {
		event.Data = NormalizeMouseReport(event.Data)
		execute = event.Execute
		resetRelativeMouse = event.ResetRelativeMouse
		resetAbsoluteMouse = event.ResetAbsoluteMouse

		cleanupFailure := func(writeErr error) {
			log.Errorf("mouse HID write failed: %s", writeErr)
			if dropped := drainHIDQueue(queue); dropped > 0 {
				log.Debugf("dropped %d stale mouse HID reports after write failure", dropped)
			}

			if len(event.Data) == 5 && event.Data[0] != 0 {
				relativeButtonsActive = true
			}
			if len(event.Data) == 7 && event.Data[0] != 0 {
				absoluteButtonsActive = true
				absoluteReleaseReport = absoluteMouseReleaseReport(event.Data)
			}

			if relativeButtonsActive || len(event.Data) == 5 {
				if err := runCleanup(execute, func() error {
					return h.writeHID(h.relativeMouseDevice(relativePath), relativeMouseReleaseReport())
				}); err != nil {
					log.Errorf("release relative mouse after write failure failed: %s", err)
				} else {
					relativeButtonsActive = false
					if resetRelativeMouse != nil {
						resetRelativeMouse()
					}
				}
			}

			if absoluteButtonsActive || len(event.Data) == 7 {
				releaseReport := absoluteReleaseReport
				if len(event.Data) == 7 {
					releaseReport = absoluteMouseReleaseReport(event.Data)
				}
				if err := runCleanup(execute, func() error {
					return h.writeHID(h.absoluteMouseDevice(absolutePath), releaseReport)
				}); err != nil {
					log.Errorf("release absolute mouse after write failure failed: %s", err)
				} else {
					absoluteButtonsActive = false
					if resetAbsoluteMouse != nil {
						resetAbsoluteMouse()
					}
				}
			}

			event.complete(false)
		}

		switch len(event.Data) {
		case 5:
			if absoluteButtonsActive {
				if err := runCleanup(execute, func() error {
					return h.writeHID(h.absoluteMouseDevice(absolutePath), absoluteReleaseReport)
				}); err != nil {
					cleanupFailure(err)
					continue
				}
				absoluteButtonsActive = false
				if resetAbsoluteMouse != nil {
					resetAbsoluteMouse()
				}
			}

			if err := event.run(func() error {
				return h.writeHID(h.relativeMouseDevice(relativePath), event.Data)
			}); err != nil {
				cleanupFailure(err)
				continue
			}
			relativeButtonsActive = event.Data[0] != 0
			event.complete(true)
		case 7:
			if relativeButtonsActive {
				if err := runCleanup(execute, func() error {
					return h.writeHID(h.relativeMouseDevice(relativePath), relativeMouseReleaseReport())
				}); err != nil {
					cleanupFailure(err)
					continue
				}
				relativeButtonsActive = false
				if resetRelativeMouse != nil {
					resetRelativeMouse()
				}
			}

			if err := event.run(func() error {
				return h.writeHID(h.absoluteMouseDevice(absolutePath), event.Data)
			}); err != nil {
				cleanupFailure(err)
				continue
			}
			absoluteReleaseReport = absoluteMouseReleaseReport(event.Data)
			absoluteButtonsActive = event.Data[0] != 0
			event.complete(true)
		default:
			event.complete(false)
			log.Debugf("invalid mouse event: %v", event.Data)
		}
	}
}

func relativeMouseReleaseReport() []byte {
	return make([]byte, 5)
}

func absoluteMouseReleaseReport(positionReport []byte) []byte {
	report := make([]byte, 7)
	if len(positionReport) >= 5 {
		copy(report[1:5], positionReport[1:5])
	}
	return report
}

// NormalizeMouseReport accepts cached pre-AC-Pan clients as well as new reports.
// Lengths remain unambiguous: relative 4/5, absolute 6/7.
func NormalizeMouseReport(report []byte) []byte {
	if len(report) == 4 || len(report) == 6 {
		data := make([]byte, len(report)+1)
		copy(data, report)
		return data
	}
	return report
}
