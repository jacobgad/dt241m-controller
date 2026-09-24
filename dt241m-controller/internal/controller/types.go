package controller

import (
	"log/slog"
	"time"

	"github.com/jacobgad/dt241m-controller/internal/config"
	"github.com/jacobgad/dt241m-controller/internal/dt241m"
	"github.com/jacobgad/dt241m-controller/internal/mqtt"
	"github.com/jacobgad/dt241m-controller/internal/store"
)

// Readback bounds how long a write waits for the device to report the new channel.
type Readback struct {
	Attempts int
	Gap      time.Duration
}

// DefaultReadback is used when Deps.Readback.Attempts is zero.
var DefaultReadback = Readback{Attempts: 3, Gap: 400 * time.Millisecond}

// Deps wires a Controller. Zero values for Log, Now and Readback pick defaults.
type Deps struct {
	Client   dt241m.Client
	MQTT     mqtt.Connection
	Store    store.Store
	Options  config.Options
	Log      *slog.Logger
	Origin   mqtt.Origin
	Now      func() time.Time
	Readback Readback
}

// Status is the result class of a channel change.
type Status string

// Channel change statuses.
const (
	StatusMatched  Status = "matched"
	StatusMismatch Status = "mismatch"
	StatusFailed   Status = "failed"
)

// WriteStatus records what the device said about the write itself, independent of readback.
type WriteStatus string

// Write acknowledgement states.
const (
	WriteAccepted  WriteStatus = "accepted"
	WriteRejected  WriteStatus = "rejected"
	WriteAmbiguous WriteStatus = "ambiguous"
)

// FailureReason explains a StatusFailed outcome or a RejectedError.
type FailureReason string

// Failure reasons.
const (
	ReasonDeviceNotLocated FailureReason = "device_not_located"
	ReasonUnknownRole      FailureReason = "unknown_role"
	ReasonUnknownSource    FailureReason = "unknown_source"
	ReasonWriteRejected    FailureReason = "write_rejected"
	ReasonInvalidChannel   FailureReason = "invalid_channel"
	ReasonInvalidName      FailureReason = "invalid_name"
	ReasonUnknownDevice    FailureReason = "unknown_device"
	ReasonShuttingDown     FailureReason = "shutting_down"
)

// Outcome is the fully observed result of a channel change: what was asked, what the
// device acknowledged, and what it reported afterwards.
type Outcome struct {
	Status    Status
	MAC       string
	IP        string
	Requested int
	Reported  *int
	Reason    FailureReason
	Write     WriteStatus
}

// RejectedError is returned when a request is refused before anything is sent.
type RejectedError struct {
	Reason FailureReason
	Detail string
}

func (e *RejectedError) Error() string {
	if e.Detail != "" {
		return "request rejected: " + string(e.Reason) + ": " + e.Detail
	}
	return "request rejected: " + string(e.Reason)
}
