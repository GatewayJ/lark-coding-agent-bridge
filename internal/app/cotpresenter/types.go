package cotpresenter

import "context"

type Mode string

const (
	ModeOff      Mode = "off"
	ModeBrief    Mode = "brief"
	ModeDetailed Mode = "detailed"
)

type Ref struct {
	COTID     string
	MessageID string
}

type Event struct {
	EventType string `json:"event_type"`
	Content   string `json:"content"`
	Timestamp int64  `json:"timestamp"`
}

type Client interface {
	CreateMessageCOT(ctx context.Context, req CreateRequest) (Ref, error)
	UpdateMessageCOT(ctx context.Context, req UpdateRequest) error
	CompleteMessageCOT(ctx context.Context, req CompleteRequest) error
}

type CreateRequest struct {
	ReceiveID       string
	OriginMessageID string
}

type UpdateRequest struct {
	Ref    Ref
	Events []Event
}

type CompleteRequest struct {
	Ref    Ref
	Reason string
}
