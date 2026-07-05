package workspace

import (
	"context"
	"errors"

	"workspace/src/internal/db"
	models "workspace/src/internal/models/workspace"
)

var ErrDBNotConfigured = errors.New("database not configured")

func ListWorkspaces(ctx context.Context, ownerID string) ([]models.Workspace, error) {
	pool := db.GetPoolOrNil()
	if pool == nil {
		return nil, ErrDBNotConfigured
	}
	rows, err := pool.Query(ctx, `
		SELECT workspace_id, name, owner_id, created_at, updated_at
		FROM public.workspaces
		WHERE owner_id = $1
		ORDER BY created_at DESC
	`, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []models.Workspace
	for rows.Next() {
		var w models.Workspace
		if err := rows.Scan(&w.WorkspaceID, &w.Name, &w.OwnerID, &w.CreatedAt, &w.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

func GetWorkspace(ctx context.Context, id string) (*models.Workspace, error) {
	pool := db.GetPoolOrNil()
	if pool == nil {
		return nil, ErrDBNotConfigured
	}
	var w models.Workspace
	err := pool.QueryRow(ctx, `
		SELECT workspace_id, name, owner_id, created_at, updated_at
		FROM public.workspaces
		WHERE workspace_id = $1
	`, id).Scan(&w.WorkspaceID, &w.Name, &w.OwnerID, &w.CreatedAt, &w.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &w, nil
}

// EnsureWorkspace upserts a workspace row using an externally supplied ID (e.g. the
// team/workspace ID already known to the frontend from Zef-backend), so callers that
// only know an opaque workspace ID can reference it here without a separate provisioning step.
func EnsureWorkspace(ctx context.Context, workspaceID, ownerID string) error {
	pool := db.GetPoolOrNil()
	if pool == nil {
		return ErrDBNotConfigured
	}
	_, err := pool.Exec(ctx, `
		INSERT INTO public.workspaces (workspace_id, name, owner_id)
		VALUES ($1, 'Workspace', $2)
		ON CONFLICT (workspace_id) DO NOTHING
	`, workspaceID, ownerID)
	return err
}

func CreateWorkspace(ctx context.Context, name, ownerID string) (*models.Workspace, error) {
	pool := db.GetPoolOrNil()
	if pool == nil {
		return nil, ErrDBNotConfigured
	}
	var w models.Workspace
	err := pool.QueryRow(ctx, `
		INSERT INTO public.workspaces (name, owner_id)
		VALUES ($1, $2)
		RETURNING workspace_id, name, owner_id, created_at, updated_at
	`, name, ownerID).Scan(&w.WorkspaceID, &w.Name, &w.OwnerID, &w.CreatedAt, &w.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &w, nil
}
