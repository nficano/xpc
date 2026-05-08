package arcp

// Envelope type constants per docs/PROTOCOL.md §6. Centralized here so that
// callers and tests can reference them without typo risk.
const (
	// Control messages.
	TypeSessionOpen     = "session.open"
	TypeSessionAccepted = "session.accepted"
	TypeSessionClose    = "session.close"
	TypePing            = "ping"
	TypePong            = "pong"
	TypeAck             = "ack"
	TypeNack            = "nack"
	TypeCancel          = "cancel"
	TypePermissionReq   = "permission.request"
	TypePermissionGrant = "permission.grant"
	TypePermissionDeny  = "permission.deny"

	// Execution messages.
	TypeToolInvoke   = "tool.invoke"
	TypeToolResult   = "tool.result"
	TypeToolError    = "tool.error"
	TypeJobAccepted  = "job.accepted"
	TypeJobStarted   = "job.started"
	TypeJobProgress  = "job.progress"
	TypeJobCompleted = "job.completed"
	TypeJobFailed    = "job.failed"
	TypeJobCancelled = "job.cancelled"

	// Streaming messages.
	TypeStreamOpen  = "stream.open"
	TypeStreamChunk = "stream.chunk"
	TypeStreamClose = "stream.close"
	TypeStreamError = "stream.error"

	// Event messages.
	TypeLog = "log"
)

// Nack codes per docs/PROTOCOL.md §6.1 nack payload.
const (
	NackAuthFailed          = "auth_failed"
	NackDuplicateID         = "duplicate_id"
	NackTimestampOutOfRange = "timestamp_out_of_window"
	NackInvalidEnvelope     = "invalid_envelope"
	NackUnsupportedType     = "unsupported_type"
	NackRateLimited         = "rate_limited"
)

// AllTypes is the v0 universe of message types. Useful for tests that
// iterate every type and for nack on unrecognized type.
var AllTypes = []string{
	TypeSessionOpen, TypeSessionAccepted, TypeSessionClose,
	TypePing, TypePong, TypeAck, TypeNack, TypeCancel,
	TypePermissionReq, TypePermissionGrant, TypePermissionDeny,
	TypeToolInvoke, TypeToolResult, TypeToolError,
	TypeJobAccepted, TypeJobStarted, TypeJobProgress,
	TypeJobCompleted, TypeJobFailed, TypeJobCancelled,
	TypeStreamOpen, TypeStreamChunk, TypeStreamClose, TypeStreamError,
	TypeLog,
}

// IsKnownType reports whether t is a recognized v0 message type.
func IsKnownType(t string) bool {
	for _, k := range AllTypes {
		if k == t {
			return true
		}
	}
	return false
}
