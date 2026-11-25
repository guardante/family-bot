package caldav

import (
	"context"
	"io"
	"net/http"
	"os"
	"testing"
)

func TestCheckUIDs(t *testing.T) {
	login := os.Getenv("ICLOUD_LOGIN")
	pass := os.Getenv("ICLOUD_PASS")
	if login == "" || pass == "" {
		t.Skip("нет env")
	}
	// UID'ы из логов сессии
	uids := []string{
		"nadia-1795617594631674000@nadia.bot", // тестовое из TestIcloudPush
		"nadia-1795617663648745000@nadia.bot", // статья о PHP
	}
	home := "/20370946734/calendars/BA656666-EB16-478B-BFC9-831156F2DF78/" // Домашний
	client := New().NewHTTP()
	for _, uid := range uids {
		url := "https://caldav.icloud.com" + home + uid + ".ics"
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
		req.SetBasicAuth(login, pass)
		resp, err := client.Do(req)
		if err != nil {
			t.Logf("%s: %v", uid, err)
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1000000))
		resp.Body.Close()
		summary := ""
		for _, line := range splitLines(string(body)) {
			if len(line) > 8 && line[:8] == "SUMMARY:" {
				summary = line[8:]
				break
			}
		}
		t.Logf("uid=%s → HTTP %d %s", uid[:24], resp.StatusCode, summary)
	}
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, trimCR(s[start:i]))
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, trimCR(s[start:]))
	}
	return out
}

func trimCR(s string) string {
	if len(s) > 0 && s[len(s)-1] == '\r' {
		return s[:len(s)-1]
	}
	return s
}
