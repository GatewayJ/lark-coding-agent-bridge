package intake

import "time"

type EventKind string

const (
	EventMessage    EventKind = "message"
	EventComment    EventKind = "comment"
	EventCardAction EventKind = "cardAction"
	EventReconnect  EventKind = "reconnect"
	EventKeepalive  EventKind = "keepalive"
	EventDisconnect EventKind = "disconnect"
)

type ChatType string

const (
	ChatTypeP2P   ChatType = "p2p"
	ChatTypeGroup ChatType = "group"
)

type ChatMode string

const (
	ChatModeP2P   ChatMode = "p2p"
	ChatModeGroup ChatMode = "group"
	ChatModeTopic ChatMode = "topic"
)

type Actor struct {
	OpenID  string
	UserID  string
	UnionID string
	Name    string
}

type Resource struct {
	Kind string
	ID   string
	Name string
	Size int64
	URL  string
}

type Mention struct {
	Key    string
	OpenID string
	UserID string
	Name   string
	IsBot  *bool
}

type SenderType string

const (
	SenderTypeUser SenderType = "user"
	SenderTypeBot  SenderType = "bot"
)

type MessageInput struct {
	MessageID        string
	ChatID           string
	ChatType         ChatType
	ResolvedMode     ChatMode
	ThreadID         string
	RootID           string
	ParentID         string
	ReplyToMessageID string
	Sender           Actor
	SenderType       SenderType
	Content          string
	RawContentType   string
	RawContent       any
	Resources        []Resource
	Mentions         []Mention
	MentionAll       bool
	MentionedBot     bool
	CreateTime       time.Time
	Metadata         map[string]any
}

type CommentInput struct {
	EventID      string
	FileToken    string
	FileType     string
	CommentID    string
	ReplyID      string
	Operator     Actor
	MentionedBot bool

	ExplicitScopeKey string
	InheritScopeKey  string
	BridgeReply      bool
	Metadata         map[string]any
	CreateTime       time.Time
}

type CardActionInput struct {
	EventID       string
	MessageID     string
	ChatID        string
	ChatType      ChatType
	ResolvedMode  ChatMode
	ThreadID      string
	Operator      Actor
	ActionValue   map[string]any
	FormValue     map[string]any
	RawContent    any
	CreateTime    time.Time
	ExplicitScope string
	InheritScope  string
	Metadata      map[string]any
}

type ReconnectPhase string

const (
	ReconnectReconnecting ReconnectPhase = "reconnecting"
	ReconnectRecovered    ReconnectPhase = "recovered"
	ReconnectFailed       ReconnectPhase = "failed"
)

type ReconnectInput struct {
	Phase               ReconnectPhase
	ConsecutiveAttempts int
	Error               string
	At                  time.Time
}

type ConnectionState string

const (
	ConnectionUnknown      ConnectionState = "unknown"
	ConnectionConnected    ConnectionState = "connected"
	ConnectionReconnecting ConnectionState = "reconnecting"
	ConnectionDisconnected ConnectionState = "disconnected"
)

type KeepaliveInput struct {
	State             ConnectionState
	ReconnectAttempts int
	NetworkReachable  bool
	ConsecutiveDown   int
	Slept             time.Duration
	At                time.Time
}

type DisconnectInput struct {
	Reason string
	At     time.Time
}

type NormalizedEvent struct {
	Kind EventKind

	Scope Scope
	Self  SelfLoopDecision

	Message    *MessageInput
	Comment    *CommentInput
	CardAction *CardActionInput
	Reconnect  *ReconnectInput
	Keepalive  *KeepaliveInput
	Disconnect *DisconnectInput
}
