package messaging

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"workspace/src/internal/clients/audit"
	"workspace/src/internal/clients/gcsclient"
	messagingdb "workspace/src/internal/db/messaging"
	workspacedb "workspace/src/internal/db/workspace"
	messagingModels "workspace/src/internal/models/messaging"
	"workspace/src/internal/notification"
	"workspace/src/internal/utils"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
)

// maxMessageBodyRunes bounds message length so the DB TEXT column and SSE fanout can't be
// abused with unbounded payloads (the underlying column has no CHECK constraint).
const maxMessageBodyRunes = 10000

func userID(r *http.Request) string {
	uid, _ := r.Context().Value("user_id").(string)
	return uid
}

// resolveAttachmentURL populates m.AttachmentURL with a freshly signed, short-lived URL
// derived from the stored (private) object key, honoring per-viewer view-once state: the
// sender always gets a URL; other members get exactly one successful resolution (which
// durably marks the view via MarkMessageViewed), after which the URL is omitted.
func resolveAttachmentURL(ctx context.Context, m *messagingModels.Message, requestingUserID string) {
	if m.AttachmentKey == nil {
		return
	}
	if m.ViewOnce && m.SenderID != requestingUserID {
		firstReveal, err := messagingdb.MarkMessageViewed(ctx, m.MessageID, requestingUserID)
		if err != nil {
			return
		}
		if !firstReveal {
			m.Viewed = true
			return
		}
		m.Viewed = true
	}
	bucket := os.Getenv("GCS_BUCKET_NAME")
	if bucket == "" || gcsclient.GetClient() == nil {
		return
	}
	url, err := gcsclient.SignedURL(bucket, *m.AttachmentKey, signedURLExpiry)
	if err != nil {
		return
	}
	m.AttachmentURL = &url
}

func ListConversationsHandler(w http.ResponseWriter, r *http.Request) {
	workspaceID := r.URL.Query().Get("workspace_id")
	if workspaceID == "" {
		utils.WriteError(w, http.StatusBadRequest, "workspace_id is required")
		return
	}
	if err := workspacedb.EnsureWorkspace(r.Context(), workspaceID, userID(r)); err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "Failed to ensure workspace: "+err.Error())
		return
	}
	uid := userID(r)
	convs, err := messagingdb.ListConversations(r.Context(), workspaceID, uid)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "Failed to list conversations: "+err.Error())
		return
	}
	for i := range convs {
		// View-once messages never resolve a URL in the conversation-list preview — the
		// preview snippet must not burn the once-only reveal just for rendering a snippet.
		if convs[i].LastMessage != nil && !convs[i].LastMessage.ViewOnce {
			resolveAttachmentURL(r.Context(), convs[i].LastMessage, uid)
		}
	}
	utils.WriteJSON(w, http.StatusOK, convs)
}

type createConversationRequest struct {
	WorkspaceID string   `json:"workspace_id"`
	Type        string   `json:"type"`
	Name        *string  `json:"name,omitempty"`
	MemberIDs   []string `json:"member_ids"`
}

