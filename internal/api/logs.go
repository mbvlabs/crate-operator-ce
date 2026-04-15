package api

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"
)

const maxLogsLookback = 3 * time.Hour

func parseSinceDuration(s string) (time.Duration, string, error) {
	re := regexp.MustCompile(`^(\d+)([mhd])$`)
	matches := re.FindStringSubmatch(s)
	if matches == nil {
		return 0, "", fmt.Errorf("invalid since format %q: expected e.g. 5m, 15m, 1h", s)
	}

	value := matches[1]
	unit := matches[2]

	var unitName string
	var d time.Duration
	switch unit {
	case "m":
		unitName = "minutes"
	case "h":
		unitName = "hours"
	case "d":
		unitName = "days"
	}

	parsed, err := time.ParseDuration(value + unit)
	if err != nil {
		return 0, "", fmt.Errorf("invalid since format %q", s)
	}
	d = parsed

	return d, value + " " + unitName + " ago", nil
}

func resolveSince(since string) (string, error) {
	d, journalArg, err := parseSinceDuration(since)
	if err != nil {
		return "", err
	}
	if d > maxLogsLookback {
		return "", fmt.Errorf("since %q exceeds maximum allowed lookback of 3h", since)
	}
	return journalArg, nil
}

func writeLogsError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func (h *APIHandler) GetAppLogs(w http.ResponseWriter, r *http.Request, params GetAppLogsParams) {
	since := "5m"
	if params.Since != nil {
		since = string(*params.Since)
	}
	sinceArg, err := resolveSince(since)
	if err != nil {
		writeLogsError(w, http.StatusBadRequest, err.Error())
		return
	}

	serviceName := strings.ToLower(
		fmt.Sprintf("%s--%s--%s", params.TeamSlug, params.AppSlug, params.EnvironmentName),
	)

	out, err := sudoRun(
		"journalctl",
		"-u", serviceName,
		"--since", sinceArg,
		"--no-pager",
		"--output", "short-iso",
	).Output()

	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error":   err.Error(),
			"service": serviceName,
		})
		return
	}

	_ = json.NewEncoder(w).Encode(LogsResponse{
		Service: serviceName,
		Entries: parseJournalEntries(out),
	})
}

func (h *APIHandler) GetServiceLogs(w http.ResponseWriter, r *http.Request, params GetServiceLogsParams) {
	since := "5m"
	if params.Since != nil {
		since = string(*params.Since)
	}
	if _, err := resolveSince(since); err != nil {
		writeLogsError(w, http.StatusBadRequest, err.Error())
		return
	}

	service := string(params.Service)

	out, err := sudoRun(
		"journalctl",
		fmt.Sprintf("_SYSTEMD_UNIT=%s.service", service),
		"--since", fmt.Sprintf("-%s", since),
		"--no-pager",
		"--output", "short-iso",
	).Output()

	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	_ = json.NewEncoder(w).Encode(LogsResponse{
		Service: service,
		Entries: parseJournalEntries(out),
	})
}

var syslogPriorities = [...]LogEntryPriority{
	Emerg, Alert, Crit, Err, Warning, Notice, Info, Debug,
}

func mapPriority(n int) LogEntryPriority {
	if n < 0 || n > 7 {
		return Info
	}
	return syslogPriorities[n]
}

func parseJournalEntries(output []byte) []LogEntry {
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	var entries []LogEntry

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || strings.HasPrefix(line, "-- ") {
			continue
		}

		if strings.HasPrefix(line, " ") {
			if len(entries) > 0 {
				entries[len(entries)-1].Message += "\n" + strings.TrimLeft(line, " ")
			}
			continue
		}

		entry, ok := parseJournalLine(line)
		if !ok {
			if len(entries) > 0 {
				entries[len(entries)-1].Message += "\n" + line
			}
			continue
		}

		entries = append(entries, entry)
	}

	return entries
}

func parseJournalLine(line string) (LogEntry, bool) {
	tsEnd := strings.IndexByte(line, ' ')
	if tsEnd < 0 {
		return LogEntry{}, false
	}

	ts, err := time.Parse(time.RFC3339, line[:tsEnd])
	if err != nil {
		return LogEntry{}, false
	}

	rest := line[tsEnd+1:]

	hostEnd := strings.IndexByte(rest, ' ')
	if hostEnd < 0 {
		return LogEntry{}, false
	}
	rest = rest[hostEnd+1:]

	sepIdx := strings.Index(rest, ": ")
	if sepIdx < 0 {
		return LogEntry{}, false
	}

	msg := rest[sepIdx+2:]

	priority := Info
	return LogEntry{
		Timestamp: ts,
		Message:   msg,
		Priority:  &priority,
	}, true
}
