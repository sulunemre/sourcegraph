package http

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"

	"github.com/cockroachdb/errors"

	"github.com/sourcegraph/sourcegraph/internal/search/streaming/api"
)

// NewRequest returns an http.Request against the streaming API for query.
func NewRequest(baseURL string, query string) (*http.Request, error) {
	u := baseURL + "/search/stream?q=" + url.QueryEscape(query)
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "text/event-stream")
	return req, nil
}

// Decoder decodes streaming events from a Server Sent Event stream. We only
// support streams which are generated by Sourcegraph. IE this is not a fully
// compliant Server Sent Events decoder.
type Decoder struct {
	OnProgress func(*api.Progress)
	OnMatches  func([]EventMatch)
	OnFilters  func([]*EventFilter)
	OnAlert    func(*EventAlert)
	OnError    func(*EventError)
	OnUnknown  func(event, data []byte)
}

func (rr Decoder) ReadAll(r io.Reader) error {
	const maxPayloadSize = 10 * 1024 * 1024 // 10mb
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 4096), maxPayloadSize)
	// bufio.ScanLines, except we look for two \n\n which separate events.
	split := func(data []byte, atEOF bool) (int, []byte, error) {
		if atEOF && len(data) == 0 {
			return 0, nil, nil
		}
		if i := bytes.Index(data, []byte("\n\n")); i >= 0 {
			return i + 2, data[:i], nil
		}
		// If we're at EOF, we have a final, non-terminated event. This should
		// be empty.
		if atEOF {
			return len(data), data, nil
		}
		// Request more data.
		return 0, nil, nil
	}
	scanner.Split(split)

	for scanner.Scan() {
		// event: $event\n
		// data: json($data)\n\n
		data := scanner.Bytes()
		nl := bytes.Index(data, []byte("\n"))
		if nl < 0 {
			return errors.Errorf("malformed event, no newline: %s", data)
		}

		eventK, event := splitColon(data[:nl])
		dataK, data := splitColon(data[nl+1:])

		if !bytes.Equal(eventK, []byte("event")) {
			return errors.Errorf("malformed event, expected event: %s", eventK)
		}
		if !bytes.Equal(dataK, []byte("data")) {
			return errors.Errorf("malformed event %s, expected data: %s", eventK, dataK)
		}

		if bytes.Equal(event, []byte("progress")) {
			if rr.OnProgress == nil {
				continue
			}
			var d api.Progress
			if err := json.Unmarshal(data, &d); err != nil {
				return errors.Errorf("failed to decode progress payload: %w", err)
			}
			rr.OnProgress(&d)
		} else if bytes.Equal(event, []byte("matches")) {
			if rr.OnMatches == nil {
				continue
			}
			var d []eventMatchUnmarshaller
			if err := json.Unmarshal(data, &d); err != nil {
				return errors.Errorf("failed to decode matches payload: %w", err)
			}
			m := make([]EventMatch, 0, len(d))
			for _, e := range d {
				m = append(m, e.EventMatch)
			}
			rr.OnMatches(m)
		} else if bytes.Equal(event, []byte("filters")) {
			if rr.OnFilters == nil {
				continue
			}
			var d []*EventFilter
			if err := json.Unmarshal(data, &d); err != nil {
				return errors.Errorf("failed to decode filters payload: %w", err)
			}
			rr.OnFilters(d)
		} else if bytes.Equal(event, []byte("alert")) {
			if rr.OnAlert == nil {
				continue
			}
			var d EventAlert
			if err := json.Unmarshal(data, &d); err != nil {
				return errors.Errorf("failed to decode alert payload: %w", err)
			}
			rr.OnAlert(&d)
		} else if bytes.Equal(event, []byte("error")) {
			if rr.OnError == nil {
				continue
			}
			var d EventError
			if err := json.Unmarshal(data, &d); err != nil {
				return errors.Errorf("failed to decode error payload: %w", err)
			}
			rr.OnError(&d)
		} else if bytes.Equal(event, []byte("done")) {
			// Always the last event
			break
		} else {
			if rr.OnUnknown == nil {
				continue
			}
			rr.OnUnknown(event, data)
		}
	}
	return scanner.Err()
}

func splitColon(data []byte) ([]byte, []byte) {
	i := bytes.Index(data, []byte(":"))
	if i < 0 {
		return bytes.TrimSpace(data), nil
	}
	return bytes.TrimSpace(data[:i]), bytes.TrimSpace(data[i+1:])
}

type eventMatchUnmarshaller struct {
	EventMatch
}

func (r *eventMatchUnmarshaller) UnmarshalJSON(b []byte) error {
	var typeU struct {
		Type MatchType `json:"type"`
	}

	if err := json.Unmarshal(b, &typeU); err != nil {
		return err
	}

	switch typeU.Type {
	case FileMatchType:
		r.EventMatch = &EventFileMatch{}
	case RepoMatchType:
		r.EventMatch = &EventRepoMatch{}
	case SymbolMatchType:
		r.EventMatch = &EventSymbolMatch{}
	case CommitMatchType:
		r.EventMatch = &EventCommitMatch{}
	default:
		return errors.Errorf("unknown MatchType %v", typeU.Type)
	}
	return json.Unmarshal(b, r.EventMatch)
}
