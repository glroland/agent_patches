// Package logapi implements the GET /log endpoint, which returns the tail of
// the agent's log file for display in central-ui.
package logapi

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
)

// maxBytes is the maximum number of bytes read from the end of the log file.
const maxBytes = 1 << 20 // 1 MB

// Service serves GET /log responses from the agent's log file.
type Service struct {
	logFile string
}

// New creates a log Service. logFile is the path configured under logging.file;
// an empty string means the agent is logging to stderr.
func New(logFile string) *Service {
	return &Service{logFile: logFile}
}

// Handler returns the http.HandlerFunc for GET /log.
func (s *Service) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		w.Header().Set("Content-Type", "application/json")

		if s.logFile == "" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"content":   "",
				"truncated": false,
				"note":      "agent is logging to stderr; no log file configured",
			})
			return
		}

		f, err := os.Open(s.logFile)
		if err != nil {
			if os.IsNotExist(err) {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"content":   "",
					"truncated": false,
					"note":      "log file does not exist yet",
				})
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer f.Close()

		info, err := f.Stat()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		truncated := false
		if info.Size() > maxBytes {
			if _, err := f.Seek(-maxBytes, io.SeekEnd); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			truncated = true
		}

		data, err := io.ReadAll(f)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		_ = json.NewEncoder(w).Encode(map[string]any{
			"content":   string(data),
			"truncated": truncated,
		})
	}
}
