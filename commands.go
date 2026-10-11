package main

import (
	"fmt"
	"strings"

	"github.com/sandertv/gophertunnel/minecraft/resource"
)

type chatCommand struct {
	name        string
	aliases     []string
	description string
	usage       string
	handler     func(args []string) string
}

type commandEngine struct {
	prefix         string
	forwardUnknown bool
	listenAddr     string
	remoteAddr     string
	packs          []*resource.Pack
	diagnostics    *diagnosticsTracker
	commands       []chatCommand
	commandsByName map[string]chatCommand
}

func newCommandEngine(prefix string, forwardUnknown bool, listenAddr, remoteAddr string, packs []*resource.Pack, diagnostics *diagnosticsTracker) (*commandEngine, error) {
	engine := &commandEngine{
		prefix:         prefix,
		forwardUnknown: forwardUnknown,
		listenAddr:     listenAddr,
		remoteAddr:     remoteAddr,
		packs:          packs,
		diagnostics:    diagnostics,
		commandsByName: make(map[string]chatCommand),
	}
	builtins := []chatCommand{
		{name: "help", aliases: []string{"?"}, description: "Show available proxy commands.", usage: "help [command|all]"},
		{name: "packstatus", aliases: []string{"packs"}, description: "Show locally loaded and offered resource packs.", usage: "packstatus"},
		{name: "proxyinfo", aliases: []string{"info"}, description: "Show proxy connection settings.", usage: "proxyinfo"},
		{name: "scanner", description: "Show or reset observed packet diagnostics.", usage: "scanner [status|reset]"},
	}
	for _, command := range builtins {
		if err := engine.register(command); err != nil {
			return nil, err
		}
	}
	if err := registerCustomCommands(engine); err != nil {
		return nil, fmt.Errorf("register custom chat commands: %w", err)
	}
	return engine, nil
}

func (engine *commandEngine) register(command chatCommand) error {
	command.name = strings.ToLower(strings.TrimSpace(command.name))
	if command.name == "" || strings.ContainsAny(command.name, " \t\r\n") {
		return fmt.Errorf("command name must be a non-empty single word")
	}
	command.aliases = append([]string(nil), command.aliases...)
	names := append([]string{command.name}, command.aliases...)
	for i, name := range names {
		names[i] = strings.ToLower(strings.TrimSpace(name))
		if names[i] == "" || strings.ContainsAny(names[i], " \t\r\n") {
			return fmt.Errorf("command %q has an invalid alias %q", command.name, name)
		}
		if _, exists := engine.commandsByName[names[i]]; exists {
			return fmt.Errorf("command name or alias %q is already registered", names[i])
		}
	}
	engine.commands = append(engine.commands, command)
	for _, name := range names {
		engine.commandsByName[name] = command
	}
	return nil
}

// handle processes a period-prefixed chat message. A true handled result means it
// must not be sent to the upstream server.
func (engine *commandEngine) handle(message string, activePlayers int64) (response string, handled bool) {
	if !strings.HasPrefix(message, engine.prefix) {
		return "", false
	}
	input := strings.TrimSpace(strings.TrimPrefix(message, engine.prefix))
	fields := strings.Fields(input)
	if len(fields) == 0 {
		if engine.forwardUnknown {
			return "", false
		}
		return "Type " + engine.prefix + "help to see available proxy commands.", true
	}

	name := strings.ToLower(fields[0])
	command, ok := engine.commandsByName[name]
	if !ok {
		if engine.forwardUnknown {
			return "", false
		}
		return fmt.Sprintf("Unknown proxy command %q. Use %shelp.", name, engine.prefix), true
	}
	args := fields[1:]
	if command.handler != nil {
		return command.handler(args), true
	}
	switch command.name {
	case "help":
		return engine.help(args)
	case "packstatus":
		if len(args) != 0 {
			return "Usage: " + engine.prefix + command.usage, true
		}
		if len(engine.packs) == 0 {
			return "No valid local resource packs are loaded; no pack is being offered.", true
		}
		var status strings.Builder
		fmt.Fprintf(&status, "%d resource pack(s) loaded and required from clients:", len(engine.packs))
		for _, pack := range engine.packs {
			fmt.Fprintf(&status, " %s v%s (%s);", pack.Name(), pack.Version(), pack.UUID())
		}
		return strings.TrimSuffix(status.String(), ";"), true
	case "proxyinfo":
		if len(args) != 0 {
			return "Usage: " + engine.prefix + command.usage, true
		}
		return fmt.Sprintf("Proxy listens on %s; upstream %s; %d player connection(s) active.", engine.listenAddr, engine.remoteAddr, activePlayers), true
	case "scanner":
		return engine.scannerCommand(args)
	}
	return "This proxy command is not available.", true
}

func (engine *commandEngine) scannerCommand(args []string) (string, bool) {
	if len(args) == 0 || (len(args) == 1 && strings.EqualFold(args[0], "status")) {
		return strings.Join(engine.diagnostics.statusLines(), "\n"), true
	}
	if len(args) == 1 && strings.EqualFold(args[0], "reset") {
		engine.diagnostics.reset()
		return "Scanner diagnostic counters and cached metadata reset.", true
	}
	return "Usage: " + engine.prefix + "scanner [status|reset]", true
}

func (engine *commandEngine) handleConsole(message string, activePlayers int64) (string, bool) {
	message = strings.TrimSpace(message)
	if message == "" {
		return "", false
	}
	if !strings.HasPrefix(message, engine.prefix) {
		message = engine.prefix + message
	}
	response, handled := engine.handle(message, activePlayers)
	if handled {
		return response, true
	}
	fields := strings.Fields(strings.TrimPrefix(message, engine.prefix))
	if len(fields) == 0 {
		return "", false
	}
	return fmt.Sprintf("Unknown proxy command %q.", strings.ToLower(fields[0])), true
}

func (engine *commandEngine) help(args []string) (string, bool) {
	if len(args) > 1 {
		return "Usage: " + engine.prefix + "help [command|all]", true
	}
	if len(args) == 1 && !strings.EqualFold(args[0], "all") {
		command, ok := engine.commandsByName[strings.ToLower(args[0])]
		if !ok {
			return fmt.Sprintf("No available proxy command named %q. Use %shelp all.", args[0], engine.prefix), true
		}
		return fmt.Sprintf("%s%s: %s Usage: %s%s", engine.prefix, command.name, command.description, engine.prefix, command.usage), true
	}
	var help strings.Builder
	fmt.Fprintf(&help, "Available proxy commands (prefix %q):", engine.prefix)
	for _, command := range engine.commands {
		fmt.Fprintf(&help, " %s%s - %s Usage: %s%s;", engine.prefix, command.name, command.description, engine.prefix, command.usage)
	}
	return strings.TrimSuffix(help.String(), ";"), true
}
