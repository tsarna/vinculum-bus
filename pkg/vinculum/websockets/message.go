package websockets

// Message kind constants for the Vinculum WebSocket protocol.
// These correspond to the "k" (kind) field in wire messages.
const (
	// Client to Server message kinds
	MessageKindSubscribe   = "s" // Subscribe to a topic pattern
	MessageKindUnsubscribe = "u" // Unsubscribe from a topic pattern

	// Server to Client message kinds
	MessageKindAck  = "a" // Positive acknowledgment of client request
	MessageKindNack = "n" // Negative acknowledgment (error response)

	// Bidirectional - Event messages have no "k" field
	// They are identified by the presence of "t" (topic) and "d" (data) fields
	MessageKindEvent = "" // Event message
)

// WireMessage represents the JSON structure for WebSocket messages.
// This follows the Vinculum WebSocket protocol with short field names
// for efficiency.
type WireMessage struct {
	Kind  string `json:"k,omitempty"` // Message kind/type (see MessageKind constants)
	Topic string `json:"t,omitempty"` // Topic or topic pattern
	Data  any    `json:"d,omitempty"` // Event data/payload
	Id    any    `json:"i,omitempty"` // Message identifier for request/response matching
	Error string `json:"e,omitempty"` // Error message (used with NACK)
}
