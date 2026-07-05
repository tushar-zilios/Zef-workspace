package messaging

import (
	"encoding/json"
	"net/http"

	messagingdb "workspace/src/internal/db/messaging"
	messagingModels "workspace/src/internal/models/messaging"
	"workspace/src/internal/notification"
	"workspace/src/internal/utils"

	"github.com/go-chi/chi/v5"
)

type reactionRequest struct {
	Emoji string `json:"emoji"`
}

func broadcastReactions(r *http.Request, conversationID, messageID string, reactions []messagingModels.ReactionGroup) {
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
	for _, memberID := range memberIDs {
		notification.GlobalBroker.SendReaction(memberID, event)
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
	broadcastReactions(r, conversationID, messageID, grouped[messageID])
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
	broadcastReactions(r, conversationID, messageID, grouped[messageID])
	utils.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"message_id": messageID,
		"reactions":  grouped[messageID],
	})
}
