package main

import (
	"bytes"
	"testing"
)

func TestParseArgumentsRecognizesVersion(t *testing.T) {
	configPath, showVersion, err := parseArguments([]string{"--version"})
	if err != nil {
		t.Fatalf("parse arguments: %v", err)
	}
	if !showVersion {
		t.Fatal("showVersion = false, want true")
	}
	if configPath != "tvlink.toml" {
		t.Fatalf("configPath = %q, want %q", configPath, "tvlink.toml")
	}
}

func TestWriteVersion(t *testing.T) {
	originalVersion := version
	version = "v1.2.3"
	t.Cleanup(func() { version = originalVersion })

	var output bytes.Buffer
	writeVersion(&output)
	if got, want := output.String(), "TvLink v1.2.3\n"; got != want {
		t.Fatalf("version output = %q, want %q", got, want)
	}
}
