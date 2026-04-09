package session

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	relayagent "github.com/normahq/norma/internal/apps/relay/agent"
	relaystate "github.com/normahq/norma/internal/apps/relay/state"
	"github.com/rs/zerolog"
)

func TestStopAll_CleansWorkspaceWhenRootContextCanceled(t *testing.T) {
	ctx := context.Background()
	workingDir := t.TempDir()
	initGitRepo(t, ctx, workingDir)

	writeFile(t, filepath.Join(workingDir, "seed.txt"), "seed\n")
	runGit(t, ctx, workingDir, "add", "seed.txt")
	runGit(t, ctx, workingDir, "commit", "-m", "chore: seed")

	workspaceDir := filepath.Join(t.TempDir(), "relay-workspace")
	runGit(t, ctx, workingDir, "worktree", "add", "-b", "norma/relay/relay-1-1", workspaceDir, "HEAD")

	rootCtx, rootCancel := context.WithCancel(context.Background())
	rootCancel()

	m := &Manager{
		workspaces:       relayagent.NewWorkspaceManager(workingDir, "master"),
		workspaceEnabled: true,
		logger:           zerolog.Nop(),
		rootCtx:          rootCtx,
		sessions: map[string]*TopicSession{
			"relay-1-1": {
				sessionID:    "relay-1-1",
				workspaceDir: workspaceDir,
			},
		},
	}

	m.StopAll()

	if _, err := os.Stat(workspaceDir); !os.IsNotExist(err) {
		t.Fatalf("workspace still exists after StopAll; stat err = %v", err)
	}
}

func TestStopSession_UsesNonCanceledCleanupContext(t *testing.T) {
	rootCtx, rootCancel := context.WithCancel(context.Background())
	rootCancel()

	store := &fakeSessionStore{}
	m := &Manager{
		logger:       zerolog.Nop(),
		rootCtx:      rootCtx,
		sessionStore: store,
		sessions: map[string]*TopicSession{
			"relay-10-42": {
				sessionID: "relay-10-42",
			},
		},
	}

	m.StopSession(10, 42)

	if store.deletedSessionID != "relay-10-42" {
		t.Fatalf("DeleteBySessionID called with %q, want %q", store.deletedSessionID, "relay-10-42")
	}
	if store.deleteCtxErr != nil {
		t.Fatalf("DeleteBySessionID ctx was canceled: %v", store.deleteCtxErr)
	}
}

func TestExtraMCPServerIDs_ForAllSessions(t *testing.T) {
	m := &Manager{relayMCPServerIDs: []string{"srv.one", "srv.two"}}

	got := m.extraMCPServerIDs()
	if strings.Join(got, ",") != "srv.one,srv.two" {
		t.Fatalf("extraMCPServerIDs() = %#v, want [srv.one srv.two]", got)
	}

	// Ensure returned slice is detached.
	got[0] = "mutated"
	if m.relayMCPServerIDs[0] != "srv.one" {
		t.Fatalf("relayMCPServerIDs mutated through returned slice: %#v", m.relayMCPServerIDs)
	}
}

func TestMergeUniqueStringIDs(t *testing.T) {
	base := []string{"norma.config", "norma.state", "shared"}
	extra := []string{" custom.one ", "shared", "", "custom.two"}

	got := mergeUniqueStringIDs(base, extra)
	want := []string{"norma.config", "norma.state", "shared", "custom.one", "custom.two"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("mergeUniqueStringIDs(%#v, %#v) = %#v, want %#v", base, extra, got, want)
	}
}

type fakeSessionStore struct {
	deletedSessionID string
	deleteCtxErr     error
}

func (f *fakeSessionStore) Upsert(context.Context, relaystate.SessionRecord) error {
	return nil
}

func (f *fakeSessionStore) GetByChatTopic(context.Context, int64, int) (relaystate.SessionRecord, bool, error) {
	return relaystate.SessionRecord{}, false, nil
}

func (f *fakeSessionStore) DeleteBySessionID(ctx context.Context, sessionID string) error {
	f.deletedSessionID = sessionID
	f.deleteCtxErr = ctx.Err()
	return nil
}

func (f *fakeSessionStore) List(context.Context) ([]relaystate.SessionRecord, error) {
	return nil, nil
}

func initGitRepo(t *testing.T, ctx context.Context, workingDir string) {
	t.Helper()
	runGit(t, ctx, workingDir, "init")
	runGit(t, ctx, workingDir, "config", "user.name", "Norma Test")
	runGit(t, ctx, workingDir, "config", "user.email", "norma-test@example.com")
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write file %s: %v", path, err)
	}
}

func runGit(t *testing.T, ctx context.Context, dir string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}
