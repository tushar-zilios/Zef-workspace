package notification

// Event payload shapes forwarded to Zef-backend's shared SSE stream via
// NotifyBackendMessagingEvent (see backend_client.go) — Zef-workspace no longer runs its
// own SSE broker; these types just define the JSON wire format the frontend parses.

type MessageSSEEvent struct {
	Type                   string   `json:"type"`
	ConversationID         string   `json:"conversation_id"`
	MessageID              string   `json:"message_id"`
	SenderID               string   `json:"sender_id"`
	SenderName             string   `json:"sender_name"`
	Body                   string   `json:"body"`
	AttachmentURL          *string  `json:"attachment_url,omitempty"`
	AttachmentKind         *string  `json:"attachment_kind,omitempty"`
	AttachmentName         *string  `json:"attachment_name,omitempty"`
	AttachmentSizeBytes    *int64   `json:"attachment_size_bytes,omitempty"`
	CreatedAt              string   `json:"created_at"`
	UpdatedAt              *string  `json:"updated_at,omitempty"`
	ViewOnce               bool     `json:"view_once,omitempty"`
	Viewed                 bool     `json:"viewed,omitempty"`
	ForwardedFromMessageID *string  `json:"forwarded_from_message_id,omitempty"`
	ForwardedFromSenderID  *string  `json:"forwarded_from_sender_id,omitempty"`
	ThreadRootMessageID    *string  `json:"thread_root_message_id,omitempty"`
	SharedTaskID           *string  `json:"shared_task_id,omitempty"`
	SharedTaskTitle        *string  `json:"shared_task_title,omitempty"`
	SharedTaskStatus       *string  `json:"shared_task_status,omitempty"`
	SharedTaskNumber       *int     `json:"shared_task_number,omitempty"`
	Poll                   *PollSSE `json:"poll,omitempty"`
}

type PollOptionSSE struct {
	OptionID  string `json:"option_id"`
	Text      string `json:"text"`
	Votes     int    `json:"votes"`
	VotedByMe bool   `json:"voted_by_me"`
	IsCorrect bool   `json:"is_correct,omitempty"`
}

type PollSSE struct {
	PollID      string          `json:"poll_id"`
	Question    string          `json:"question"`
	MultiChoice bool            `json:"multi_choice"`
	IsQuiz      bool            `json:"is_quiz"`
	Options     []PollOptionSSE `json:"options"`
}

// PollVoteSSEEvent notifies conversation members of an updated vote tally for a poll,
// mirroring ReactionSSEEvent's role for reactions.
type PollVoteSSEEvent struct {
	Type           string          `json:"type"` // "poll_vote"
	ConversationID string          `json:"conversation_id"`
	MessageID      string          `json:"message_id"`
	PollID         string          `json:"poll_id"`
	Options        []PollOptionSSE `json:"options"`
}

type MessageViewedSSEEvent struct {
	Type           string `json:"type"` // "message_viewed"
	ConversationID string `json:"conversation_id"`
	MessageID      string `json:"message_id"`
	ViewerID       string `json:"viewer_id"`
}

type ReactionGroupSSE struct {
	Emoji   string   `json:"emoji"`
	Count   int      `json:"count"`
	UserIDs []string `json:"user_ids"`
	Reacted bool     `json:"reacted"`
}

type ReactionSSEEvent struct {
	Type           string             `json:"type"`
	ConversationID string             `json:"conversation_id"`
	MessageID      string             `json:"message_id"`
	Reactions      []ReactionGroupSSE `json:"reactions"`
}