func CreateConversationHandler(w http.ResponseWriter, r *http.Request) {
	var req createConversationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.WriteError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	uid := userID(r)
	if req.WorkspaceID == "" {
		utils.WriteError(w, http.StatusBadRequest, "workspace_id is required")
		return
	}
	if err := workspacedb.EnsureWorkspace(r.Context(), req.WorkspaceID, uid); err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "Failed to ensure workspace: "+err.Error())
		return
	}
	switch req.Type {
	case "direct":
		if len(req.MemberIDs) != 1 || req.MemberIDs[0] == "" {
			utils.WriteError(w, http.StatusBadRequest, "direct conversations require exactly one member_id")
			return
		}
		existing, err := messagingdb.FindDirectConversation(r.Context(), req.WorkspaceID, uid, req.MemberIDs[0])
		if err == nil {
			utils.WriteJSON(w, http.StatusOK, existing)
			return
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			utils.WriteError(w, http.StatusInternalServerError, "Failed to check existing conversation: "+err.Error())
			return
		}
	case "group":
		if req.Name == nil || *req.Name == "" {
			utils.WriteError(w, http.StatusBadRequest, "group conversations require a name")
			return
		}
		if len(req.MemberIDs) == 0 {
			utils.WriteError(w, http.StatusBadRequest, "group conversations require at least one member_id")
			return
		}
	default:
		utils.WriteError(w, http.StatusBadRequest, "type must be 'direct' or 'group'")
		return
	}

	conv, err := messagingdb.CreateConversation(r.Context(), req.WorkspaceID, req.Type, req.Name, uid, req.MemberIDs)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "Failed to create conversation: "+err.Error())
		return
	}
	audit.Publish("conversation.create", "conversation", conv.ConversationID, uid, map[string]any{
		"workspace_id": req.WorkspaceID,
		"type":         req.Type,
	})
	utils.WriteJSON(w, http.StatusCreated, conv)
}

func ListMessagesHandler(w http.ResponseWriter, r *http.Request) {
	conversationID := chi.URLParam(r, "id")
	uid := userID(r)

	isMember, err := messagingdb.IsMember(r.Context(), conversationID, uid)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "Failed to verify membership: "+err.Error())
		return
	}
	if !isMember {
		utils.WriteError(w, http.StatusForbidden, "Not a member of this conversation")
		return
	}

	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	if limit > 100 {
		limit = 100
	}

	var before time.Time
	if b := r.URL.Query().Get("before"); b != "" {
		parsed, err := time.Parse(time.RFC3339, b)
		if err != nil {
			utils.WriteError(w, http.StatusBadRequest, "Invalid before timestamp, expected RFC3339")
			return
		}
		before = parsed
	}

	messages, err := messagingdb.ListMessages(r.Context(), conversationID, before, limit, uid)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "Failed to list messages: "+err.Error())
		return
	}
	for i := range messages {
		resolveAttachmentURL(r.Context(), &messages[i], uid)
	}
	utils.WriteJSON(w, http.StatusOK, messages)
}

type sendMessageRequest struct {
	SenderName          string       `json:"sender_name"`
	Body                string       `json:"body"`
	AttachmentKey       *string      `json:"attachment_key,omitempty"`
	AttachmentKind      *string      `json:"attachment_kind,omitempty"`
	AttachmentName      *string      `json:"attachment_name,omitempty"`
	AttachmentSizeBytes *int64       `json:"attachment_size_bytes,omitempty"`
	ScheduledFor        *time.Time   `json:"scheduled_for,omitempty"`
	ViewOnce            bool         `json:"view_once,omitempty"`
	ThreadRootMessageID *string      `json:"thread_root_message_id,omitempty"`
	SharedTaskID        *string      `json:"shared_task_id,omitempty"`
	SharedTaskTitle     *string      `json:"shared_task_title,omitempty"`
	SharedTaskStatus    *string      `json:"shared_task_status,omitempty"`
	SharedTaskNumber    *int         `json:"shared_task_number,omitempty"`
	Poll                *pollRequest `json:"poll,omitempty"`
}

type pollRequest struct {
	Question    string   `json:"question"`
	MultiChoice bool     `json:"multi_choice"`
	Options     []string `json:"options"`
}

