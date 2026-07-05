package messaging

import "time"

type Conversation struct {
	ConversationID string    `json:"conversation_id"`
	WorkspaceID    string    `json:"workspace_id"`
	Type           string    `json:"type"`
	Name           *string   `json:"name,omitempty"`
	CreatedBy      string    `json:"created_by"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
	Members        []string  `json:"members,omitempty"`
	LastMessage    *Message  `json:"last_message,omitempty"`
}

type Message struct {
	MessageID              string          `json:"message_id"`
	ConversationID         string          `json:"conversation_id"`
	SenderID               string          `json:"sender_id"`
	Body                   string          `json:"body"`
	AttachmentKey          *string         `json:"-"`
	AttachmentURL          *string         `json:"attachment_url,omitempty"`
	AttachmentKind         *string         `json:"attachment_kind,omitempty"`
	AttachmentName         *string         `json:"attachment_name,omitempty"`
	AttachmentSizeBytes    *int64          `json:"attachment_size_bytes,omitempty"`
	CreatedAt              time.Time       `json:"created_at"`
	UpdatedAt              *time.Time      `json:"updated_at,omitempty"`
	DeletedAt              *time.Time      `json:"-"`
	ScheduledFor           *time.Time      `json:"scheduled_for,omitempty"`
	Status                 string          `json:"status"`
	Reactions              []ReactionGroup `json:"reactions,omitempty"`
	ViewOnce               bool            `json:"view_once,omitempty"`
	Viewed                 bool            `json:"viewed,omitempty"`
	ForwardedFromMessageID *string         `json:"forwarded_from_message_id,omitempty"`
	ForwardedFromSenderID  *string         `json:"forwarded_from_sender_id,omitempty"`
	ThreadRootMessageID    *string         `json:"thread_root_message_id,omitempty"`
	ReplyCount             int             `json:"reply_count,omitempty"`
	SharedTaskID           *string         `json:"shared_task_id,omitempty"`
	SharedTaskTitle        *string         `json:"shared_task_title,omitempty"`
	SharedTaskStatus       *string         `json:"shared_task_status,omitempty"`
	SharedTaskNumber       *int            `json:"shared_task_number,omitempty"`
}

type ReactionGroup struct {
	Emoji   string   `json:"emoji"`
	Count   int      `json:"count"`
	UserIDs []string `json:"user_ids"`
	Reacted bool     `json:"reacted"`
}
