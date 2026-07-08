package intake

import (
	"crypto/sha256"
	"encoding/hex"
)

const CurrentThread = "current"

type ScopeSource string

const (
	ScopeSourceIM         ScopeSource = "im"
	ScopeSourceCard       ScopeSource = "card"
	ScopeSourceComment    ScopeSource = "comment"
	ScopeSourceReconnect  ScopeSource = "reconnect"
	ScopeSourceKeepalive  ScopeSource = "keepalive"
	ScopeSourceDisconnect ScopeSource = "disconnect"
)

type Scope struct {
	Key       string
	Source    ScopeSource
	ChatID    string
	ChatType  ChatType
	ChatMode  ChatMode
	ThreadID  string
	ActorID   string
	ParentKey string

	FileToken        string
	FileType         string
	CommentID        string
	CommentScopeKey  string
	DocumentScopeKey string
}

func MessageScope(input MessageInput) Scope {
	mode := normalizeMode(input.ChatType, input.ResolvedMode)
	if input.ThreadID != "" {
		mode = ChatModeTopic
	}
	return Scope{
		Key:      chatScopeKey(input.ChatID, input.ThreadID),
		Source:   ScopeSourceIM,
		ChatID:   input.ChatID,
		ChatType: input.ChatType,
		ChatMode: mode,
		ThreadID: input.ThreadID,
		ActorID:  input.Sender.OpenID,
	}
}

func CardActionScope(input CardActionInput) Scope {
	mode := normalizeMode(input.ChatType, input.ResolvedMode)
	if input.ThreadID != "" {
		mode = ChatModeTopic
	}
	key := input.ExplicitScope
	parent := ""
	if key == "" {
		key = input.InheritScope
		parent = input.InheritScope
	}
	if key == "" {
		key = chatScopeKey(input.ChatID, input.ThreadID)
	}
	return Scope{
		Key:       key,
		Source:    ScopeSourceCard,
		ChatID:    input.ChatID,
		ChatType:  input.ChatType,
		ChatMode:  mode,
		ThreadID:  input.ThreadID,
		ActorID:   input.Operator.OpenID,
		ParentKey: parent,
	}
}

func CommentScope(input CommentInput) Scope {
	commentKey := CommentScopeKey(input.FileToken, input.CommentID)
	docKey := CommentDocumentScopeKey(input.FileToken)
	key := input.ExplicitScopeKey
	parent := ""
	if key == "" {
		key = input.InheritScopeKey
		parent = input.InheritScopeKey
	}
	if key == "" {
		key = commentKey
	}
	return Scope{
		Key:              key,
		Source:           ScopeSourceComment,
		ActorID:          input.Operator.OpenID,
		ParentKey:        parent,
		FileToken:        input.FileToken,
		FileType:         input.FileType,
		CommentID:        input.CommentID,
		CommentScopeKey:  commentKey,
		DocumentScopeKey: docKey,
	}
}

func LifecycleScope(source ScopeSource) Scope {
	return Scope{Key: string(source), Source: source}
}

func CommentDocumentScopeKey(fileToken string) string {
	return "comment-doc:" + tokenDigest(fileToken)
}

func CommentScopeKey(fileToken, commentID string) string {
	return "comment:" + tokenDigest(fileToken+":"+commentID)
}

func chatScopeKey(chatID, threadID string) string {
	if threadID != "" {
		return chatID + ":" + threadID
	}
	return chatID
}

func normalizeMode(chatType ChatType, mode ChatMode) ChatMode {
	if mode != "" {
		return mode
	}
	if chatType == ChatTypeP2P {
		return ChatModeP2P
	}
	return ChatModeGroup
}

func tokenDigest(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])[:16]
}
