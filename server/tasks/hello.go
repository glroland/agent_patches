package tasks

import (
	"context"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/toolrunner"
)

type helloInput struct{}

// NewHelloTool returns a task tool that responds with "world".
func NewHelloTool() (anthropic.BetaTool, error) {
	return toolrunner.NewBetaToolFromJSONSchema(
		"hello",
		"Returns 'world' as a response to any greeting or hello request.",
		func(_ context.Context, _ helloInput) (anthropic.BetaToolResultBlockParamContentUnion, error) {
			return anthropic.BetaToolResultBlockParamContentUnion{
				OfText: &anthropic.BetaTextBlockParam{Text: "world"},
			}, nil
		},
	)
}
