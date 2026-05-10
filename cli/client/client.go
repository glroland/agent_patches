package client

import (
	"context"
	"fmt"
	"strings"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/a2aproject/a2a-go/v2/a2aclient/agentcard"
)

// Client wraps the a2aclient.Client with helpers for patches-cli.
type Client struct {
	inner *a2aclient.Client
	card  *a2a.AgentCard
}

// New fetches the agent card from serverURL and constructs a Client.
// If token is non-empty it is sent as a Bearer token on every request.
func New(ctx context.Context, serverURL, token string) (*Client, error) {
	card, err := agentcard.DefaultResolver.Resolve(ctx, serverURL)
	if err != nil {
		return nil, fmt.Errorf("client: resolve agent card from %s: %w", serverURL, err)
	}

	opts := []a2aclient.FactoryOption{}
	if token != "" {
		opts = append(opts, a2aclient.WithCallInterceptors(&staticBearerInterceptor{token: token}))
	}

	inner, err := a2aclient.NewFromCard(ctx, card, opts...)
	if err != nil {
		return nil, fmt.Errorf("client: create from card: %w", err)
	}

	return &Client{inner: inner, card: card}, nil
}

// SendTask sends a message and returns the agent's text response.
func (c *Client) SendTask(ctx context.Context, input string) (string, error) {
	msg := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart(input))
	result, err := c.inner.SendMessage(ctx, &a2a.SendMessageRequest{Message: msg})
	if err != nil {
		return "", fmt.Errorf("client: send message: %w", err)
	}
	return extractText(result), nil
}

// AgentCard returns the cached agent card fetched during New.
func (c *Client) AgentCard() *a2a.AgentCard {
	return c.card
}

// extractText pulls all text parts from a SendMessageResult.
func extractText(result a2a.SendMessageResult) string {
	var sb strings.Builder
	switch v := result.(type) {
	case *a2a.Message:
		for _, part := range v.Parts {
			sb.WriteString(part.Text())
		}
	case *a2a.Task:
		if v.Status.Message != nil {
			for _, part := range v.Status.Message.Parts {
				sb.WriteString(part.Text())
			}
		}
		for _, artifact := range v.Artifacts {
			for _, part := range artifact.Parts {
				sb.WriteString(part.Text())
			}
		}
	}
	return sb.String()
}

// staticBearerInterceptor attaches a fixed Bearer token to every outgoing request.
type staticBearerInterceptor struct {
	a2aclient.PassthroughInterceptor
	token string
}

func (s *staticBearerInterceptor) Before(ctx context.Context, req *a2aclient.Request) (context.Context, any, error) {
	req.ServiceParams["Authorization"] = []string{"Bearer " + s.token}
	return ctx, nil, nil
}
