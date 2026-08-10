package messaging

import (
	"encoding/json"
	"errors"
	"net/http"

	"workspace/src/internal/clients/audit"
	messagingdb "workspace/src/internal/db/messaging"
	messagingModels "workspace/src/internal/models/messaging"
	"workspace/src/internal/notification"
	"workspace/src/internal/utils"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
)

type pollVoteRequest struct {
	OptionID string `json:"option_id"`
}

// broadcastPollVote notifies all conversation members of the updated vote tally. Like
// broadcastReactions's "reacted" flag, VotedByMe here is computed relative to the acting user
// and broadcast as-is to every member — each client already knows its own vote state from its
// last fetch/vote response and primarily cares about the refreshed Votes counts.
func broadcastPollVote(r *http.Request, conversationID, messageID, pollID, actorID string, options []messagingModels.PollOption) {
	memberIDs, err := messagingdb.ListMemberIDs(r.Context(), conversationID)
	if err != nil {
		return
	}
	sseOptions := make([]notification.PollOptionSSE, len(options))
	for i, o := range options {
		sseOptions[i] = notification.PollOptionSSE{
			OptionID:  o.OptionID,
			Text:      o.Text,
			Votes:     o.Votes,
			VotedByMe: o.VotedByMe,
			IsCorrect: o.IsCorrect,
		}
	}
	payload, err := json.Marshal(notification.PollVoteSSEEvent{
		Type:           "poll_vote",
		ConversationID: conversationID,
		MessageID:      messageID,
		PollID:         pollID,
		Options:        sseOptions,
	})
	if err != nil {
		return
	}
	for _, memberID := range memberIDs {
		notification.NotifyBackendMessagingEvent(memberID, payload)
	}
}

// VotePollHandler handles POST /messaging/conversations/{id}/messages/{message_id}/poll/votes
func VotePollHandler(w http.ResponseWriter, r *http.Request) {
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

	var req pollVoteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.OptionID == "" {
		utils.WriteError(w, http.StatusBadRequest, "option_id is required")
		return
	}

	pollID, multiChoice, err := messagingdb.GetPollForMessage(r.Context(), messageID)
	if errors.Is(err, pgx.ErrNoRows) {
		utils.WriteError(w, http.StatusNotFound, "Message has no poll")
		return
	}
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "Failed to load poll: "+err.Error())
		return
	}
	belongs, err := messagingdb.OptionBelongsToPoll(r.Context(), pollID, req.OptionID)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "Failed to verify poll option: "+err.Error())
		return
	}
	if !belongs {
		utils.WriteError(w, http.StatusBadRequest, "option_id does not belong to this poll")
		return
	}
	_ = multiChoice

	if err := messagingdb.VoteOnPoll(r.Context(), pollID, req.OptionID, uid); err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "Failed to record vote: "+err.Error())
		return
	}

	options, err := messagingdb.ListPollOptionsTally(r.Context(), pollID, uid)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "Failed to load poll tally: "+err.Error())
		return
	}
	broadcastPollVote(r, conversationID, messageID, pollID, uid, options)
	audit.Publish("message.poll.vote", "message", messageID, uid, map[string]any{"conversation_id": conversationID, "poll_id": pollID, "option_id": req.OptionID})
	utils.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"poll_id": pollID,
		"options": options,
	})
}

// RemovePollVoteHandler handles DELETE /messaging/conversations/{id}/messages/{message_id}/poll/votes
func RemovePollVoteHandler(w http.ResponseWriter, r *http.Request) {
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

	var req pollVoteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.OptionID == "" {
		utils.WriteError(w, http.StatusBadRequest, "option_id is required")
		return
	}

	pollID, _, err := messagingdb.GetPollForMessage(r.Context(), messageID)
	if errors.Is(err, pgx.ErrNoRows) {
		utils.WriteError(w, http.StatusNotFound, "Message has no poll")
		return
	}
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "Failed to load poll: "+err.Error())
		return
	}

	if err := messagingdb.RemovePollVote(r.Context(), req.OptionID, uid); err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "Failed to remove vote: "+err.Error())
		return
	}

	options, err := messagingdb.ListPollOptionsTally(r.Context(), pollID, uid)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "Failed to load poll tally: "+err.Error())
		return
	}
	broadcastPollVote(r, conversationID, messageID, pollID, uid, options)
	audit.Publish("message.poll.unvote", "message", messageID, uid, map[string]any{"conversation_id": conversationID, "poll_id": pollID, "option_id": req.OptionID})
	utils.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"poll_id": pollID,
		"options": options,
	})
}
