package main

import (
	"fmt"
	"strings"
)

// Add your local chat commands here. The handler receives arguments after the
// command name and returns the message shown to the player.
func registerCustomCommands(engine *commandEngine) error {
	return engine.register(chatCommand{
		name:        "hello",
		description: "Example custom command; edit its handler in custom_commands.go.",
		usage:       "hello [name]",
		handler: func(args []string) string {
			if len(args) > 1 {
				return "Usage: " + engine.prefix + "hello [name]"
			}
			if len(args) == 0 {
				return "Hello from your custom command!"
			}
			return fmt.Sprintf("Hello, %s!", strings.TrimSpace(args[0]))
		},
	})
}
