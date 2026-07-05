package notification

import (
	"encoding/json"
	"sync"
)

type MessageSSEEvent struct {
	Type                   string  `json:"type"`
	ConversationID         string  `json:"conversation_id"`
	MessageID              string  `json:"message_id"`
	SenderID               string  `json:"sender_id"`
	Body                   string  `json:"body"`
	AttachmentURL          *string `json:"attachment_url,omitempty"`
	AttachmentKind         *string `json:"attachment_kind,omitempty"`
	AttachmentName         *string `json:"attachment_name,omitempty"`
	AttachmentSizeBytes    *int64  `json:"attachment_size_bytes,omitempty"`
	CreatedAt              string  `json:"created_at"`
	UpdatedAt              *string `json:"updated_at,omitempty"`
	ViewOnce               bool    `json:"view_once,omitempty"`
	Viewed                 bool    `json:"viewed,omitempty"`
	ForwardedFromMessageID *string `json:"forwarded_from_message_id,omitempty"`
	ForwardedFromSenderID  *string `json:"forwarded_from_sender_id,omitempty"`
	ThreadRootMessageID    *string `json:"thread_root_message_id,omitempty"`
	SharedTaskID           *string `json:"shared_task_id,omitempty"`
	SharedTaskTitle        *string `json:"shared_task_title,omitempty"`
	SharedTaskStatus       *string `json:"shared_task_status,omitempty"`
	SharedTaskNumber       *int    `json:"shared_task_number,omitempty"`
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

type Broker struct {
	mutex   sync.RWMutex
	clients map[string]map[chan string]bool
}

var GlobalBroker = NewBroker()

func NewBroker() *Broker {
	return &Broker{clients: make(map[string]map[chan string]bool)}
}

func (b *Broker) Register(userID string, ch chan string) {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	if b.clients[userID] == nil {
		b.clients[userID] = make(map[chan string]bool)
	}
	b.clients[userID][ch] = true
}

func (b *Broker) Unregister(userID string, ch chan string) {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	if chans, ok := b.clients[userID]; ok {
		delete(chans, ch)
		if len(chans) == 0 {
			delete(b.clients, userID)
		}
	}
	close(ch)
}

func (b *Broker) SendMessage(userID string, event MessageSSEEvent) bool {
	payload, err := json.Marshal(event)
	if err != nil {
		return false
	}
	return b.send(userID, string(payload))
}

func (b *Broker) SendReaction(userID string, event ReactionSSEEvent) bool {
	payload, err := json.Marshal(event)
	if err != nil {
		return false
	}
	return b.send(userID, string(payload))
}

func (b *Broker) SendMessageViewed(userID string, event MessageViewedSSEEvent) bool {
	payload, err := json.Marshal(event)
	if err != nil {
		return false
	}
	return b.send(userID, string(payload))
}

func (b *Broker) send(userID, msg string) bool {
	b.mutex.RLock()
	defer b.mutex.RUnlock()
	chans, ok := b.clients[userID]
	if !ok {
		return false
	}
	sent := false
	for ch := range chans {
		select {
		case ch <- msg:
			sent = true
		default:
		}
	}
	return sent
}
