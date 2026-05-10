package tests

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/a2aproject/a2a-go/v2/a2asrv"

	"agent_patches/endpoint-server/executor"
)

// startTestServer spins up an httptest server with an optional bearer token.
// It returns the server and a client pre-wired to talk to it.
func startTestServer(t *testing.T, runner executor.Runner, bearerToken string) (*httptest.Server, *a2aclient.Client) {
	t.Helper()

	exec := executor.New(runner)
	opts := []a2asrv.RequestHandlerOption{}
	if bearerToken != "" {
		opts = append(opts, a2asrv.WithCallInterceptors(newTestBearerInterceptor(bearerToken)))
	}

	reqHandler := a2asrv.NewHandler(exec, opts...)

	card := &a2a.AgentCard{
		Name:    "test-agent",
		Version: "1.0.0",
	}

	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	card.SupportedInterfaces = []*a2a.AgentInterface{
		a2a.NewAgentInterface(srv.URL, a2a.TransportProtocolJSONRPC),
	}
	mux.Handle(a2asrv.WellKnownAgentCardPath, a2asrv.NewStaticAgentCardHandler(card))
	mux.Handle("/", a2asrv.NewJSONRPCHandler(reqHandler))

	t.Cleanup(srv.Close)

	clientOpts := []a2aclient.FactoryOption{}
	if bearerToken != "" {
		clientOpts = append(clientOpts, a2aclient.WithCallInterceptors(
			&testBearerInterceptor{token: bearerToken},
		))
	}

	c, err := a2aclient.NewFromCard(context.Background(), card, clientOpts...)
	if err != nil {
		t.Fatalf("NewFromCard: %v", err)
	}

	return srv, c
}

// newTestBearerInterceptor creates a server-side bearer auth interceptor for tests.
func newTestBearerInterceptor(token string) *testServerBearerInterceptor {
	return &testServerBearerInterceptor{token: token}
}

type testServerBearerInterceptor struct {
	a2asrv.PassthroughCallInterceptor
	token string
}

func (b *testServerBearerInterceptor) Before(ctx context.Context, callCtx *a2asrv.CallContext, _ *a2asrv.Request) (context.Context, any, error) {
	vals, ok := callCtx.ServiceParams().Get("authorization")
	if !ok || len(vals) == 0 {
		return ctx, nil, a2a.ErrUnauthenticated
	}
	tok, ok := strings.CutPrefix(vals[0], "Bearer ")
	if !ok || tok != b.token {
		return ctx, nil, a2a.ErrUnauthenticated
	}
	return ctx, nil, nil
}

type testBearerInterceptor struct {
	a2aclient.PassthroughInterceptor
	token string
}

func (b *testBearerInterceptor) Before(ctx context.Context, req *a2aclient.Request) (context.Context, any, error) {
	req.ServiceParams["Authorization"] = []string{"Bearer " + b.token}
	return ctx, nil, nil
}

// ---- Tests ------------------------------------------------------------------

func TestServer_SendMessage_ReturnsAgentText(t *testing.T) {
	_, c := startTestServer(t, &mockRunner{resp: "world"}, "")

	msg := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("hello"))
	result, err := c.SendMessage(context.Background(), &a2a.SendMessageRequest{Message: msg})
	if err != nil {
		t.Fatalf("SendMessage() error: %v", err)
	}

	resp, ok := result.(*a2a.Message)
	if !ok {
		t.Fatalf("result type = %T, want *a2a.Message", result)
	}
	if got := resp.Parts[0].Text(); got != "world" {
		t.Errorf("text = %q, want %q", got, "world")
	}
}

func TestServer_SendMessage_AgentError_ReturnsError(t *testing.T) {
	_, c := startTestServer(t, &mockRunner{err: errTest}, "")

	msg := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("hello"))
	_, err := c.SendMessage(context.Background(), &a2a.SendMessageRequest{Message: msg})
	if err == nil {
		t.Fatal("expected error from agent failure, got nil")
	}
}

func TestServer_Auth_ValidToken_Passes(t *testing.T) {
	_, c := startTestServer(t, &mockRunner{resp: "world"}, "s3cr3t")

	msg := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("hello"))
	_, err := c.SendMessage(context.Background(), &a2a.SendMessageRequest{Message: msg})
	if err != nil {
		t.Fatalf("expected success with valid token, got: %v", err)
	}
}

func TestServer_Auth_NoToken_Rejected(t *testing.T) {
	_, _ = startTestServer(t, &mockRunner{resp: "world"}, "s3cr3t")

	// Create a client WITHOUT a bearer interceptor.
	card := &a2a.AgentCard{
		Name:    "test-agent",
		Version: "1.0.0",
	}
	// We need the server URL — start a dedicated one.
	exec := executor.New(&mockRunner{resp: "world"})
	reqHandler := a2asrv.NewHandler(exec,
		a2asrv.WithCallInterceptors(newTestBearerInterceptor("s3cr3t")),
	)
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()

	card.SupportedInterfaces = []*a2a.AgentInterface{
		a2a.NewAgentInterface(srv.URL, a2a.TransportProtocolJSONRPC),
	}
	mux.Handle(a2asrv.WellKnownAgentCardPath, a2asrv.NewStaticAgentCardHandler(card))
	mux.Handle("/", a2asrv.NewJSONRPCHandler(reqHandler))

	c, err := a2aclient.NewFromCard(context.Background(), card) // no auth interceptor
	if err != nil {
		t.Fatalf("NewFromCard: %v", err)
	}

	msg := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("hello"))
	_, err = c.SendMessage(context.Background(), &a2a.SendMessageRequest{Message: msg})
	if err == nil {
		t.Fatal("expected error for missing token, got nil")
	}
}

func TestServer_AgentCard_AlwaysPublic(t *testing.T) {
	srv, _ := startTestServer(t, &mockRunner{resp: "world"}, "s3cr3t")

	resp, err := http.Get(srv.URL + a2asrv.WellKnownAgentCardPath)
	if err != nil {
		t.Fatalf("GET agent card: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("agent card status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
}

func TestServer_Streaming_ReturnsEvents(t *testing.T) {
	_, c := startTestServer(t, &mockRunner{resp: "world"}, "")

	msg := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("hello"))
	var events []a2a.Event
	for ev, err := range c.SendStreamingMessage(context.Background(), &a2a.SendMessageRequest{Message: msg}) {
		if err != nil {
			t.Fatalf("streaming event error: %v", err)
		}
		events = append(events, ev)
	}
	if len(events) == 0 {
		t.Fatal("expected at least one streaming event, got none")
	}
}

// errTest is a sentinel for agent errors in tests.
var errTest = iter_error("test agent error")

type iter_error string

func (e iter_error) Error() string { return string(e) }
