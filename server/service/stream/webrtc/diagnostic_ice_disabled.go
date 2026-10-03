//go:build !nanokvm_profile

package webrtc

import pionwebrtc "github.com/pion/webrtc/v4"

func configureDiagnosticICEInterface(_ *pionwebrtc.SettingEngine) {}
