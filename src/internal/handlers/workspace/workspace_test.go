package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"workspace/src/internal/db"
	workspacedb "workspace/src/internal/db/workspace"
	models "workspace/src/internal/models/workspace"

	"github.com/go-chi/chi/v5"
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

func TestCreateWorkspaceHandler_BadRequest(t *testing.T) {
	requirePool(t)
	body := []byte(`{"name":"", "owner_id":""}`)
	req := httptest.NewRequest(http.MethodPost, "/workspaces", bytes.NewReader(body))
	w := httptest.NewRecorder()

	CreateWorkspaceHandler(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCreateWorkspaceHandler_InvalidJSON(t *testing.T) {
	requirePool(t)
	req := httptest.NewRequest(http.MethodPost, "/workspaces", bytes.NewReader([]byte("not json")))
	w := httptest.NewRecorder()

	CreateWorkspaceHandler(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCreateWorkspaceHandler_Success(t *testing.T) {
	requirePool(t)
	ownerID := uniqueKey("owner-")
	reqBody, _ := json.Marshal(createRequest{Name: "Handler Workspace", OwnerID: ownerID})
	req := httptest.NewRequest(http.MethodPost, "/workspaces", bytes.NewReader(reqBody))
	w := httptest.NewRecorder()

	CreateWorkspaceHandler(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var ws models.Workspace
	if err := json.NewDecoder(w.Body).Decode(&ws); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if ws.WorkspaceID == "" || ws.Name != "Handler Workspace" || ws.OwnerID != ownerID {
		t.Fatalf("unexpected workspace in response: %+v", ws)
	}
}

func TestGetWorkspaceHandler_NotFound(t *testing.T) {
	requirePool(t)
	r := chi.NewRouter()
	r.Get("/workspaces/{id}", GetWorkspaceHandler)

	req := httptest.NewRequest(http.MethodGet, "/workspaces/00000000-0000-0000-0000-000000000000", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestGetWorkspaceHandler_Found(t *testing.T) {
	requirePool(t)
	ownerID := uniqueKey("owner-")
	ws, err := workspacedb.CreateWorkspace(context.Background(), "Existing Workspace", ownerID)
	if err != nil {
		t.Fatalf("seed CreateWorkspace failed: %v", err)
	}

	r := chi.NewRouter()
	r.Get("/workspaces/{id}", GetWorkspaceHandler)

	req := httptest.NewRequest(http.MethodGet, "/workspaces/"+ws.WorkspaceID, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var got models.Workspace
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if got.WorkspaceID != ws.WorkspaceID {
		t.Fatalf("unexpected workspace: %+v", got)
	}
}

func TestListWorkspacesHandler(t *testing.T) {
	requirePool(t)
	ownerID := uniqueKey("owner-")
	if _, err := workspacedb.CreateWorkspace(context.Background(), "Listed Workspace", ownerID); err != nil {
		t.Fatalf("seed CreateWorkspace failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/workspaces?owner_id="+ownerID, nil)
	w := httptest.NewRecorder()

	ListWorkspacesHandler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var list []models.Workspace
	if err := json.NewDecoder(w.Body).Decode(&list); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if len(list) != 1 || list[0].OwnerID != ownerID {
		t.Fatalf("expected exactly the seeded workspace, got %+v", list)
	}
}