func SendMessageHandler(w http.ResponseWriter, r *http.Request) {
	conversationID := chi.URLParam(r, "id")
	uid := userID(r)

	isMember, err := messagingdb.IsMember(r.Context(), conversationID, uid)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "Failed to verify membership: "+err.Error())
		return
	}
	if !isMember {
		utils.WriteError(w, http.StatusForbidden, "Not a member of this conversation")
		return
	}

	var req sendMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.WriteError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if req.Body == "" && req.AttachmentKey == nil && req.SharedTaskID == nil && req.Poll == nil {
		utils.WriteError(w, http.StatusBadRequest, "body, attachment, shared task, or poll is required")
		return
	}
	if utf8.RuneCountInString(req.Body) > maxMessageBodyRunes {
		utils.WriteError(w, http.StatusBadRequest, "body exceeds maximum length")
		return
	}
	if req.SenderName == "" {
		utils.WriteError(w, http.StatusBadRequest, "sender_name is required")
		return
	}
	if req.ScheduledFor != nil && !req.ScheduledFor.After(time.Now()) {
		utils.WriteError(w, http.StatusBadRequest, "scheduled_for must be in the future")
		return
	}
	if req.ViewOnce && req.AttachmentKey == nil {
		utils.WriteError(w, http.StatusBadRequest, "view_once requires an attachment")
		return
	}
	if req.ThreadRootMessageID != nil {
		if err := messagingdb.ValidateReplyTarget(r.Context(), *req.ThreadRootMessageID, conversationID); err != nil {
			utils.WriteError(w, http.StatusBadRequest, "Invalid reply target: "+err.Error())
			return
		}
	}

	var sharedTask *messagingdb.SharedTaskRef
	if req.SharedTaskID != nil {
		sharedTask = &messagingdb.SharedTaskRef{
			TaskID: *req.SharedTaskID,
			Title:  req.SharedTaskTitle,
			Status: req.SharedTaskStatus,
			Number: req.SharedTaskNumber,
		}
	}
	var poll *messagingdb.PollRef
	if req.Poll != nil {
		question := req.Poll.Question
		options := make([]string, 0, len(req.Poll.Options))
		for _, opt := range req.Poll.Options {
			if opt = strings.TrimSpace(opt); opt != "" {
				options = append(options, opt)
			}
		}
		if question == "" {
			utils.WriteError(w, http.StatusBadRequest, "poll question is required")
			return
		}
		if len(options) < 2 {
			utils.WriteError(w, http.StatusBadRequest, "poll requires at least 2 options")
			return
		}
		if len(options) > 10 {
			utils.WriteError(w, http.StatusBadRequest, "poll supports at most 10 options")
			return
		}
		poll = &messagingdb.PollRef{Question: question, MultiChoice: req.Poll.MultiChoice, Options: options}
	}
	msg, err := messagingdb.CreateMessage(r.Context(), conversationID, uid, req.SenderName, req.Body, req.AttachmentKey, req.AttachmentKind, req.AttachmentName, req.AttachmentSizeBytes, req.ScheduledFor, req.ViewOnce, nil, nil, req.ThreadRootMessageID, sharedTask, poll)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "Failed to send message: "+err.Error())
		return
	}
	resolveAttachmentURL(r.Context(), msg, uid)

	if msg.Status == "sent" {
		BroadcastMessageEvent(r.Context(), "message", msg)
	}
	audit.Publish("message.send", "message", msg.MessageID, uid, map[string]any{
		"conversation_id": conversationID,
		"status":          msg.Status,
		"has_attachment":  msg.AttachmentKey != nil,
	})

	utils.WriteJSON(w, http.StatusCreated, msg)
}

