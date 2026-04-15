package atlas

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/helios/helios/internal/atlas/aof"
	"github.com/helios/helios/internal/atlas/protocol"
	"github.com/helios/helios/internal/plugin"
)

func TestAtlasCommandPlugins_KeyGuard(t *testing.T) {
	manager := plugin.NewManager(plugin.ManagerConfig{})
	if err := manager.Load("keyguard", plugin.RuntimeConfig{
		Enabled:  true,
		FailOpen: false,
		Settings: map[string]interface{}{
			"max_value_bytes": 4,
			"denied_prefixes": []string{"blocked:"},
		},
	}); err != nil {
		t.Fatalf("load keyguard plugin: %v", err)
	}

	if err := manager.Start(context.Background()); err != nil {
		t.Fatalf("start plugin manager: %v", err)
	}
	defer manager.Stop(context.Background())

	atlasInstance, err := New(&Config{
		DataDir:          t.TempDir(),
		AOFSyncMode:      aof.SyncEvery,
		SnapshotInterval: 1 * time.Hour,
		MinCommands:      100,
		PluginManager:    manager,
	})
	if err != nil {
		t.Fatalf("create atlas: %v", err)
	}
	defer atlasInstance.Close()

	okResp := atlasInstance.Handle(protocol.NewSetCommand("good:key", []byte("ok"), 0))
	if !okResp.OK {
		t.Fatalf("expected set good:key to succeed, got error: %s", okResp.Error)
	}

	blockedValueResp := atlasInstance.Handle(protocol.NewSetCommand("good:oversized", []byte("abcdef"), 0))
	if blockedValueResp.OK {
		t.Fatal("expected oversized value to be blocked by keyguard plugin")
	}
	if !strings.Contains(blockedValueResp.Error, "max_value_bytes") {
		t.Fatalf("expected max_value_bytes error, got: %s", blockedValueResp.Error)
	}

	if _, exists := atlasInstance.Get("good:oversized"); exists {
		t.Fatal("oversized key should not be written")
	}

	blockedPrefixResp := atlasInstance.Handle(protocol.NewSetCommand("blocked:internal", []byte("ok"), 0))
	if blockedPrefixResp.OK {
		t.Fatal("expected blocked prefix to be rejected")
	}
	if !strings.Contains(blockedPrefixResp.Error, "blocked by policy") {
		t.Fatalf("expected blocked prefix error, got: %s", blockedPrefixResp.Error)
	}

	if _, exists := atlasInstance.Get("blocked:internal"); exists {
		t.Fatal("blocked-prefix key should not be written")
	}
}
