package gateway

import (
	"bytes"
	"encoding/json"
)

// chatTidy mends a Chat Completions stream relayed as it is. WorkBuddy's
// (copilot.tencent.com) keeps sending "reasoning_content": "" in every
// delta once the model has done thinking; its own CLI ignores an empty one
// after the thinking is over, Cline does too, but Qoder takes each for
// the thinking starting again and each piece of text after it for a new
// message, so a reply read one word to a line. An empty reasoning_content
// says nothing, so it is left out. Lines pass whole, as they came, unless
// one has it.
type chatTidy struct {
	buf []byte
}

var emptyReasoning = []byte(`"reasoning_content":""`)

// write takes what was read and gives back what to send on: every line
// that is complete, mended.
func (t *chatTidy) write(b []byte) []byte {
	t.buf = append(t.buf, b...)
	i := bytes.LastIndexByte(t.buf, '\n')
	if i < 0 {
		return nil
	}
	out := tidyLines(t.buf[:i+1])
	t.buf = append(t.buf[:0], t.buf[i+1:]...)
	return out
}

// flush is what's left once the stream ends.
func (t *chatTidy) flush() []byte {
	out := tidyLines(t.buf)
	t.buf = nil
	return out
}

func tidyLines(b []byte) []byte {
	if !bytes.Contains(b, emptyReasoning) {
		return append([]byte(nil), b...)
	}
	var out []byte
	for len(b) > 0 {
		line := b
		if i := bytes.IndexByte(b, '\n'); i >= 0 {
			line, b = b[:i+1], b[i+1:]
		} else {
			b = nil
		}
		out = append(out, tidyLine(line)...)
	}
	return out
}

func tidyLine(line []byte) []byte {
	if !bytes.Contains(line, emptyReasoning) {
		return line
	}
	body := bytes.TrimRight(line, "\r\n")
	end := line[len(body):]
	data, ok := bytes.CutPrefix(body, []byte("data:"))
	if !ok {
		return line
	}
	var chunk map[string]json.RawMessage
	if json.Unmarshal(data, &chunk) != nil {
		return line
	}
	var choices []map[string]json.RawMessage
	if json.Unmarshal(chunk["choices"], &choices) != nil {
		return line
	}
	changed := false
	for _, c := range choices {
		var delta map[string]json.RawMessage
		if json.Unmarshal(c["delta"], &delta) != nil {
			continue
		}
		if string(delta["reasoning_content"]) != `""` {
			continue
		}
		delete(delta, "reasoning_content")
		c["delta"], _ = json.Marshal(delta)
		changed = true
	}
	if !changed {
		return line
	}
	chunk["choices"], _ = json.Marshal(choices)
	nb, err := json.Marshal(chunk)
	if err != nil {
		return line
	}
	return append(append([]byte("data: "), nb...), end...)
}
