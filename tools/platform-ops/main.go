// Command platform-ops supplies host-side operational helpers without a Python runtime.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

type usageError struct{ error }

func (err usageError) Unwrap() error { return err.error }

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := execute(ctx, os.Args[1:], os.Stdin, os.Stdout); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintln(os.Stderr, "platform-ops:", err)
		var usage usageError
		if errors.As(err, &usage) {
			os.Exit(2)
		}
		os.Exit(1)
	}
}

func execute(ctx context.Context, args []string, in io.Reader, out io.Writer) error {
	options := flag.NewFlagSet("platform-ops", flag.ContinueOnError)
	options.SetOutput(out)
	root := options.String("root", ".", "PressureCooker source/deployment directory")
	options.Usage = func() {
		fmt.Fprintln(out, "Usage: platform-ops [--root DIRECTORY] prepare-secrets | json-field KEY... | fetch-node | check-smoke DIRECTORY | platform-smoke [flags]")
	}
	if err := options.Parse(args); err != nil {
		return usageError{err}
	}
	args = options.Args()
	if len(args) == 0 {
		options.Usage()
		return usageError{errors.New("a command is required")}
	}
	absRoot, err := filepath.Abs(*root)
	if err != nil {
		return err
	}
	switch args[0] {
	case "prepare-secrets":
		if len(args) != 1 {
			return usageError{errors.New("prepare-secrets takes no arguments")}
		}
		return prepareSecrets(absRoot)
	case "json-field":
		if len(args) < 2 {
			return usageError{errors.New("json-field requires one or more exact object keys")}
		}
		return jsonField(in, out, args[1:])
	case "fetch-node":
		if len(args) != 1 {
			return usageError{errors.New("fetch-node takes no arguments")}
		}
		return fetchNode(ctx, absRoot, &http.Client{Timeout: 10 * time.Minute}, out)
	case "check-smoke":
		if len(args) != 2 {
			return usageError{errors.New("usage: check-smoke DIRECTORY")}
		}
		return checkSmoke(args[1], out)
	case "platform-smoke":
		return platformSmoke(ctx, args[1:], out)
	default:
		return usageError{errors.New("unknown command; use --help for usage")}
	}
}
