package main

import (
	"strings"
	"testing"
)

func testCommandEngine(t *testing.T, forwardUnknown bool) *commandEngine {
	t.Helper()
	diagnostics, err := newDiagnosticsTracker(true, 8)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := newCommandEngine(".", forwardUnknown, "0.0.0.0:25665", "donutsmp.net:19132", nil, diagnostics)
	if err != nil {
		t.Fatal(err)
	}
	return engine
}

func TestCommandEngineHandlesSafeCommandsLocally(t *testing.T) {
	engine := testCommandEngine(t, false)
	tests := []struct {
		message  string
		contains string
	}{
		{message: ".HELP", contains: ".help"},
		{message: ".help all", contains: ".packstatus"},
		{message: ".help esp", contains: "No available proxy command"},
		{message: ".packs", contains: "No valid local resource packs"},
		{message: ".proxyinfo", contains: "donutsmp.net:19132"},
	}
	for _, test := range tests {
		t.Run(test.message, func(t *testing.T) {
			response, handled := engine.handle(test.message, 3)
			if !handled {
				t.Fatal("expected recognized proxy command to be handled locally")
			}
			if !strings.Contains(strings.ToLower(response), strings.ToLower(test.contains)) {
				t.Fatalf("response %q does not contain %q", response, test.contains)
			}
		})
	}
}

func TestCommandEngineUnknownPrefixForwardingIsConfigurable(t *testing.T) {
	forwardEngine := testCommandEngine(t, true)
	if response, handled := forwardEngine.handle(".serverplugin", 0); handled || response != "" {
		t.Fatalf("unknown command should be forwarded, got handled=%t response=%q", handled, response)
	}

	localEngine := testCommandEngine(t, false)
	if response, handled := localEngine.handle(".serverplugin", 0); !handled || !strings.Contains(response, "Unknown proxy command") {
		t.Fatalf("unknown command should be handled locally with explanation, got handled=%t response=%q", handled, response)
	}
}

func TestCommandEnginePrefixIsConfigurable(t *testing.T) {
	diagnostics, err := newDiagnosticsTracker(true, 8)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := newCommandEngine("!", false, "", "", nil, diagnostics)
	if err != nil {
		t.Fatal(err)
	}
	if response, handled := engine.handle("!help", 0); !handled || !strings.Contains(response, "Available proxy commands") {
		t.Fatalf("custom prefix was not recognized: handled=%t response=%q", handled, response)
	}
	if response, handled := engine.handle(".help", 0); handled || response != "" {
		t.Fatalf("default prefix should not match with custom prefix: handled=%t response=%q", handled, response)
	}
}
