package hid

import (
	"NanoKVM-Server/service/controlmode"
	"NanoKVM-Server/service/inputcontrol"
)

type Service struct {
	hid         *Hid
	control     *controlmode.Manager
	coordinator *inputcontrol.Coordinator
	// allowManual gates HTTP manual input by control mode, mirroring the
	// WebSocket input path. Nil allows every mode.
	allowManual func(controlmode.Mode) bool
}

// SetManualInputPolicy installs the control-mode predicate for HTTP manual
// input. The router supplies it to avoid an import cycle with picoclaw.
func (s *Service) SetManualInputPolicy(allow func(controlmode.Mode) bool) {
	s.allowManual = allow
}

func NewService() *Service {
	return &Service{
		hid:         GetHid(),
		control:     controlmode.GetManager(),
		coordinator: inputcontrol.GetCoordinator(),
	}
}

func (s *Service) newManualSession() *inputcontrol.ManualSession {
	return inputcontrol.NewManualSession(s.control, s.coordinator)
}
