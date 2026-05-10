package tasks

import (
	"context"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/toolrunner"

	"agent_patches/server/patching"
)

type patchInput struct{}

// NewPatchTool creates a task tool that patches the current system.
// It auto-detects whether the OS is Windows, Debian-based, or Fedora-based,
// runs the appropriate package manager, and reboots the system if required.
func NewPatchTool() (anthropic.BetaTool, error) {
	return toolrunner.NewBetaToolFromJSONSchema(
		"patch",
		"Patches the current system. Detects the OS (Windows, Debian-based Linux, "+
			"or Fedora-based Linux), runs the appropriate update commands, checks "+
			"whether a reboot is required, and reboots the system if so.",
		func(ctx context.Context, _ patchInput) (anthropic.BetaToolResultBlockParamContentUnion, error) {
			p, err := patching.New()
			if err != nil {
				return textResult(fmt.Sprintf("OS detection failed: %v", err)), nil
			}

			log, err := p.Run(ctx)
			if err != nil {
				return textResult(fmt.Sprintf("%serror: %v", log, err)), nil
			}

			return textResult(log), nil
		},
	)
}

func textResult(s string) anthropic.BetaToolResultBlockParamContentUnion {
	return anthropic.BetaToolResultBlockParamContentUnion{
		OfText: &anthropic.BetaTextBlockParam{Text: s},
	}
}
