package messaging

import (
	"encoding/json"
	"net/http"
	"unicode/utf8"

	"workspace/src/internal/clients/audit"
	messagingdb "workspace/src/internal/db/messaging"
	"workspace/src/internal/utils"

	"github.com/go-chi/chi/v5"
)

type forwardMessageRequest struct {
	TargetConversationIDs []string `json:"target_conversation_ids"`
	Caption               string   `json:"caption,omitempty"`
	ForwarderName         string   `json:"forwarder_name"`
}

// ForwardMessageHandler handles POST /messaging/conversations/{id}/messages/{message_id}/forward
// {id} is the SOURCE conversation (used only for the membership + soft-delete check on the source
// message); the handler additionally verifies the requester is a member of EVERY target conversation
// before forwarding to any of them (all-or-nothing membership check, then all-or-nothing DB write).
func ForwardMessageHandler(w http.ResponseWriter, r *http.Request) {
	sourceConversationID := chi.URLParam(r, "id")
	messageID := chi.URLParam(r, "message_id")
	uid := userID(r)

	isMember, err := messagingdb.IsMember(r.Context(), sourceConversationID, uid)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "Failed to verify membership: "+err.Error())
		return
	}
	if !isMember {
		utils.WriteError(w, http.StatusForbidden, "Not a member of this conversation")
		return
	}
	inConv, err := messagingdb.MessageInConversation(r.Context(), messageID, sourceConversationID)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "Failed to verify message: "+err.Error())
		return
	}
	if !inConv {
		utils.WriteError(w, http.StatusNotFound, "Message not found in this conversation")
		return
	}

	var req forwardMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.WriteError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if len(req.TargetConversationIDs) == 0 {
		utils.WriteError(w, http.StatusBadRequest, "target_conversation_ids is required")
		return
	}
	if req.ForwarderName == "" {
		utils.WriteError(w, http.StatusBadRequest, "forwarder_name is required")
		return
	}
	if utf8.RuneCountInString(req.Caption) > maxMessageBodyRunes {
		utils.WriteError(w, http.StatusBadRequest, "caption exceeds maximum length")
		return
	}

	for _, targetID := range req.TargetConversationIDs {
		targetIsMember, err := messagingdb.IsMember(r.Context(), targetID, uid)
		if err != nil {
			utils.WriteError(w, http.StatusInternalServerError, "Failed to verify target membership: "+err.Error())
			return
		}
		if !targetIsMember {
			utils.WriteError(w, http.StatusForbidden, "Not a member of one or more target conversations")
			return
		}
	}

	copies, err := messagingdb.ForwardMessage(r.Context(), messageID, uid, req.ForwarderName, req.TargetConversationIDs, req.Caption)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "Failed to forward message: "+err.Error())
		return
	}

	for i := range copies {
		resolveAttachmentURL(r.Context(), &copies[i], uid)
		BroadcastMessageEvent(r.Context(), "message", &copies[i])
		audit.Publish("message.forward", "message", copies[i].MessageID, uid, map[string]any{
			"source_message_id":      messageID,
			"target_conversation_id": copies[i].ConversationID,
		})
	}

	utils.WriteJSON(w, http.StatusCreated, copies)
}
