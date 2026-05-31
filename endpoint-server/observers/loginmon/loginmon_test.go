package loginmon

import (
	"strings"
	"testing"
	"time"
)

var baseInfo = &sessionInfo{
	Username:    "alice",
	Class:       "user",
	SessionType: "tty",
	Remote:      true,
	RemoteHost:  "10.0.0.42",
	TTY:         "pts/1",
	Leader:      4321,
	Timestamp:   time.Date(2025, 6, 1, 14, 30, 0, 0, time.UTC),
}

func TestBuildMessage_ContainsEssentialFields(t *testing.T) {
	msg := buildMessage("webserver01", "3", baseInfo)

	for _, want := range []string{
		"webserver01",
		"alice",
		"2025",     // timestamp present
		"3",        // session ID
		"tty",      // session type
		"10.0.0.42", // remote host
		"pts/1",    // TTY
		"4321",     // leader PID
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("buildMessage missing %q\nfull message:\n%s", want, msg)
		}
	}
}

func TestBuildMessage_LocalLogin(t *testing.T) {
	info := *baseInfo
	info.Remote = false
	info.RemoteHost = ""

	msg := buildMessage("host", "1", &info)

	if !strings.Contains(msg, "local console") {
		t.Errorf("expected 'local console' for non-remote session, got:\n%s", msg)
	}
	if strings.Contains(msg, "10.0.0.42") {
		t.Errorf("remote host should not appear for local session:\n%s", msg)
	}
}

func TestBuildMessage_RemoteUserIncluded(t *testing.T) {
	info := *baseInfo
	info.RemoteUser = "bob"

	msg := buildMessage("host", "1", &info)

	if !strings.Contains(msg, "bob") {
		t.Errorf("expected remote user 'bob' in message:\n%s", msg)
	}
}

func TestBuildMessage_DisplayIncluded(t *testing.T) {
	info := *baseInfo
	info.SessionType = "x11"
	info.Display = ":0"

	msg := buildMessage("host", "1", &info)

	if !strings.Contains(msg, ":0") {
		t.Errorf("expected display ':0' in message:\n%s", msg)
	}
}

func TestBuildMessage_NoLeaderPIDWhenZero(t *testing.T) {
	info := *baseInfo
	info.Leader = 0

	msg := buildMessage("host", "1", &info)

	if strings.Contains(msg, "Leader PID") {
		t.Errorf("Leader PID line should be omitted when PID is 0:\n%s", msg)
	}
}
