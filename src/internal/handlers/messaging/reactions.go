package messaging

import (
	"encoding/json"
	"net/http"

	"workspace/src/internal/clients/audit"
	messagingdb "workspace/src/internal/db/messaging"
	messagingModels "workspace/src/internal/models/messaging"
	"workspace/src/internal/notification"
	"workspace/src/internal/utils"

	"github.com/go-chi/chi/v5"
)

type reactionRequest struct {
	Emoji string `json:"emoji"`
}

func broadcastReactions(r *http.Request, conversationID, messageID, actorID string, reactions []messagingModels.ReactionGroup) {
	sseReactions := make([]notification.ReactionGroupSSE, len(reactions))
	for i, rg := range reactions {
		sseReactions[i] = notification.ReactionGroupSSE{
			Emoji:   rg.Emoji,
			Count:   rg.Count,
			UserIDs: rg.UserIDs,
			Reacted: rg.Reacted,
		}
	}
	memberIDs, err := messagingdb.ListMemberIDs(r.Context(), conversationID)
	if err != nil {
		return
	}
	event := notification.ReactionSSEEvent{
		Type:           "reaction",
		ConversationID: conversationID,
		MessageID:      messageID,
		Reactions:      sseReactions,
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return
	}
	for _, memberID := range memberIDs {
		notification.NotifyBackendMessagingEvent(memberID, payload)
		if memberID == actorID {
			continue
		}
		notification.NotifyBackend(memberID, "workspace_messages", "reaction", "New reaction", "Someone reacted to a message", "conversation", conversationID, actorID)
	}
}

// AddReactionHandler handles POST /messaging/conversations/{id}/messages/{message_id}/reactions
func AddReactionHandler(w http.ResponseWriter, r *http.Request) {
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

	var req reactionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Emoji == "" {
		utils.WriteError(w, http.StatusBadRequest, "emoji is required")
		return
	}

	if err := messagingdb.AddReaction(r.Context(), messageID, uid, req.Emoji); err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "Failed to add reaction: "+err.Error())
		return
	}

	grouped, err := messagingdb.ListReactionsForMessages(r.Context(), []string{messageID}, uid)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "Failed to load reactions: "+err.Error())
		return
	}
	broadcastReactions(r, conversationID, messageID, uid, grouped[messageID])
	audit.Publish("message.reaction.add", "message", messageID, uid, map[string]any{"conversation_id": conversationID, "emoji": req.Emoji})
	utils.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"message_id": messageID,
		"reactions":  grouped[messageID],
	})
}

// RemoveReactionHandler handles DELETE /messaging/conversations/{id}/messages/{message_id}/reactions
func RemoveReactionHandler(w http.ResponseWriter, r *http.Request) {
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

	var req reactionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Emoji == "" {
		utils.WriteError(w, http.StatusBadRequest, "emoji is required")
		return
	}

	if err := messagingdb.RemoveReaction(r.Context(), messageID, uid, req.Emoji); err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "Failed to remove reaction: "+err.Error())
		return
	}

	grouped, err := messagingdb.ListReactionsForMessages(r.Context(), []string{messageID}, uid)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "Failed to load reactions: "+err.Error())
		return
	}
	broadcastReactions(r, conversationID, messageID, uid, grouped[messageID])
	audit.Publish("message.reaction.remove", "message", messageID, uid, map[string]any{"conversation_id": conversationID, "emoji": req.Emoji})
	utils.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"message_id": messageID,
		"reactions":  grouped[messageID],
	})
}
