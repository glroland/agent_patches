package loginsessions

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"

	"agent_patches/endpoint-server/a2a/tool"
)

const (
	logindDest   = "org.freedesktop.login1"
	logindPath   = dbus.ObjectPath("/org/freedesktop/login1")
	logindIface  = "org.freedesktop.login1.Manager"
	sessionIface = "org.freedesktop.login1.Session"
)

// SessionInfo holds the properties of an active login session.
type SessionInfo struct {
	ID          string
	Username    string
	Class       string // "user", "greeter", "lock-screen", "background"
	SessionType string // "tty", "x11", "wayland", "mir", "unspecified"
	Seat        string
	Remote      bool
	RemoteHost  string
	RemoteUser  string
	TTY         string
	Display     string
	Leader      uint32 // PID of the session leader process
	Timestamp   time.Time
}

type loginSessionsInput struct{}

// NewLoginSessionsTool returns a task tool that reports currently active
// login sessions on the host via the systemd-logind D-Bus API.
// On hosts without systemd-logind (e.g. macOS, Windows) it reports that
// session enumeration is unavailable.
func NewLoginSessionsTool() (tool.Tool, error) {
	return tool.New(
		"login_sessions",
		"Lists currently active login sessions on the host, including the "+
			"user, session type, and whether the session originated remotely. "+
			"Requires systemd-logind; unavailable on macOS and Windows.",
		func(_ context.Context, _ loginSessionsInput) (string, error) {
			sessions, err := listSessions()
			if err != nil {
				return fmt.Sprintf("Login session enumeration unavailable: %v", err), nil
			}
			if len(sessions) == 0 {
				return "No active login sessions.", nil
			}
			return BuildReport(sessions), nil
		},
	)
}

// listSessions connects to the system D-Bus, enumerates all sessions known
// to logind, and fetches their properties.
func listSessions() ([]SessionInfo, error) {
	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		return nil, fmt.Errorf("connect to system bus: %w", err)
	}
	defer conn.Close()

	obj := conn.Object(logindDest, logindPath)

	var rawSessions []struct {
		ID   string
		UID  uint32
		User string
		Seat string
		Path dbus.ObjectPath
	}
	if err := obj.Call(logindIface+".ListSessions", 0).Store(&rawSessions); err != nil {
		return nil, fmt.Errorf("ListSessions: %w", err)
	}

	sessions := make([]SessionInfo, 0, len(rawSessions))
	for _, rs := range rawSessions {
		info, err := fetchSessionInfo(conn, rs.Path)
		if err != nil {
			continue
		}
		info.ID = rs.ID
		if info.Username == "" {
			info.Username = rs.User
		}
		if info.Seat == "" {
			info.Seat = rs.Seat
		}
		sessions = append(sessions, *info)
	}
	return sessions, nil
}

// fetchSessionInfo calls GetAll on the session D-Bus object and maps the
// returned variant map to a SessionInfo.
func fetchSessionInfo(conn *dbus.Conn, path dbus.ObjectPath) (*SessionInfo, error) {
	obj := conn.Object(logindDest, path)

	var props map[string]dbus.Variant
	if err := obj.Call("org.freedesktop.DBus.Properties.GetAll", 0, sessionIface).Store(&props); err != nil {
		return nil, fmt.Errorf("GetAll(%s): %w", path, err)
	}

	info := &SessionInfo{}

	if v, ok := props["Name"]; ok {
		info.Username, _ = v.Value().(string)
	}
	if v, ok := props["Class"]; ok {
		info.Class, _ = v.Value().(string)
	}
	if v, ok := props["Type"]; ok {
		info.SessionType, _ = v.Value().(string)
	}
	if v, ok := props["Seat"]; ok {
		if seat, ok := v.Value().([]interface{}); ok && len(seat) > 0 {
			info.Seat, _ = seat[0].(string)
		}
	}
	if v, ok := props["Remote"]; ok {
		info.Remote, _ = v.Value().(bool)
	}
	if v, ok := props["RemoteHost"]; ok {
		info.RemoteHost, _ = v.Value().(string)
	}
	if v, ok := props["RemoteUser"]; ok {
		info.RemoteUser, _ = v.Value().(string)
	}
	if v, ok := props["TTY"]; ok {
		info.TTY, _ = v.Value().(string)
	}
	if v, ok := props["Display"]; ok {
		info.Display, _ = v.Value().(string)
	}
	if v, ok := props["Leader"]; ok {
		info.Leader, _ = v.Value().(uint32)
	}
	if v, ok := props["Timestamp"]; ok {
		// logind reports microseconds since the Unix epoch (UTC).
		if usec, ok := v.Value().(uint64); ok && usec > 0 {
			info.Timestamp = time.UnixMicro(int64(usec)).UTC()
		}
	}

	return info, nil
}

// BuildReport composes a human-readable summary of active login sessions.
func BuildReport(sessions []SessionInfo) string {
	var sb strings.Builder
	for i, info := range sessions {
		fmt.Fprintf(&sb, "Session ID:   %s\n", info.ID)
		fmt.Fprintf(&sb, "User:         %s\n", info.Username)
		if info.Class != "" {
			fmt.Fprintf(&sb, "Class:        %s\n", info.Class)
		}
		fmt.Fprintf(&sb, "Session type: %s\n", info.SessionType)
		if info.Seat != "" {
			fmt.Fprintf(&sb, "Seat:         %s\n", info.Seat)
		}
		if info.TTY != "" {
			fmt.Fprintf(&sb, "TTY:          %s\n", info.TTY)
		}
		if info.Display != "" {
			fmt.Fprintf(&sb, "Display:      %s\n", info.Display)
		}
		if info.Remote {
			fmt.Fprintf(&sb, "Origin:       remote\n")
			if info.RemoteHost != "" {
				fmt.Fprintf(&sb, "From host:    %s\n", info.RemoteHost)
			}
			if info.RemoteUser != "" {
				fmt.Fprintf(&sb, "Remote user:  %s\n", info.RemoteUser)
			}
		} else {
			fmt.Fprintf(&sb, "Origin:       local console\n")
		}
		if info.Leader > 0 {
			fmt.Fprintf(&sb, "Leader PID:   %d\n", info.Leader)
		}
		if !info.Timestamp.IsZero() {
			fmt.Fprintf(&sb, "Started:      %s\n", info.Timestamp.Format(time.RFC1123))
		}
		if i < len(sessions)-1 {
			fmt.Fprintf(&sb, "\n")
		}
	}
	return sb.String()
}
