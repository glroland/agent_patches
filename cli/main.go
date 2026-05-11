package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"agent_patches/cli/client"
)

func main() {
	server  := flag.String("server", "http://localhost:8080", "A2A server URL")
	token   := flag.String("token", "", "Bearer token for authentication")
	info    := flag.Bool("info", false, "Print the agent card and exit")
	verbose := flag.Bool("v", false, "Print progress messages")

	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: patches-cli [flags] <skill> [args...]")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Flags:")
		flag.PrintDefaults()
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Examples:")
		fmt.Fprintln(os.Stderr, "  patches-cli hello")
		fmt.Fprintln(os.Stderr, "  patches-cli patch")
		fmt.Fprintln(os.Stderr, "  patches-cli -server http://prod:8080 -token s3cr3t patch")
		fmt.Fprintln(os.Stderr, "  patches-cli -info")
	}

	flag.Parse()

	if *token == "" {
		*token = os.Getenv("AGENT_PATCHES_TOKEN")
	}

	ctx := context.Background()

	if *verbose {
		fmt.Fprintf(os.Stderr, "Connecting to %s...\n", *server)
	}

	c, err := client.New(ctx, *server, *token)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: could not connect to %s: %v\n", *server, err)
		os.Exit(1)
	}

	if *info {
		printAgentCard(c)
		return
	}

	args := flag.Args()
	if len(args) == 0 {
		flag.Usage()
		fmt.Fprintln(os.Stderr)
		printSkills(c)
		os.Exit(1)
	}

	skillName := args[0]
	skillArgs := args[1:]

	skills := skillMap(c)
	if _, ok := skills[skillName]; !ok {
		fmt.Fprintf(os.Stderr, "Error: unknown skill %q\n\n", skillName)
		printSkills(c)
		os.Exit(1)
	}

	message := buildMessage(skillName, skillArgs)

	if *verbose {
		fmt.Fprintf(os.Stderr, "Running skill %q...\n", skillName)
	}

	result, err := c.SendTask(ctx, message)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Println(result)
}

// buildMessage constructs the agent prompt for a skill invocation.
func buildMessage(skill string, args []string) string {
	if len(args) == 0 {
		return "Run the " + skill + " skill."
	}
	return "Run the " + skill + " skill: " + strings.Join(args, " ")
}

// skillMap returns the server's skills keyed by ID.
func skillMap(c *client.Client) map[string]string {
	m := make(map[string]string)
	for _, s := range c.AgentCard().Skills {
		m[s.ID] = s.Description
	}
	return m
}

// printSkills lists available skills from the agent card.
func printSkills(c *client.Client) {
	skills := c.AgentCard().Skills
	if len(skills) == 0 {
		fmt.Fprintln(os.Stderr, "No skills available.")
		return
	}
	fmt.Fprintln(os.Stderr, "Available skills:")
	for _, s := range skills {
		fmt.Fprintf(os.Stderr, "  %-16s %s\n", s.ID, s.Description)
	}
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
