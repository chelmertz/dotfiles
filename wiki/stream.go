package main

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
)

type apiUsage struct {
	Input      int64 `json:"input_tokens"`
	CacheWrite int64 `json:"cache_creation_input_tokens"`
	CacheRead  int64 `json:"cache_read_input_tokens"`
	Output     int64 `json:"output_tokens"`
}

// usageTap passes a response body through unchanged and extracts usage from
// it: from an event stream (message_start carries input, message_delta the
// cumulative output and closes the report) or from a JSON body. done is called
// exactly once, with nil unless a complete report was seen.
type usageTap struct {
	body   io.ReadCloser
	stream bool
	done   func(*Reported)

	buf      bytes.Buffer // JSON body, or the unparsed tail of the stream
	rep      Reported
	started  bool
	complete bool
	finished bool
}

func newUsageTap(body io.ReadCloser, contentType string, done func(*Reported)) *usageTap {
	return &usageTap{body: body, stream: strings.HasPrefix(contentType, "text/event-stream"), done: done}
}

func (t *usageTap) Read(p []byte) (int, error) {
	n, err := t.body.Read(p)
	if n > 0 && t.buf.Len() < maxBody {
		t.buf.Write(p[:n])
		if t.stream {
			t.scanEvents()
		}
	}
	if err == io.EOF {
		if !t.stream {
			t.parseJSON()
		}
		t.finish()
	}
	return n, err
}

func (t *usageTap) Close() error {
	t.finish()
	return t.body.Close()
}

func (t *usageTap) finish() {
	if t.finished {
		return
	}
	t.finished = true
	if t.complete {
		t.done(&t.rep)
	} else {
		t.done(nil)
	}
}

// scanEvents consumes complete lines from the buffer.
func (t *usageTap) scanEvents() {
	for {
		line, err := t.buf.ReadBytes('\n')
		if err != nil {
			// Incomplete line: put it back for the next read.
			rest := append([]byte(nil), line...)
			t.buf.Reset()
			t.buf.Write(rest)
			return
		}
		data, ok := bytes.CutPrefix(bytes.TrimRight(line, "\r\n"), []byte("data:"))
		if !ok {
			continue
		}
		var ev struct {
			Type    string `json:"type"`
			Message struct {
				Usage apiUsage `json:"usage"`
			} `json:"message"`
			Usage *apiUsage `json:"usage"`
		}
		if json.Unmarshal(bytes.TrimSpace(data), &ev) != nil {
			continue
		}
		switch ev.Type {
		case "message_start":
			u := ev.Message.Usage
			t.rep = Reported{Input: u.Input, CacheWrite: u.CacheWrite, CacheRead: u.CacheRead, Output: u.Output}
			t.started = true
		case "message_delta":
			if ev.Usage != nil && t.started {
				t.rep.Output = ev.Usage.Output
				if ev.Usage.Input > 0 {
					t.rep.Input = ev.Usage.Input
				}
				t.complete = true
			}
		}
	}
}

func (t *usageTap) parseJSON() {
	var resp struct {
		Usage       *apiUsage `json:"usage"`
		InputTokens *int64    `json:"input_tokens"` // count_tokens
	}
	if json.Unmarshal(t.buf.Bytes(), &resp) != nil {
		return
	}
	switch {
	case resp.Usage != nil:
		u := resp.Usage
		t.rep = Reported{Input: u.Input, CacheWrite: u.CacheWrite, CacheRead: u.CacheRead, Output: u.Output}
		t.complete = true
	case resp.InputTokens != nil:
		// count_tokens costs nothing; a complete report of zero use.
		t.complete = true
	}
}
