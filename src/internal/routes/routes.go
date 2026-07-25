package routes

import (
	"net/http"

	messagingHandlers "workspace/src/internal/handlers/messaging"
	workspaceHandlers "workspace/src/internal/handlers/workspace"
	"workspace/src/internal/utils"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

func NewRouter() http.Handler {
	r := chi.NewRouter()

	r.Use(utils.CORSMiddleware)
	r.Use(conditionalLogger)
	r.Use(handlerLogger)
	r.Use(middleware.Recoverer)
	r.Use(requestTimeout)

	r.Get("/health", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })

	r.Route("/workspaces", func(r chi.Router) {
		r.Use(JWTMiddleware)
		r.Get("/", workspaceHandlers.ListWorkspacesHandler)
		r.Post("/", workspaceHandlers.CreateWorkspaceHandler)
		r.Get("/{id}", workspaceHandlers.GetWorkspaceHandler)
	})

	r.Route("/messaging", func(r chi.Router) {
		r.Use(JWTMiddleware)
		r.Get("/conversations", messagingHandlers.ListConversationsHandler)
		r.Post("/conversations", messagingHandlers.CreateConversationHandler)
		r.Get("/conversations/{id}/messages", messagingHandlers.ListMessagesHandler)
		r.Get("/conversations/{id}/scheduled", messagingHandlers.ListScheduledMessagesHandler)
		r.Post("/conversations/{id}/read", messagingHandlers.MarkConversationReadHandler)
		r.Get("/conversations/{id}/messages/{message_id}/thread", messagingHandlers.ListThreadHandler)

		// Mutating/write-heavy actions get per-user rate limiting to blunt flood/spam abuse.
		r.Group(func(r chi.Router) {
			r.Use(RateLimitMiddleware)
			r.Post("/conversations/{id}/messages", messagingHandlers.SendMessageHandler)
			r.Put("/conversations/{id}/messages/{message_id}", messagingHandlers.UpdateMessageHandler)
			r.Delete("/conversations/{id}/messages/{message_id}", messagingHandlers.DeleteMessageHandler)
			r.Post("/conversations/{id}/attachments", messagingHandlers.UploadMessageAttachmentHandler)
			r.Post("/conversations/{id}/messages/{message_id}/reactions", messagingHandlers.AddReactionHandler)
			r.Delete("/conversations/{id}/messages/{message_id}/reactions", messagingHandlers.RemoveReactionHandler)
			r.Post("/conversations/{id}/messages/{message_id}/view", messagingHandlers.MarkMessageViewedHandler)
			r.Post("/conversations/{id}/messages/{message_id}/forward", messagingHandlers.ForwardMessageHandler)
		})
	})

	return r
}
