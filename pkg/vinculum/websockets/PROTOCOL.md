# Vinculum WebSocket Event Protocol

The Vinculum WebSocket protocol is JSON-based and using short keys for brevity and efficiency. Each message is a JSON object with specific keys that define the message type and content.

## Message Structure

All messages are JSON objects that may contain the following keys:

| Key | Description |
|-----|-------------|
| `k` | **Kind** - The type of message (string) |
| `t` | **Topic** - The topic or topic pattern (string) |
| `d` | **Data** - The event data/payload (obnject) |
| `e` | **Error** - An error message (string) |
| `i` | **ID** - A message identifier for matching responses with requests (integer or string)|

## Message Types

### Client to Server

#### Subscribe (`"s"`)
Subscribe to a topic pattern.

```json
{
  "k": "s",
  "t": "user/+/events",
  "i": "req-123"
}
```

- `t`: Topic pattern to subscribe to
- `i`: Optional client-chosen identifier for response matching

#### Unsubscribe (`"u"`)
Unsubscribe from a topic pattern.

```json
{
  "k": "u", 
  "t": "user/+/events",
  "i": "req-124"
}
```

- `t`: Topic pattern to unsubscribe from
- `i`: Optional client-chosen identifier for response matching

### Server to Client

#### Acknowledgment (`"a"`)
Positive acknowledgment of a client request.

```json
{
  "k": "a",
  "i": "req-123"
}
```

- `i`: Identifier from the original request

#### Negative Acknowledgement (NACK) (`"n"`)
Error response to a client request.

```json
{
  "k": "n",
  "i": "req-123",
  "e": "Invalid topic pattern"
}
```

- `i`: Identifier from the original request
- `e`: Error message describing what went wrong

### Bidirectional

#### Event Messages
Event messages have no `k` field and represent actual event data.

**Server to Client (Event Delivery):**
```json
{
  "t": "user/alice/login",
  "d": {
    "timestamp": "2023-12-01T10:30:00Z",
    "ip": "192.168.1.100"
  }
}
```

**Client to Server (Event Publishing - if allowed):**
```json
{
  "t": "user/alice/action",
  "d": {
    "action": "click",
    "element": "button-submit"
  },
  "i": "pub-456"
}
```

- `t`: The specific topic for this event
- `d`: The event payload/data
- `i`: Optional identifier - if included, server will send `"a"` or `"n"` response

## Protocol Flow Examples

### Subscription Flow
```
Client → Server: {"k": "s", "t": "notifications/#", "i": "sub-1"}
Server → Client: {"k": "a", "i": "sub-1"}
Server → Client: {"t": "notifications/email", "d": {"subject": "Welcome"}}
Server → Client: {"t": "notifications/sms", "d": {"message": "Code: 123456"}}
```

### Error Handling
```
Client → Server: {"k": "s", "t": "invalid/[pattern", "i": "sub-2"}
Server → Client: {"k": "n", "i": "sub-2", "e": "Invalid topic pattern syntax"}
```

### Event Publishing (if enabled)
```
Client → Server: {"t": "metrics/cpu", "d": {"usage": 85.2}, "i": "pub-1"}
Server → Client: {"k": "a", "i": "pub-1"}
```

## Implementation Notes

- **Compact Format**: Short keys minimize bandwidth usage
- **Optional IDs**: Clients can omit `i` for fire-and-forget messages
- **Topic Patterns**: Support MQTT-style wildcards (`+` for single level, `#` for multi-level)
- **Error Handling**: All requests with `i` receive either `"a"` or `"n"` responses
- **Event Streaming**: Messages without `k` are pure event data for maximum efficiency