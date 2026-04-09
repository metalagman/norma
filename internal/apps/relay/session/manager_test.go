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
	runGit(t, ctx, workingDir, "worktree", "add", "-b", "norma/relay/tg-1-1", workspaceDir, "HEAD")

	rootCtx, rootCancel := context.WithCancel(context.Background())
	rootCancel()

	m := &Manager{
		workspaces:       relayagent.NewWorkspaceManager(workingDir, "master"),
		workspaceEnabled: true,
		logger:           zerolog.Nop(),
		rootCtx:          rootCtx,
		sessions: map[string]*TopicSession{
			"tg-1-1": {
				sessionID:    "tg-1-1",
				locator:      NewTelegramSessionLocator(1, 1),
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
			"tg-10-42": {
				sessionID: "tg-10-42",
				locator:   NewTelegramSessionLocator(10, 42),
			},
		},
	}

	m.StopTelegramSession(10, 42)

	if store.deletedSessionID != "tg-10-42" {
		t.Fatalf("DeleteBySessionID called with %q, want %q", store.deletedSessionID, "tg-10-42")
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
	base := []string{"relay", "shared"}
	extra := []string{" custom.one ", "shared", "", "custom.two"}

	got := mergeUniqueStringIDs(base, extra)
	want := []string{"relay", "shared", "custom.one", "custom.two"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("mergeUniqueStringIDs(%#v, %#v) = %#v, want %#v", base, extra, got, want)
	}
}

func TestGetSessionInfo_ReturnsPersistedSession(t *testing.T) {
	store := &fakeSessionStore{
		recordsByID: map[string]relaystate.SessionRecord{
			"tg-10-42": {
				SessionID:    "tg-10-42",
				ChannelType:  relaystate.ChannelTypeTelegram,
				AddressKey:   "10:42",
				AddressJSON:  `{"chat_id":10,"topic_id":42}`,
				AgentName:    "opencode",
				WorkspaceDir: "/tmp/workspace",
				BranchName:   "norma/relay/tg-10-42",
				Status:       relaystate.SessionStatusActive,
			},
		},
	}

	m := &Manager{
		logger:       zerolog.Nop(),
		sessionStore: store,
		sessions:     map[string]*TopicSession{},
	}

	info, err := m.GetSessionInfo(context.Background(), "tg-10-42")
	if err != nil {
		t.Fatalf("GetSessionInfo() error = %v", err)
	}
	if info.SessionID != "tg-10-42" || info.ChatID != 10 || info.TopicID != 42 {
		t.Fatalf("GetSessionInfo() = %+v, want session/chat/topic tg-10-42/10/42", info)
	}
	if info.Status != sessionStatusPersisted {
		t.Fatalf("GetSessionInfo() status = %q, want %q", info.Status, sessionStatusPersisted)
	}
}

func TestListSessionInfos_MergesActiveAndPersisted(t *testing.T) {
	store := &fakeSessionStore{
		listRecords: []relaystate.SessionRecord{
			{
				SessionID:    "tg-1-1",
				ChannelType:  relaystate.ChannelTypeTelegram,
				AddressKey:   "1:1",
				AddressJSON:  `{"chat_id":1,"topic_id":1}`,
				AgentName:    "persisted",
				WorkspaceDir: "/tmp/persisted",
				BranchName:   "norma/relay/tg-1-1",
				Status:       relaystate.SessionStatusActive,
			},
			{
				SessionID:    "tg-2-2",
				ChannelType:  relaystate.ChannelTypeTelegram,
				AddressKey:   "2:2",
				AddressJSON:  `{"chat_id":2,"topic_id":2}`,
				AgentName:    "inactive",
				WorkspaceDir: "/tmp/inactive",
				BranchName:   "norma/relay/tg-2-2",
				Status:       relaystate.SessionStatusActive,
			},
		},
	}

	m := &Manager{
		logger:       zerolog.Nop(),
		sessionStore: store,
		sessions: map[string]*TopicSession{
			"tg-1-1": {
				sessionID: "tg-1-1",
				locator:   NewTelegramSessionLocator(1, 1),
				agentName: "active",
				chatID:    1,
				topicID:   1,
			},
		},
	}

	infos, err := m.ListSessionInfos(context.Background())
	if err != nil {
		t.Fatalf("ListSessionInfos() error = %v", err)
	}
	if len(infos) != 2 {
		t.Fatalf("ListSessionInfos() len = %d, want 2", len(infos))
	}

	byID := make(map[string]TopicSessionInfo, len(infos))
	for _, info := range infos {
		byID[info.SessionID] = info
	}
	if byID["tg-1-1"].AgentName != "active" || byID["tg-1-1"].Status != relaystate.SessionStatusActive {
		t.Fatalf("active session merge = %+v, want active override", byID["tg-1-1"])
	}
	if byID["tg-2-2"].Status != sessionStatusPersisted {
		t.Fatalf("persisted session status = %q, want %q", byID["tg-2-2"].Status, sessionStatusPersisted)
	}
}

func TestStopSessionByID_PersistedSessionCleansWorkspace(t *testing.T) {
	ctx := context.Background()
	workingDir := t.TempDir()
	initGitRepo(t, ctx, workingDir)

	writeFile(t, filepath.Join(workingDir, "seed.txt"), "seed\n")
	runGit(t, ctx, workingDir, "add", "seed.txt")
	runGit(t, ctx, workingDir, "commit", "-m", "chore: seed")

	workspaceDir := filepath.Join(t.TempDir(), "relay-workspace")
	runGit(t, ctx, workingDir, "worktree", "add", "-b", "norma/relay/tg-9-9", workspaceDir, "HEAD")

	store := &fakeSessionStore{
		recordsByID: map[string]relaystate.SessionRecord{
			"tg-9-9": {
				SessionID:    "tg-9-9",
				ChannelType:  relaystate.ChannelTypeTelegram,
				AddressKey:   "9:9",
				AddressJSON:  `{"chat_id":9,"topic_id":9}`,
				AgentName:    "opencode",
				WorkspaceDir: workspaceDir,
				BranchName:   "norma/relay/tg-9-9",
				Status:       relaystate.SessionStatusActive,
			},
		},
	}

	m := &Manager{
		workspaces:       relayagent.NewWorkspaceManager(workingDir, "master"),
		workspaceEnabled: true,
		logger:           zerolog.Nop(),
		sessionStore:     store,
		sessions:         map[string]*TopicSession{},
	}

	if err := m.StopSessionByID(ctx, "tg-9-9"); err != nil {
		t.Fatalf("StopSessionByID() error = %v", err)
	}
	if store.deletedSessionID != "tg-9-9" {
		t.Fatalf("DeleteBySessionID called with %q, want %q", store.deletedSessionID, "tg-9-9")
	}
	if _, err := os.Stat(workspaceDir); !os.IsNotExist(err) {
		t.Fatalf("workspace still exists after StopSessionByID; stat err = %v", err)
	}
}

type fakeSessionStore struct {
	deletedSessionID string
	deleteCtxErr     error
	recordsByID      map[string]relaystate.SessionRecord
	listRecords      []relaystate.SessionRecord
}

func (f *fakeSessionStore) Upsert(context.Context, relaystate.SessionRecord) error {
	return nil
}

func (f *fakeSessionStore) GetByAddress(context.Context, string, string) (relaystate.SessionRecord, bool, error) {
	return relaystate.SessionRecord{}, false, nil
}

func (f *fakeSessionStore) GetBySessionID(_ context.Context, sessionID string) (relaystate.SessionRecord, bool, error) {
	if f.recordsByID == nil {
		return relaystate.SessionRecord{}, false, nil
	}
	record, ok := f.recordsByID[sessionID]
	return record, ok, nil
}

func (f *fakeSessionStore) DeleteBySessionID(ctx context.Context, sessionID string) error {
	f.deletedSessionID = sessionID
	f.deleteCtxErr = ctx.Err()
	return nil
}

func (f *fakeSessionStore) List(context.Context) ([]relaystate.SessionRecord, error) {
	if f.listRecords == nil {
		return nil, nil
	}
	return append([]relaystate.SessionRecord(nil), f.listRecords...), nil
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

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file %s: %v", path, err)
	}
	return string(data)
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
