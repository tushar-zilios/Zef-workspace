package workspace

import "time"

type Workspace struct {
	WorkspaceID string    `json:"workspace_id"`
	Name        string    `json:"name"`
	OwnerID     string    `json:"owner_id"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}