// BroadcastMessageEvent notifies all conversation members over SSE; also used by the
// scheduled-message dispatch worker once a held message becomes due.
func BroadcastMessageEvent(ctx context.Context, eventType string, msg *messagingModels.Message) {
	memberIDs, err := messagingdb.ListMemberIDs(ctx, msg.ConversationID)
	if err != nil {
		return
	}
	event := notification.MessageSSEEvent{
		Type:                   eventType,
		ConversationID:         msg.ConversationID,
		MessageID:              msg.MessageID,
		SenderID:               msg.SenderID,
		SenderName:             msg.SenderName,
		Body:                   msg.Body,
		AttachmentURL:          msg.AttachmentURL,
		AttachmentKind:         msg.AttachmentKind,
		AttachmentName:         msg.AttachmentName,
		AttachmentSizeBytes:    msg.AttachmentSizeBytes,
		CreatedAt:              msg.CreatedAt.Format(time.RFC3339),
		ViewOnce:               msg.ViewOnce,
		Viewed:                 msg.Viewed,
		ForwardedFromMessageID: msg.ForwardedFromMessageID,
		ForwardedFromSenderID:  msg.ForwardedFromSenderID,
		ThreadRootMessageID:    msg.ThreadRootMessageID,
		SharedTaskID:           msg.SharedTaskID,
		SharedTaskTitle:        msg.SharedTaskTitle,
		SharedTaskStatus:       msg.SharedTaskStatus,
		SharedTaskNumber:       msg.SharedTaskNumber,
	}
	if msg.Poll != nil {
		opts := make([]notification.PollOptionSSE, len(msg.Poll.Options))
		for i, o := range msg.Poll.Options {
			opts[i] = notification.PollOptionSSE{OptionID: o.OptionID, Text: o.Text, Votes: o.Votes, VotedByMe: o.VotedByMe}
		}
		event.Poll = &notification.PollSSE{PollID: msg.Poll.PollID, Question: msg.Poll.Question, MultiChoice: msg.Poll.MultiChoice, Options: opts}
	}
	if msg.UpdatedAt != nil {
		s := msg.UpdatedAt.Format(time.RFC3339)
		event.UpdatedAt = &s
	}
	if payload, err := json.Marshal(event); err == nil {
		for _, memberID := range memberIDs {
			notification.NotifyBackendMessagingEvent(memberID, payload)
		}
	}

	snippet := msg.Body
	if len(snippet) > 80 {
		snippet = snippet[:80] + "…"
	}
	switch eventType {
	case "message":
		title := msg.SenderName
		if title == "" {
			title = "New message"
		}
		if convo, err := messagingdb.GetConversation(ctx, msg.ConversationID); err == nil && convo.Name != nil && *convo.Name != "" {
			if msg.SenderName != "" {
				title = *convo.Name + " · " + msg.SenderName
			} else {
				title = *convo.Name
			}
		}
		for _, memberID := range memberIDs {
			if memberID == msg.SenderID {
				continue
			}
			notification.NotifyBackend(memberID, "workspace_messages", "new_message", title, snippet, "conversation", msg.ConversationID, msg.SenderID)
		}
	case "message_updated":
		for _, memberID := range memberIDs {
			if memberID == msg.SenderID {
				continue
			}
			notification.NotifyBackend(memberID, "workspace_messages", "message_updated", "Message edited", snippet, "conversation", msg.ConversationID, msg.SenderID)
		}
	}
}

type updateMessageRequest struct {
	Body string `json:"body"`
}

func UpdateMessageHandler(w http.ResponseWriter, r *http.Request) {
	conversationID := chi.URLParam(r, "id")
	messageID := chi.URLParam(r, "message_id")
	uid := userID(r)

	isMember, err := messagingdb.IsMember(r.Context(), conversationID, uid)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "Failed to verify membership: "+err.Error())
		return
	}
	if !isMember {
		utils.WriteError(w, http.StatusForbidden, "Not a member of this conversation")
		return
	}
	inConv, err := messagingdb.MessageInConversation(r.Context(), messageID, conversationID)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "Failed to verify message: "+err.Error())
		return
	}
	if !inConv {
		utils.WriteError(w, http.StatusNotFound, "Message not found in this conversation")
		return
	}

	var req updateMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.WriteError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if req.Body == "" {
		utils.WriteError(w, http.StatusBadRequest, "body is required")
		return
	}
	if utf8.RuneCountInString(req.Body) > maxMessageBodyRunes {
		utils.WriteError(w, http.StatusBadRequest, "body exceeds maximum length")
		return
	}

	msg, err := messagingdb.UpdateMessage(r.Context(), messageID, uid, req.Body)
	if errors.Is(err, messagingdb.ErrMessageNotEditable) {
		utils.WriteError(w, http.StatusForbidden, "Message not found, not yours, or past the 5 minute edit window")
		return
	}
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "Failed to update message: "+err.Error())
		return
	}
	resolveAttachmentURL(r.Context(), msg, uid)
	BroadcastMessageEvent(r.Context(), "message_updated", msg)
	audit.Publish("message.update", "message", messageID, uid, map[string]any{"conversation_id": conversationID})
	utils.WriteJSON(w, http.StatusOK, msg)
}

