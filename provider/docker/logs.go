package docker

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

// dockerStreamHeader is the 8-byte frame header that prefixes every
// chunk from cli.ContainerLogs when the container has no TTY:
//
//	[0]    stream type — 0=stdin, 1=stdout, 2=stderr
//	[1:4]  reserved (zero)
//	[4:8]  big-endian uint32 frame size
//
// We split on those frames, then split each frame's payload by '\n'
// so the consumer gets one structured event per logical log line
// instead of arbitrary byte chunks.
const dockerStreamHeader = 8

// logEvent is the JSON shape emitted on the wire (one per SSE event
// or one per line, depending on the consumer). Keeping it small +
// stable — adding fields is fine, removing isn't.
type logEvent struct {
	Timestamp string `json:"ts"`
	Stream    string `json:"stream"` // "stdout" | "stderr"
	Line      string `json:"line"`
}

// demuxedDockerStream wraps the multiplexed docker logs stream into
// a stream of JSON-shaped log events (one object per line, '\n'
// terminated). Caller reads via the returned io.ReadCloser; the
// producer goroutine exits cleanly when src returns EOF or io.Pipe
// is closed by the consumer.
//
// When the docker container runs with TTY=true the stream isn't
// multiplexed — the 8-byte header is absent. We default to assuming
// no TTY (which is what the docker provider's ContainerCreate uses
// today). If TTY support is added later, the producer needs a
// detection path or an explicit flag.
func demuxedDockerStream(src io.ReadCloser) io.ReadCloser {
	r, w := io.Pipe()
	go func() {
		defer src.Close()
		defer w.Close()
		if err := streamDockerLogs(src, w); err != nil && err != io.EOF {
			// Best-effort: surface the parse error as a final event
			// so the consumer sees why the stream ended.
			ev := logEvent{
				Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
				Stream:    "stderr",
				Line:      "ctrlplane/docker: log stream ended: " + err.Error(),
			}
			data, _ := json.Marshal(ev)
			_, _ = w.Write(append(data, '\n'))
		}
	}()
	return r
}

// streamDockerLogs is the producer loop — pure function so it's
// testable without a docker socket.
func streamDockerLogs(src io.Reader, dst io.Writer) error {
	br := bufio.NewReader(src)
	header := make([]byte, dockerStreamHeader)

	for {
		// Read exactly one frame header.
		if _, err := io.ReadFull(br, header); err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				return nil
			}
			return fmt.Errorf("read header: %w", err)
		}

		streamType := streamTypeName(header[0])
		size := binary.BigEndian.Uint32(header[4:8])
		if size == 0 {
			continue
		}

		payload := make([]byte, size)
		if _, err := io.ReadFull(br, payload); err != nil {
			return fmt.Errorf("read frame: %w", err)
		}

		// Each frame may contain multiple lines (or a partial last
		// line). Splitting by '\n' is sufficient because docker's
		// timestamp prefix already escapes embedded newlines in
		// container output.
		for _, raw := range strings.Split(string(payload), "\n") {
			ts, line := splitDockerTimestamp(raw)
			if line == "" && ts == "" {
				continue
			}
			ev := logEvent{
				Timestamp: ts,
				Stream:    streamType,
				Line:      line,
			}
			data, err := json.Marshal(ev)
			if err != nil {
				return fmt.Errorf("marshal log event: %w", err)
			}
			if _, err := dst.Write(append(data, '\n')); err != nil {
				return fmt.Errorf("write log event: %w", err)
			}
		}
	}
}

// streamTypeName maps the docker frame-header byte to the JSON
// "stream" tag. Treat unknown types (anything non-1/non-2) as
// stdout — better to leak occasional misclassifications than drop
// data on the floor.
func streamTypeName(b byte) string {
	switch b {
	case 2:
		return "stderr"
	default:
		return "stdout"
	}
}

// splitDockerTimestamp pulls the leading RFC3339 timestamp docker
// emits when ContainerLogs is called with Timestamps:true. Format:
//
//	2026-04-28T15:00:00.123456789Z <body>
//
// When the line doesn't have a timestamp prefix (older docker, or
// the first/last partial line of a frame), returns ("", line) and
// the consumer falls back to time.Now in the caller.
func splitDockerTimestamp(raw string) (ts, body string) {
	if len(raw) < 20 || raw[19] != '.' && raw[19] != 'Z' && raw[19] != ' ' {
		return "", raw
	}
	parts := strings.SplitN(raw, " ", 2)
	if len(parts) != 2 {
		return "", raw
	}
	if _, err := time.Parse(time.RFC3339Nano, parts[0]); err != nil {
		return "", raw
	}
	return parts[0], parts[1]
}
