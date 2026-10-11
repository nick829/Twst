package main

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"strings"

	"github.com/sandertv/gophertunnel/minecraft"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

func runConsoleCommands(input io.Reader, output io.Writer, commands *commandEngine, activePlayers func() int64) {
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 1024), 4096)
	for scanner.Scan() {
		response, handled := commands.handleConsole(scanner.Text(), activePlayers())
		if handled && response != "" {
			if _, err := fmt.Fprintln(output, response); err != nil {
				log.Printf("console command output failed: %v", err)
				return
			}
		}
	}
	if err := scanner.Err(); err != nil {
		log.Printf("console command input failed: %v", err)
	}
}

func writeCommandResponse(conn *minecraft.Conn, response string) error {
	for _, line := range strings.Split(response, "\n") {
		if line == "" {
			continue
		}
		if err := conn.WritePacket(&packet.Text{TextType: packet.TextTypeSystem, Message: line}); err != nil {
			return err
		}
	}
	return nil
}
