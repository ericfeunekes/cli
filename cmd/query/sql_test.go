package query

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"

	sdkconfig "github.com/databricks/databricks-sdk-go/config"

	"github.com/databricks/cli/libs/cmdctx"
	"github.com/databricks/cli/libs/env"
)

func TestResolveAllowDestructiveFlagPrecedence(t *testing.T) {
	opts := &sqlOptions{}
	cmd := &cobra.Command{}
	cmd.Flags().BoolVar(&opts.allowDestructive, "allow-destructive", false, "")

	ctx := cmdctx.SetConfigUsed(context.Background(), &sdkconfig.Config{})
	cmd.SetContext(ctx)

	if err := cmd.Flags().Set("allow-destructive", "true"); err != nil {
		t.Fatalf("failed to set flag: %v", err)
	}

	allowed, err := opts.resolveAllowDestructive(cmd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !allowed {
		t.Fatalf("expected flag to allow destructive statements")
	}
}

func TestResolveAllowDestructiveEnv(t *testing.T) {
	opts := &sqlOptions{}
	cmd := &cobra.Command{}
	cmd.Flags().BoolVar(&opts.allowDestructive, "allow-destructive", false, "")

	ctx := env.Set(context.Background(), "DATABRICKS_CLI_ALLOW_DESTRUCTIVE_SQL", "true")
	ctx = cmdctx.SetConfigUsed(ctx, &sdkconfig.Config{})
	cmd.SetContext(ctx)

	allowed, err := opts.resolveAllowDestructive(cmd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !allowed {
		t.Fatalf("expected env var to allow destructive statements")
	}
}

func TestResolveAllowDestructiveEnvInvalid(t *testing.T) {
	opts := &sqlOptions{}
	cmd := &cobra.Command{}
	cmd.Flags().BoolVar(&opts.allowDestructive, "allow-destructive", false, "")

	ctx := env.Set(context.Background(), "DATABRICKS_CLI_ALLOW_DESTRUCTIVE_SQL", "maybe")
	ctx = cmdctx.SetConfigUsed(ctx, &sdkconfig.Config{})
	cmd.SetContext(ctx)

	_, err := opts.resolveAllowDestructive(cmd)
	if err == nil {
		t.Fatalf("expected error for invalid env var value")
	}
}

func TestResolveAllowDestructiveProfile(t *testing.T) {
	opts := &sqlOptions{}
	cmd := &cobra.Command{}
	cmd.Flags().BoolVar(&opts.allowDestructive, "allow-destructive", false, "")

	cfgDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "databrickscfg")
	contents := "[DEFAULT]\ncli.allow_destructive_sql = true\n"
	if err := os.WriteFile(cfgPath, []byte(contents), 0o600); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	cfg := &sdkconfig.Config{ConfigFile: cfgPath, Profile: "DEFAULT"}
	ctx := cmdctx.SetConfigUsed(context.Background(), cfg)
	cmd.SetContext(ctx)

	allowed, err := opts.resolveAllowDestructive(cmd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !allowed {
		t.Fatalf("expected profile to allow destructive statements")
	}
}
