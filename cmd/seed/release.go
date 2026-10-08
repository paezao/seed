package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"seed/kernel/template"
	"seed/kernel/update"
)

// cmdRelease makes and signs kernel releases (used by the release workflow).
//
//	seed release keygen --out release.key     (prints the public key)
//	seed release sign --key release.key --template template.tar.gz \
//	  --dockerfile Dockerfile --version 2026.10.20 --notes notes.md --out dist/
func cmdRelease(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: seed release keygen|sign …")
	}
	switch args[0] {
	case "keygen":
		fs := flag.NewFlagSet("keygen", flag.ContinueOnError)
		out := fs.String("out", "", "file for the private key (created 0600; never printed)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *out == "" {
			return errors.New("--out is required")
		}
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return err
		}
		f, err := os.OpenFile(*out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return err
		}
		if _, err := f.WriteString(base64.StdEncoding.EncodeToString(priv.Seed()) + "\n"); err != nil {
			f.Close()
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}
		fmt.Println(hex.EncodeToString(pub))
		return nil
	case "sign":
		fs := flag.NewFlagSet("sign", flag.ContinueOnError)
		keyFile := fs.String("key", "", "private key file (or SEED_RELEASE_KEY in the environment)")
		tmpl := fs.String("template", "", "template.tar.gz")
		dockerfile := fs.String("dockerfile", "Dockerfile", "the release's runtime Dockerfile")
		version := fs.String("version", "", "release version")
		notes := fs.String("notes", "", "release notes (markdown file)")
		out := fs.String("out", "dist", "output directory")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		seed := os.Getenv("SEED_RELEASE_KEY")
		if *keyFile != "" {
			b, err := os.ReadFile(*keyFile)
			if err != nil {
				return err
			}
			seed = string(b)
		}
		raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(seed))
		if err != nil || len(raw) != ed25519.SeedSize {
			return errors.New("no valid release key (--key or SEED_RELEASE_KEY)")
		}
		key := ed25519.NewKeyFromSeed(raw)
		t, err := os.ReadFile(*tmpl)
		if err != nil {
			return err
		}
		df, err := os.ReadFile(*dockerfile)
		if err != nil {
			return err
		}
		var notesText string
		if *notes != "" {
			b, err := os.ReadFile(*notes)
			if err != nil {
				return err
			}
			notesText = string(b)
		}
		if *version == "" {
			return errors.New("--version is required")
		}
		m := update.Manifest{Version: *version, PublishedAt: time.Now().UTC().Truncate(time.Second), Notes: notesText,
			Template: update.RuntimeHash(t), TemplateSize: int64(len(t)), Runtime: update.RuntimeHash(df)}
		manifest, err := json.MarshalIndent(m, "", "  ")
		if err != nil {
			return err
		}
		if _, err := update.Verify(manifest, update.Sign(manifest, key), []ed25519.PublicKey{key.Public().(ed25519.PublicKey)}); err != nil {
			return err
		}
		if err := os.MkdirAll(*out, 0o755); err != nil {
			return err
		}
		for name, data := range map[string][]byte{"manifest.json": manifest, "manifest.json.sig": update.Sign(manifest, key), "template.tar.gz": t} {
			if err := os.WriteFile(filepath.Join(*out, name), data, 0o644); err != nil {
				return err
			}
		}
		fmt.Printf("signed release %s (template %s, runtime %s)\n", m.Version, m.Template[:12], m.Runtime[:12])
		return nil
	}
	return fmt.Errorf("unknown release command %q", args[0])
}

// cmdKernelRollback is run by the boot script with the last good kernel
// binary when a new kernel fails to build or crashes right after starting.
// Exit 3: nothing to roll back (the kernel files are the good ones).
func cmdKernelRollback(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("kernel-rollback", flag.ContinueOnError)
	dir := fs.String("dir", "/seed", "Seed directory")
	reason := fs.String("reason", "failed to start", "what went wrong")
	logFile := fs.String("log", "", "build or start log to keep with the report")
	if err := fs.Parse(args); err != nil {
		return err
	}
	good, err := os.ReadFile(filepath.Join(*dir, ".seed", "kernel-good"))
	if err != nil {
		return fmt.Errorf("no kernel has run well here yet: %w", err)
	}
	res, err := template.RollbackKernel(ctx, *dir, strings.TrimSpace(string(good)), *reason)
	if errors.Is(err, template.ErrNothingToRollBack) {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(3)
	}
	if err != nil {
		return err
	}
	var logTail string
	if *logFile != "" {
		if b, err := os.ReadFile(*logFile); err == nil {
			if len(b) > 4000 {
				b = b[len(b)-4000:]
			}
			logTail = string(b)
		}
	}
	report, _ := json.Marshal(map[string]any{"from": res.From, "to": res.To, "generation": res.Generation,
		"commit": res.Commit, "reason": *reason, "log": logTail, "at": time.Now().UTC()})
	if err := os.WriteFile(filepath.Join(*dir, ".seed", "kernel-rollback.json"), report, 0o600); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "my new kernel %s; went back to %s (generation %d)\n", *reason, res.To, res.Generation)
	return nil
}
