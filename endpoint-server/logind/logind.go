// Package logind wraps the systemd-logind D-Bus API to enumerate and fetch
// properties of interactive login sessions. It is used by both the
// check_interactive_logins skill and the loginmonitor background service.
package logind

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
)

const (
	LogindDest   = "org.freedesktop.login1"
	LogindPath   = dbus.ObjectPath("/org/freedesktop/login1")
	LogindIface  = "org.freedesktop.login1.Manager"
	SessionIface = "org.freedesktop.login1.Session"
)

// SessionInfo holds the properties of one login session.
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
	Leader      uint32 // PID of session leader
	Timestamp   time.Time
}

// ListSessions connects to the system D-Bus, enumerates all sessions known
// to logind, and fetches their properties. The connection is closed before
// returning.
func ListSessions() ([]SessionInfo, error) {
	slog.Debug("logind: connecting to system D-Bus")
	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		return nil, fmt.Errorf("connect to system bus: %w", err)
	}
	defer conn.Close()

	return ListSessionsConn(conn)
}

// ListSessionsConn enumerates sessions using an already-open D-Bus connection.
// Use this when the caller manages the connection lifetime (e.g. the monitor).
func ListSessionsConn(conn *dbus.Conn) ([]SessionInfo, error) {
	obj := conn.Object(LogindDest, LogindPath)

	var rawSessions []struct {
		ID   string
		UID  uint32
		User string
		Seat string
		Path dbus.ObjectPath
	}
	slog.Debug("logind: calling ListSessions")
	if err := obj.Call(LogindIface+".ListSessions", 0).Store(&rawSessions); err != nil {
		return nil, fmt.Errorf("ListSessions: %w", err)
	}
	slog.Debug("logind: ListSessions returned", "count", len(rawSessions))

	sessions := make([]SessionInfo, 0, len(rawSessions))
	for _, rs := range rawSessions {
		info, err := FetchSessionInfo(conn, rs.Path)
		if err != nil {
			slog.Debug("logind: skipping session", "id", rs.ID, "error", err)
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

// FetchSessionInfo calls GetAll on a session D-Bus object and maps the
// returned variant map to a SessionInfo.
func FetchSessionInfo(conn *dbus.Conn, path dbus.ObjectPath) (*SessionInfo, error) {
	obj := conn.Object(LogindDest, path)

	var props map[string]dbus.Variant
	if err := obj.Call("org.freedesktop.DBus.Properties.GetAll", 0, SessionIface).Store(&props); err != nil {
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