func DeleteMessageHandler(w http.ResponseWriter, r *http.Request) {
	conversationID := chi.URLParam(r, "id")
	messageID := chi.URLParam(r, "message_id")
	uid := userID(r)

	isMember, err := messagingdb.IsMember(r.Context(), conversationID, uid)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "Failed to verify membership: "+err.Error())
		return
	}
	if !isMember {
		utils.WriteError(w, http.StatusForbidden, "Not a member of this conversation")
		return
	}
	inConv, err := messagingdb.MessageInConversation(r.Context(), messageID, conversationID)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "Failed to verify message: "+err.Error())
		return
	}
	if !inConv {
		utils.WriteError(w, http.StatusNotFound, "Message not found in this conversation")
		return
	}

	scope := r.URL.Query().Get("scope")
	if scope == "" {
		scope = "everyone"
	}

	if scope == "me" {
		if err := messagingdb.DeleteMessageForMe(r.Context(), messageID, uid); err != nil {
			utils.WriteError(w, http.StatusInternalServerError, "Failed to delete message: "+err.Error())
			return
		}

		event := notification.MessageSSEEvent{
			Type:           "message_deleted_for_me",
			ConversationID: conversationID,
			MessageID:      messageID,
			SenderID:       uid,
		}
		if payload, err := json.Marshal(event); err == nil {
			notification.NotifyBackendMessagingEvent(uid, payload)
		}

		audit.Publish("message.delete_for_me", "message", messageID, uid, map[string]any{"conversation_id": conversationID})
		utils.WriteJSON(w, http.StatusOK, map[string]string{"message_id": messageID})
		return
	}

	if err := messagingdb.DeleteMessage(r.Context(), messageID, uid); err != nil {
		if errors.Is(err, messagingdb.ErrMessageNotOwned) {
			utils.WriteError(w, http.StatusForbidden, "Message not found or not yours")
			return
		}
		utils.WriteError(w, http.StatusInternalServerError, "Failed to delete message: "+err.Error())
		return
	}

	memberIDs, err := messagingdb.ListMemberIDs(r.Context(), conversationID)
	if err == nil {
		event := notification.MessageSSEEvent{
			Type:           "message_deleted",
			ConversationID: conversationID,
			MessageID:      messageID,
			SenderID:       uid,
		}
		if payload, err := json.Marshal(event); err == nil {
			for _, memberID := range memberIDs {
				notification.NotifyBackendMessagingEvent(memberID, payload)
				if memberID == uid {
					continue
				}
				notification.NotifyBackend(memberID, "workspace_messages", "message_deleted", "Message deleted", "", "conversation", conversationID, uid)
			}
		}
	}

	audit.Publish("message.delete", "message", messageID, uid, map[string]any{"conversation_id": conversationID})
	utils.WriteJSON(w, http.StatusOK, map[string]string{"message_id": messageID})
}

func ListScheduledMessagesHandler(w http.ResponseWriter, r *http.Request) {
	conversationID := chi.URLParam(r, "id")
	uid := userID(r)

	isMember, err := messagingdb.IsMember(r.Context(), conversationID, uid)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "Failed to verify membership: "+err.Error())
		return
	}
	if !isMember {
		utils.WriteError(w, http.StatusForbidden, "Not a member of this conversation")
		return
	}

	msgs, err := messagingdb.ListScheduledMessages(r.Context(), conversationID, uid)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "Failed to list scheduled messages: "+err.Error())
		return
	}
	utils.WriteJSON(w, http.StatusOK, msgs)
}

