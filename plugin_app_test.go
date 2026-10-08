package codexgo_test

import (
	"context"
	"encoding/json"
	"testing"

	codexgo "github.com/zealbase/codex-app-server-go"
)

// Every plugin/marketplace/app RPC must reach its exact upstream wire method. These are
// thin, schema-derived bindings, so the thing worth asserting is the mapping itself
// (a typo in the wire string would otherwise only surface against a real server).
func TestPluginMarketplaceAppRPCsUseUpstreamWireMethods(t *testing.T) {
	cases := []struct {
		wireMethod string
		call       func(context.Context, *codexgo.Client) error
	}{
		{"marketplace/add", func(ctx context.Context, c *codexgo.Client) error {
			_, err := c.MarketplaceAdd(ctx, codexgo.MarketplaceAddParams{Source: "https://example.test/mp.git"})
			return err
		}},
		{"marketplace/remove", func(ctx context.Context, c *codexgo.Client) error {
			_, err := c.MarketplaceRemove(ctx, codexgo.MarketplaceRemoveParams{MarketplaceName: "mp"})
			return err
		}},
		{"marketplace/upgrade", func(ctx context.Context, c *codexgo.Client) error {
			_, err := c.MarketplaceUpgrade(ctx, codexgo.MarketplaceUpgradeParams{})
			return err
		}},
		{"plugin/list", func(ctx context.Context, c *codexgo.Client) error {
			_, err := c.PluginList(ctx, codexgo.PluginListParams{})
			return err
		}},
		{"plugin/installed", func(ctx context.Context, c *codexgo.Client) error {
			_, err := c.PluginInstalled(ctx, codexgo.PluginInstalledParams{})
			return err
		}},
		{"plugin/reconcile", func(ctx context.Context, c *codexgo.Client) error {
			_, err := c.PluginReconcile(ctx, codexgo.PluginReconcileParams{})
			return err
		}},
		{"plugin/read", func(ctx context.Context, c *codexgo.Client) error {
			_, err := c.PluginRead(ctx, codexgo.PluginReadParams{PluginName: "p"})
			return err
		}},
		{"plugin/skill/read", func(ctx context.Context, c *codexgo.Client) error {
			resp, err := c.PluginSkillRead(ctx, codexgo.PluginSkillReadParams{
				RemoteMarketplaceName: "mp", RemotePluginID: "pid", SkillName: "s",
			})
			if err == nil && resp.Contents != "body" {
				t.Errorf("contents = %q, want body", resp.Contents)
			}
			return err
		}},
		{"plugin/share/save", func(ctx context.Context, c *codexgo.Client) error {
			_, err := c.PluginShareSave(ctx, codexgo.PluginShareSaveParams{PluginPath: "/tmp/p"})
			return err
		}},
		{"plugin/share/updateTargets", func(ctx context.Context, c *codexgo.Client) error {
			_, err := c.PluginShareUpdateTargets(ctx, codexgo.PluginShareUpdateTargetsParams{
				Discoverability: codexgo.PluginShareUpdateDiscoverabilityPRIVATE,
				RemotePluginID:  "pid",
				ShareTargets:    []codexgo.PluginShareTarget{},
			})
			return err
		}},
		{"plugin/share/list", func(ctx context.Context, c *codexgo.Client) error {
			_, err := c.PluginShareList(ctx, codexgo.PluginShareListParams{})
			return err
		}},
		{"plugin/share/checkout", func(ctx context.Context, c *codexgo.Client) error {
			_, err := c.PluginShareCheckout(ctx, codexgo.PluginShareCheckoutParams{RemotePluginID: "pid"})
			return err
		}},
		{"plugin/share/delete", func(ctx context.Context, c *codexgo.Client) error {
			_, err := c.PluginShareDelete(ctx, codexgo.PluginShareDeleteParams{RemotePluginID: "pid"})
			return err
		}},
		{"plugin/install", func(ctx context.Context, c *codexgo.Client) error {
			_, err := c.PluginInstall(ctx, codexgo.PluginInstallParams{PluginName: "p"})
			return err
		}},
		{"plugin/uninstall", func(ctx context.Context, c *codexgo.Client) error {
			_, err := c.PluginUninstall(ctx, codexgo.PluginUninstallParams{PluginID: "pid"})
			return err
		}},
		{"app/list", func(ctx context.Context, c *codexgo.Client) error {
			_, err := c.AppsList(ctx, codexgo.AppsListParams{})
			return err
		}},
		{"app/installed", func(ctx context.Context, c *codexgo.Client) error {
			_, err := c.AppsInstalled(ctx, codexgo.AppsInstalledParams{})
			return err
		}},
		{"app/read", func(ctx context.Context, c *codexgo.Client) error {
			_, err := c.AppsRead(ctx, codexgo.AppsReadParams{AppIDs: []string{"a1"}})
			return err
		}},
	}

	for _, tc := range cases {
		t.Run(tc.wireMethod, func(t *testing.T) {
			client, mock := newClientFromMock(t)
			defer client.Close()

			var got json.RawMessage
			reply := map[string]any{}
			if tc.wireMethod == "plugin/skill/read" {
				reply = map[string]any{"contents": "body"}
			}
			mock.Handle(tc.wireMethod, recorder(&got, reply))

			if err := tc.call(testCtx(t), client); err != nil {
				t.Fatalf("%s: %v", tc.wireMethod, err)
			}
			if len(got) == 0 {
				t.Fatalf("%s: handler never received params", tc.wireMethod)
			}
		})
	}
}

// The wire params must actually carry the fields the caller set -- a renamed or dropped
// struct tag would silently send an empty body.
func TestPluginInstallSendsRequiredFields(t *testing.T) {
	client, mock := newClientFromMock(t)
	defer client.Close()

	var got struct {
		PluginName      string `json:"pluginName"`
		MarketplacePath string `json:"marketplacePath"`
	}
	mock.Handle("plugin/install", func(params json.RawMessage) (any, error) {
		if err := json.Unmarshal(params, &got); err != nil {
			t.Errorf("unmarshal params: %v", err)
		}
		return map[string]any{}, nil
	})

	// marketplacePath is optional upstream (anyOf [path, null]), so it is a pointer here.
	mpPath := codexgo.AbsolutePathBuf("/mp/plugins/formatter")
	if _, err := client.PluginInstall(testCtx(t), codexgo.PluginInstallParams{
		PluginName:      "formatter",
		MarketplacePath: &mpPath,
	}); err != nil {
		t.Fatalf("PluginInstall: %v", err)
	}

	if got.PluginName != "formatter" {
		t.Errorf("pluginName = %q, want formatter", got.PluginName)
	}
	if got.MarketplacePath != "/mp/plugins/formatter" {
		t.Errorf("marketplacePath = %q", got.MarketplacePath)
	}
}
