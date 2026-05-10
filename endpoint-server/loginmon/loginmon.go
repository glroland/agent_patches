package loginmon

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"

	"agent_patches/endpoint-server/config"
	"agent_patches/endpoint-server/notifier"
)

const (
	logindDest   = "org.freedesktop.login1"
	logindPath   = dbus.ObjectPath("/org/freedesktop/login1")
	logindIface  = "org.freedesktop.login1.Manager"
	sessionIface = "org.freedesktop.login1.Session"
)

// Monitor listens for systemd-logind session-creation events over D-Bus and
// sends a notification for every new interactive user session.
// The goroutine always starts; D-Bus connectivity failures are retried with
// backoff so a transient unavailability does not permanently silence the monitor.
type Monitor struct {
	cfg      *config.LoginMonitorSettings
	notifier *notifier.Notifier
}

// New creates a Monitor.
func New(cfg *config.LoginMonitorSettings, n *notifier.Notifier) *Monitor {
	return &Monitor{cfg: cfg, notifier: n}
}

// Start launches the background listener in a new goroutine and returns
// immediately. The goroutine exits when ctx is cancelled.
func (m *Monitor) Start(ctx context.Context) {
	if !m.cfg.Enabled {
		slog.Info("login_monitor: disabled")
		return
	}
	slog.Info("login_monitor: starting")
	go m.loop(ctx)
}

// loop retries the D-Bus session whenever it ends, backing off on repeated
// failures so a broken bus does not spin the CPU.
func (m *Monitor) loop(ctx context.Context) {
	backoff := 5 * time.Second
	const maxBackoff = 5 * time.Minute

	for {
		if err := m.runSession(ctx); err != nil {
			if ctx.Err() != nil {
				slog.Info("login_monitor: stopped")
				return
			}
			slog.Warn("login_monitor: D-Bus session ended, retrying",
				"error", err, "backoff", backoff)
			select {
			case <-ctx.Done():
				slog.Info("login_monitor: stopped")
				return
			case <-time.After(backoff):
			}
			if backoff < maxBackoff {
				backoff *= 2
			}
			continue
		}
		// Clean exit means ctx was cancelled.
		slog.Info("login_monitor: stopped")
		return
	}
}

// runSession opens one D-Bus connection, subscribes to SessionNew, and
// dispatches signals until either ctx is cancelled or the connection drops.
func (m *Monitor) runSession(ctx context.Context) error {
	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		return fmt.Errorf("connect to system bus: %w", err)
	}
	defer conn.Close()

	if err := conn.AddMatchSignal(
		dbus.WithMatchObjectPath(logindPath),
		dbus.WithMatchInterface(logindIface),
		dbus.WithMatchMember("SessionNew"),
	); err != nil {
		return fmt.Errorf("add match rule: %w", err)
	}

	ch := make(chan *dbus.Signal, 16)
	conn.Signal(ch)
	defer conn.RemoveSignal(ch)

	slog.Info("login_monitor: listening for interactive logins")

	for {
		select {
		case <-ctx.Done():
			return nil
		case sig, ok := <-ch:
			if !ok {
				return fmt.Errorf("signal channel closed")
			}
			if sig != nil {
				m.handleSessionNew(ctx, conn, sig)
			}
		}
	}
}

// handleSessionNew is called for every SessionNew signal. It fetches session
// properties, discards non-user sessions, then notifies.
func (m *Monitor) handleSessionNew(ctx context.Context, conn *dbus.Conn, sig *dbus.Signal) {
	if len(sig.Body) < 2 {
		return
	}
	sessionID, _ := sig.Body[0].(string)
	sessionPath, ok := sig.Body[1].(dbus.ObjectPath)
	if !ok {
		return
	}

	info, err := fetchSessionInfo(conn, sessionPath)
	if err != nil {
		slog.Warn("login_monitor: failed to read session properties",
			"session", sessionID, "error", err)
		return
	}

	// Only alert on interactive user sessions; skip greeter, lock-screen, etc.
	if info.Class != "user" {
		slog.Debug("login_monitor: ignoring non-user session",
			"session", sessionID, "class", info.Class)
		return
	}

	slog.Info("login_monitor: interactive login detected",
		"user", info.Username,
		"type", info.SessionType,
		"remote", info.Remote,
		"from", info.RemoteHost,
	)

	host, _ := os.Hostname()
	m.notifier.Notify(ctx,
		fmt.Sprintf("[%s] Login: %s", host, info.Username),
		buildMessage(host, sessionID, info),
	)
}

// sessionInfo holds the properties we extract from a logind session object.
type sessionInfo struct {
	Username    string
	Class       string // "user", "greeter", "lock-screen", "background"
	SessionType string // "tty", "x11", "wayland", "mir", "unspecified"
	Remote      bool
	RemoteHost  string
	RemoteUser  string
	TTY         string
	Display     string
	Leader      uint32   // PID of the session leader process
	Timestamp   time.Time
}

// fetchSessionInfo calls GetAll on the session D-Bus object and maps the
// returned variant map to a sessionInfo.
func fetchSessionInfo(conn *dbus.Conn, path dbus.ObjectPath) (*sessionInfo, error) {
	obj := conn.Object(logindDest, path)

	var props map[string]dbus.Variant
	if err := obj.Call("org.freedesktop.DBus.Properties.GetAll", 0, sessionIface).Store(&props); err != nil {
		return nil, fmt.Errorf("GetAll(%s): %w", path, err)
	}

	info := &sessionInfo{}

	if v, ok := props["Name"]; ok {
		info.Username, _ = v.Value().(string)
	}
	if v, ok := props["Class"]; ok {
		info.Class, _ = v.Value().(string)
	}
	if v, ok := props["Type"]; ok {
		info.SessionType, _ = v.Value().(string)
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
	if info.Timestamp.IsZero() {
		info.Timestamp = time.Now().UTC()
	}

	return info, nil
}

// buildMessage composes the human-readable notification body.
func buildMessage(host, sessionID string, info *sessionInfo) string {
	var sb strings.Builder

	fmt.Fprintf(&sb, "Interactive login detected on host %q.\n\n", host)
	fmt.Fprintf(&sb, "User:         %s\n", info.Username)
	fmt.Fprintf(&sb, "Time:         %s\n", info.Timestamp.Format(time.RFC1123))
	fmt.Fprintf(&sb, "Session ID:   %s\n", sessionID)
	fmt.Fprintf(&sb, "Session type: %s\n", info.SessionType)

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

	return sb.String()
}
