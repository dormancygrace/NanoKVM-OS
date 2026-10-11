package proto

type GetHidModeRsp struct {
	Mode string `json:"mode"` // normal or hid-only
}

type GetKeyboardLedStatusRsp struct {
	KeyboardEnabled bool   `json:"keyboardEnabled"`
	NumLock         bool   `json:"numLock"`
	CapsLock        bool   `json:"capsLock"`
	ScrollLock      bool   `json:"scrollLock"`
	Known           bool   `json:"known"`
	UpdatedAt       string `json:"updatedAt"`
}

type SetHidModeReq struct {
	Mode string `validate:"required"` // normal or hid-only
}

type ShortcutKey struct {
	Code  string `json:"code"`
	Label string `json:"label"`
}

type Shortcut struct {
	ID   string        `json:"id"`
	Keys []ShortcutKey `json:"keys"`
}

type GetShortcutsRsp struct {
	Shortcuts []Shortcut `json:"shortcuts"`
}

type AddShortcutReq struct {
	Keys []ShortcutKey `validate:"required"`
}

type DeleteShortcutReq struct {
	ID string `validate:"required"`
}

// KeySequenceStartedRsp answers a key sequence request: for REISUB the
// steps go on after the response, for the others they are done.
type KeySequenceStartedRsp struct {
	Sequence   string `json:"sequence"`
	DurationMs int    `json:"durationMs"`
}

type KeySequenceResult struct {
	Sequence   string `json:"sequence"`
	Ok         bool   `json:"ok"`
	Error      string `json:"error,omitempty"`
	FinishedAt string `json:"finishedAt"`
}

type KeySequenceStatusRsp struct {
	Running     bool               `json:"running"`
	Sequence    string             `json:"sequence,omitempty"`
	StartedAt   string             `json:"startedAt,omitempty"`
	DurationMs  int                `json:"durationMs,omitempty"`
	RemainingMs int                `json:"remainingMs,omitempty"`
	LastResult  *KeySequenceResult `json:"lastResult,omitempty"`
}

type SetLeaderKeyReq struct {
	Key string `validate:"omitempty"`
}

type GetLeaderKeyRsp struct {
	Key string `json:"key"`
}
