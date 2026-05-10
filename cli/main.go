package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"agent_patches/cli/client"
	"agent_patches/server/logger"
)

func main() {
	server  := flag.String("server", "http://localhost:8080", "A2A server URL")
	token   := flag.String("token", "", "Bearer token for authentication")
	info    := flag.Bool("info", false, "Print the agent card and exit")
	verbose := flag.Bool("v", false, "Enable debug logging")

	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: patches-cli [flags] <message>")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Flags:")
		flag.PrintDefaults()
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Examples:")
		fmt.Fprintln(os.Stderr, "  patches-cli hello")
		fmt.Fprintln(os.Stderr, "  patches-cli -server http://prod:8080 -token s3cr3t hello")
		fmt.Fprintln(os.Stderr, "  patches-cli -info")
	}

	flag.Parse()

	level := "info"
	if *verbose {
		level = "debug"
	}
	logger.Setup(level)

	ctx := context.Background()

	c, err := client.New(ctx, *server, *token)
	if err != nil {
		slog.Error("failed to connect to server", "server", *server, "error", err)
		os.Exit(1)
	}

	if *info {
		printAgentCard(c)
		return
	}

	args := flag.Args()
	if len(args) == 0 {
		flag.Usage()
		os.Exit(1)
	}

	input := strings.Join(args, " ")
	slog.Debug("sending task", "server", *server, "input_len", len(input))

	result, err := c.SendTask(ctx, input)
	if err != nil {
		slog.Error("task failed", "error", err)
		os.Exit(1)
	}

	fmt.Println(result)
}

func printAgentCard(c *client.Client) {
	card := c.AgentCard()
	fmt.Printf("Name:        %s\n", card.Name)
	fmt.Printf("Description: %s\n", card.Description)
	fmt.Printf("Version:     %s\n", card.Version)
	if len(card.SecuritySchemes) > 0 {
		names := make([]string, 0, len(card.SecuritySchemes))
		for name := range card.SecuritySchemes {
			names = append(names, string(name))
		}
		fmt.Printf("Auth:        %s\n", strings.Join(names, ", "))
	} else {
		fmt.Println("Auth:        none")
	}
	if len(card.Skills) > 0 {
		fmt.Println("Skills:")
		for _, s := range card.Skills {
			fmt.Printf("  %-16s %s\n", s.ID, s.Description)
		}
	}
}
