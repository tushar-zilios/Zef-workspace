package workspace

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"workspace/src/internal/db"

	"github.com/google/uuid"
	"github.com/joho/godotenv"
)

func TestMain(m *testing.M) {
	_ = godotenv.Load("../../../../.env")
	if os.Getenv("RUN_DB_TESTS") != "1" {
		os.Exit(0)
	}
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		os.Exit(0)
	}
	if _, err := db.InitDB(context.Background(), dbURL, "development"); err != nil {
		os.Exit(0)
	}
	code := m.Run()
	db.CloseDB()
	os.Exit(code)
}

func requirePool(t *testing.T) {
	t.Helper()
	if db.GetPoolOrNil() == nil {
		t.Skip("DATABASE_URL not set; skipping DB test")
	}
}

func uniqueKey(prefix string) string {
	return fmt.Sprintf("%s%d", prefix, time.Now().UnixNano())
}

func TestCreateAndGetWorkspace(t *testing.T) {
	requirePool(t)
	ownerID := uniqueKey("owner-")
	ws, err := CreateWorkspace(context.Background(), "Test Workspace", ownerID)
	if err != nil {
		t.Fatalf("CreateWorkspace failed: %v", err)
	}
	if ws.WorkspaceID == "" {
		t.Fatal("expected non-empty workspace ID")
	}

	got, err := GetWorkspace(context.Background(), ws.WorkspaceID)
	if err != nil {
		t.Fatalf("GetWorkspace failed: %v", err)
	}
	if got.Name != "Test Workspace" || got.OwnerID != ownerID {
		t.Fatalf("unexpected workspace: %+v", got)
	}
}

func TestGetWorkspace_NotFound(t *testing.T) {
	requirePool(t)
	_, err := GetWorkspace(context.Background(), "00000000-0000-0000-0000-000000000000")
	if err == nil {
		t.Fatal("expected error for nonexistent workspace")
	}
}

func TestListWorkspaces_ByOwner(t *testing.T) {
	requirePool(t)
	ownerID := uniqueKey("owner-")
	ws, err := CreateWorkspace(context.Background(), "Listed Workspace", ownerID)
	if err != nil {
		t.Fatalf("CreateWorkspace failed: %v", err)
	}

	list, err := ListWorkspaces(context.Background(), ownerID)
	if err != nil {
		t.Fatalf("ListWorkspaces failed: %v", err)
	}
	if len(list) != 1 || list[0].WorkspaceID != ws.WorkspaceID {
		t.Fatalf("expected exactly the created workspace, got %+v", list)
	}
}

func TestEnsureWorkspace_UpsertsAndIsIdempotent(t *testing.T) {
	requirePool(t)
	workspaceID := uuid.New().String()
	ownerID := uniqueKey("owner-")

	if err := EnsureWorkspace(context.Background(), workspaceID, ownerID); err != nil {
		t.Fatalf("EnsureWorkspace (first) failed: %v", err)
	}
	// Calling again with a different owner must be a no-op (ON CONFLICT DO NOTHING).
	if err := EnsureWorkspace(context.Background(), workspaceID, uniqueKey("owner-other-")); err != nil {
		t.Fatalf("EnsureWorkspace (second) failed: %v", err)
	}

	got, err := GetWorkspace(context.Background(), workspaceID)
	if err != nil {
		t.Fatalf("GetWorkspace failed: %v", err)
	}
	if got.OwnerID != ownerID {
		t.Fatalf("expected first-write owner to persist, got %+v", got)
	}
}