// MarkMessageViewedHandler handles POST /messaging/conversations/{id}/messages/{message_id}/view
// Idempotent: only the first call for a given (message_id, user) burns the once-only reveal;
// subsequent calls are no-ops (already recorded).
func MarkMessageViewedHandler(w http.ResponseWriter, r *http.Request) {
	conversationID := chi.URLParam(r, "id")
	messageID := chi.URLParam(r, "message_id")
	uid := userID(r)

	isMember, err := messagingdb.IsMember(r.Context(), conversationID, uid)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "Failed to verify membership: "+err.Error())
		return
	}
	if !isMember {
		utils.WriteError(w, http.StatusForbidden, "Not a member of this conversation")
		return
	}
	inConv, err := messagingdb.MessageInConversation(r.Context(), messageID, conversationID)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "Failed to verify message: "+err.Error())
		return
	}
	if !inConv {
		utils.WriteError(w, http.StatusNotFound, "Message not found in this conversation")
		return
	}

	if _, err := messagingdb.MarkMessageViewed(r.Context(), messageID, uid); err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "Failed to mark message viewed: "+err.Error())
		return
	}

	memberIDs, err := messagingdb.ListMemberIDs(r.Context(), conversationID)
	if err == nil {
		event := notification.MessageViewedSSEEvent{
			Type:           "message_viewed",
			ConversationID: conversationID,
			MessageID:      messageID,
			ViewerID:       uid,
		}
		if payload, err := json.Marshal(event); err == nil {
			for _, memberID := range memberIDs {
				notification.NotifyBackendMessagingEvent(memberID, payload)
				if memberID == uid {
					continue
				}
				notification.NotifyBackend(memberID, "workspace_messages", "message_viewed", "Message viewed", "", "conversation", conversationID, uid)
			}
		}
	}

	audit.Publish("message.view", "message", messageID, uid, map[string]any{"conversation_id": conversationID})
	utils.WriteJSON(w, http.StatusOK, map[string]interface{}{"message_id": messageID, "viewed": true})
}

// ListThreadHandler handles GET /messaging/conversations/{id}/messages/{message_id}/thread
func ListThreadHandler(w http.ResponseWriter, r *http.Request) {
	conversationID := chi.URLParam(r, "id")
	messageID := chi.URLParam(r, "message_id")
	uid := userID(r)

	isMember, err := messagingdb.IsMember(r.Context(), conversationID, uid)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "Failed to verify membership: "+err.Error())
		return
	}
	if !isMember {
		utils.WriteError(w, http.StatusForbidden, "Not a member of this conversation")
		return
	}
	inConv, err := messagingdb.MessageInConversation(r.Context(), messageID, conversationID)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "Failed to verify message: "+err.Error())
		return
	}
	if !inConv {
		utils.WriteError(w, http.StatusNotFound, "Message not found in this conversation")
		return
	}

	messages, err := messagingdb.ListThreadMessages(r.Context(), messageID, uid)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "Failed to list thread: "+err.Error())
		return
	}
	for i := range messages {
		resolveAttachmentURL(r.Context(), &messages[i], uid)
	}
	utils.WriteJSON(w, http.StatusOK, messages)
}

// MarkConversationReadHandler handles POST /messaging/conversations/{id}/read
func MarkConversationReadHandler(w http.ResponseWriter, r *http.Request) {
	conversationID := chi.URLParam(r, "id")
	uid := userID(r)

	isMember, err := messagingdb.IsMember(r.Context(), conversationID, uid)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "Failed to verify membership: "+err.Error())
		return
	}
	if !isMember {
		utils.WriteError(w, http.StatusForbidden, "Not a member of this conversation")
		return
	}

	if err := messagingdb.MarkConversationRead(r.Context(), conversationID, uid); err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "Failed to mark conversation read: "+err.Error())
		return
	}
	utils.WriteJSON(w, http.StatusOK, map[string]string{"conversation_id": conversationID})
}
