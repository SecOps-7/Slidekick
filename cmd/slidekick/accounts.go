package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/SecOps-7/slidekick/internal/accounts"
	"github.com/SecOps-7/slidekick/internal/oidc"
	"github.com/SecOps-7/slidekick/internal/secret"
	"github.com/SecOps-7/slidekick/internal/store"
)

// openAccounts sets up user accounts: the encryption key for stored secrets,
// the first admin (from SLIDEKICK_ADMIN_USERNAME / SLIDEKICK_ADMIN_PASSWORD, or
// via /setup with a one-time code printed here), and periodic clean-up.
func openAccounts(ctx context.Context, cmd string, st *store.Store, data, token string, log *slog.Logger) (*accounts.Service, error) {
	box, where, err := secret.Load(os.Getenv("SLIDEKICK_SECRET_KEY"), data)
	if err != nil {
		return nil, err
	}
	log.Info("secrets encryption key", "from", where)
	acc := accounts.New(st, box, oidc.NewManager(nil))

	if acc.SetupNeeded(ctx) {
		user, pw := os.Getenv("SLIDEKICK_ADMIN_USERNAME"), os.Getenv("SLIDEKICK_ADMIN_PASSWORD")
		switch {
		case user != "" && pw != "":
			if _, _, err := acc.EnsureAdmin(ctx, user, pw); err != nil {
				return nil, fmt.Errorf("SLIDEKICK_ADMIN_USERNAME/PASSWORD: %w", err)
			}
			log.Info("first admin created from SLIDEKICK_ADMIN_USERNAME", "username", user)
		case cmd == "serve":
			hint := "open /setup and enter this code"
			if token != "" {
				hint = "open /setup and enter this code or the server token"
			}
			log.Warn("no accounts yet: create the first admin", "setup_code", acc.SetupCode(), "how", hint)
		}
	}
	go func() {
		t := time.NewTicker(time.Hour)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				st.PruneExpired(ctx)
			}
		}
	}()
	return acc, nil
}

// adminCommand creates an admin, or resets an existing user to an enabled
// admin with a new password. It works while the server runs (SQLite WAL):
//
//	docker exec -i slidekick slidekick admin alice   (password on stdin)
func adminCommand(data string, args []string) error {
	if len(args) != 1 {
		return errors.New("usage: slidekick admin <username>  (password from SLIDEKICK_ADMIN_PASSWORD or the first line of stdin)")
	}
	pw := os.Getenv("SLIDEKICK_ADMIN_PASSWORD")
	if pw == "" {
		fmt.Fprint(os.Stderr, "new password: ")
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil && line == "" {
			return errors.New("no password given")
		}
		pw = strings.TrimRight(line, "\r\n")
	}
	st, err := store.Open(data)
	if err != nil {
		return err
	}
	defer st.Close()
	box, _, err := secret.Load(os.Getenv("SLIDEKICK_SECRET_KEY"), data)
	if err != nil {
		return err
	}
	acc := accounts.New(st, box, oidc.NewManager(nil))
	u, created, err := acc.EnsureAdmin(context.Background(), args[0], pw)
	if err != nil {
		return err
	}
	if created {
		fmt.Printf("created admin %q\n", u.Username)
	} else {
		fmt.Printf("%q is now an enabled admin with the new password; their other sessions were signed out\n", u.Username)
	}
	return nil
}
