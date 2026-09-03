package api

// ProtocolVersion is the only schema_version this server accepts, per
// docs/PROTOCOL_V1.md. A future v2 gets its own allowlist entry, not a
// silent relaxation of this one.
const ProtocolVersion = 1

// usageWindowRequest mirrors exactly the two fields docs/PROTOCOL_V1.md
// allows for a window — nothing else. There is deliberately no
// map[string]any anywhere in this file: an unrecognized field must be a
// hard decode error (see dec.DisallowUnknownFields in handlers.go), never
// silently accepted into a generic bag.
type usageWindowRequest struct {
	UsedPercent float64 `json:"used_percent"`
	ResetAt     string  `json:"reset_at"`
}

type usageRequest struct {
	SchemaVersion int                 `json:"schema_version"`
	Provider      string              `json:"provider"`
	ObservedAt    string              `json:"observed_at"`
	FiveHour      *usageWindowRequest `json:"five_hour,omitempty"`
	Weekly        *usageWindowRequest `json:"weekly,omitempty"`
}

type usageResponse struct {
	Accepted  bool `json:"accepted"`
	Persisted bool `json:"persisted"`
}

// errorResponse is deliberately terse: a short machine-readable category
// and a short human hint, never a raw parse error, stack trace, file path,
// or SQL error string.
type errorResponse struct {
	Error  string `json:"error"`
	Detail string `json:"detail,omitempty"`
}

type healthResponse struct {
	Status          string `json:"status"`
	ProtocolVersion int    `json:"protocol_version"`
	ServerTime      string `json:"server_time"`
}

type statusResponse struct {
	ProtocolVersion int    `json:"protocol_version"`
	DeviceValid     bool   `json:"device_valid"`
	ServerTime      string `json:"server_time"`
}

// pairRequest mirrors exactly docs/PROTOCOL_V1.md's pairing exchange —
// nothing else. No Telegram id, no user id, no destination/text/url/path
// ever appears here; the server resolves the user from the pairing code
// itself (see Store.RedeemPairingCode), never from anything the client
// supplies.
type pairRequest struct {
	Code          string `json:"code"`
	ClientVersion string `json:"client_version"`
	Platform      string `json:"platform"`
}

type pairResponse struct {
	Linked      bool   `json:"linked"`
	DeviceID    string `json:"device_id"`
	DeviceToken string `json:"device_token"`
}
