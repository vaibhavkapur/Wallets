// Command optimizerctl is the operator tool: tenants, API keys and the pause
// control. It talks to PostgreSQL directly and is not exposed over HTTP.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/unowallet/card-stablecoin-optimizer/orchestrator/internal/api"
	"github.com/unowallet/card-stablecoin-optimizer/orchestrator/internal/ids"
	"github.com/unowallet/card-stablecoin-optimizer/orchestrator/internal/store"
)

const usage = `usage: optimizerctl <command> [flags]

  create-tenant -id ID -name NAME
  create-key    -tenant ID [-label TEXT] [-scopes a,b,...]   (prints the key once)
  revoke-key    -id KEY_ID
  pause         [-scope global|TENANT_ID] -reason TEXT
  resume        [-scope global|TENANT_ID]

DATABASE_URL must be set. Scopes: ` + "%s\n"

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, usage, strings.Join(api.AllScopes, ", "))
		os.Exit(2)
	}
	if err := run(os.Args[1], os.Args[2:]); err != nil {
		fmt.Fprintln(os.Stderr, "optimizerctl:", err)
		os.Exit(1)
	}
}

func run(command string, args []string) error {
	flags := flag.NewFlagSet(command, flag.ExitOnError)
	id := flags.String("id", "", "identifier")
	name := flags.String("name", "", "tenant name")
	tenant := flags.String("tenant", "", "tenant id")
	label := flags.String("label", "", "key label")
	scopes := flags.String("scopes", strings.Join(api.AllScopes, ","), "comma-separated scopes")
	scope := flags.String("scope", "global", "pause scope: global or a tenant id")
	reason := flags.String("reason", "", "why the product is paused")
	if err := flags.Parse(args); err != nil {
		return err
	}

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := store.Open(ctx, dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	now := time.Now().UTC()

	switch command {
	case "create-tenant":
		if *id == "" || *name == "" {
			return fmt.Errorf("-id and -name are required")
		}
		return db.EnsureTenant(ctx, *id, *name, now)
	case "create-key":
		if *tenant == "" {
			return fmt.Errorf("-tenant is required")
		}
		granted := strings.Split(*scopes, ",")
		for _, s := range granted {
			if !contains(api.AllScopes, s) {
				return fmt.Errorf("unknown scope %q", s)
			}
		}
		secret := ids.NewAPIKey()
		keyID, err := db.CreateAPIKey(ctx, *tenant, *label, granted, secret, now)
		if err != nil {
			return err
		}
		fmt.Printf("key id: %s\nsecret: %s\n(the secret is shown once and is not stored)\n", keyID, secret)
		return nil
	case "revoke-key":
		if *id == "" {
			return fmt.Errorf("-id is required")
		}
		return db.RevokeAPIKey(ctx, *id, now)
	case "pause":
		if *reason == "" {
			return fmt.Errorf("-reason is required")
		}
		return db.SetPaused(ctx, *scope, true, *reason, now)
	case "resume":
		return db.SetPaused(ctx, *scope, false, "", now)
	default:
		return fmt.Errorf("unknown command %q", command)
	}
}

func contains(list []string, value string) bool {
	for _, v := range list {
		if v == value {
			return true
		}
	}
	return false
}
