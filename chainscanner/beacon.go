package scanner

import (
	"bufio"
	"context"
	"errors"
	"github.com/tidwall/gjson"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
)

// Beacon head events wake the execution reader. Slot numbers never enter
// the execution cursor, including after missed slots or reconnects.
func beaconHeads(ctx context.Context, n Network, onHead func(int64) bool) error {
	req, e := http.NewRequestWithContext(ctx, "GET", n.URL, nil)
	if e != nil {
		return e
	}
	q := req.URL.Query()
	q.Set("topics", "head")
	req.URL.RawQuery = q.Encode()
	req.Header.Set("Accept", "text/event-stream")
	if n.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+n.APIKey)
	}
	res, e := newNodeHTTPClient(0).Do(req)
	if e != nil {
		return e
	}
	defer res.Body.Close()
	media, _, _ := mime.ParseMediaType(res.Header.Get("Content-Type"))
	if res.StatusCode != 200 || media != "text/event-stream" {
		return errors.New("expected Beacon head event stream")
	}
	if onHead == nil {
		return nil
	}
	lines := bufio.NewScanner(res.Body)
	lines.Buffer(make([]byte, 4096), 128<<10)
	var event, data string
	for lines.Scan() {
		line := lines.Text()
		if line == "" {
			if event == "head" && gjson.Valid(data) && gjson.Get(data, "slot").Exists() {
				slot, err := strconv.ParseInt(gjson.Get(data, "slot").String(), 10, 64)
				if err == nil && slot >= 0 && onHead(slot) {
					return nil
				}
			}
			event, data = "", ""
			continue
		}
		field, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "event":
			event = value
		case "data":
			if len(data)+len(value) > 128<<10 {
				return errors.New("oversized beacon notification")
			}
			data += value + "\n"
		}
	}
	if e = lines.Err(); e != nil {
		return e
	}
	return io.EOF
}
