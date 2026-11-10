package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"os"
	"testing"
)

func TestHelpIsSuccessfulAndDoesNotPrepareSecrets(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"platform-smoke", "--help"}} {
		root := t.TempDir()
		var out bytes.Buffer
		err := execute(context.Background(), append([]string{"--root", root}, args...), bytes.NewReader(nil), &out)
		if !errors.Is(err, flag.ErrHelp) || out.Len() == 0 {
			t.Fatalf("help is not successful: %v, %q", err, out.String())
		}
		entries, err := os.ReadDir(root)
		if err != nil || len(entries) != 0 {
			t.Fatal("help changed deployment files")
		}
	}
}
