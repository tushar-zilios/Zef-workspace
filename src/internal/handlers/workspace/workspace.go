package workspace

import (
	"encoding/json"
	"errors"
	"net/http"

	workspacedb "workspace/src/internal/db/workspace"
	"workspace/src/internal/utils"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
)

type createRequest struct {
	Name    string `json:"name"`
	OwnerID string `json:"owner_id"`
}

func ListWorkspacesHandler(w http.ResponseWriter, r *http.Request) {
	ownerID := r.URL.Query().Get("owner_id")
	workspaces, err := workspacedb.ListWorkspaces(r.Context(), ownerID)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "Failed to list workspaces: "+err.Error())
		return
	}
	utils.WriteJSON(w, http.StatusOK, workspaces)
}

func GetWorkspaceHandler(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	ws, err := workspacedb.GetWorkspace(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			utils.WriteError(w, http.StatusNotFound, "Workspace not found")
			return
		}
		utils.WriteError(w, http.StatusInternalServerError, "Failed to get workspace: "+err.Error())
		return
	}
	utils.WriteJSON(w, http.StatusOK, ws)
}

func CreateWorkspaceHandler(w http.ResponseWriter, r *http.Request) {
	var req createRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.WriteError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if req.Name == "" || req.OwnerID == "" {
		utils.WriteError(w, http.StatusBadRequest, "name and owner_id are required")
		return
	}
	ws, err := workspacedb.CreateWorkspace(r.Context(), req.Name, req.OwnerID)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "Failed to create workspace: "+err.Error())
		return
	}
	utils.WriteJSON(w, http.StatusCreated, ws)
}
