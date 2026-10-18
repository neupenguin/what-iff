package agent

import (
	"fmt"
	"strings"
	"sync"
)

// advancedTranscript is an Advanced run's reasoning (ADR 0x026): everything the model writes
// before its final reply, in order. That covers native reasoning, the text it writes between
// tool calls, earlier steps' replies and a header per step. It streams live into the job's
// draft reasoning, and at the end it becomes the message's model_reasoning, minus the final
// call's reply text, which becomes the message itself.
//
// Calls are numbered by beginCall so a provider retry can discard what the current call streamed
// (discardCallNative) without losing earlier calls.
type advancedTranscript struct {
	mu   sync.Mutex
	segs []transcriptSegment
	call int
	// out receives live deltas; reset clears what out has received (the whole transcript is
	// then re-sent). Either may be nil.
	out   func(delta string)
	reset func()
}

type transcriptKind int

const (
	transcriptNative transcriptKind = iota
	transcriptText
	transcriptHeader
)

type transcriptSegment struct {
	kind transcriptKind
	call int
	text string
}

func newAdvancedTranscript(out func(string), reset func()) *advancedTranscript {
	return &advancedTranscript{out: out, reset: reset}
}

// beginCall marks the start of a model call.
func (t *advancedTranscript) beginCall() {
	t.mu.Lock()
	t.call++
	t.mu.Unlock()
}

// currentCall is the number of the latest call.
func (t *advancedTranscript) currentCall() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.call
}

// nativeDelta records streamed model reasoning.
func (t *advancedTranscript) nativeDelta(d string) { t.appendDelta(transcriptNative, d) }

// textDelta records streamed reply text.
func (t *advancedTranscript) textDelta(d string) { t.appendDelta(transcriptText, d) }

// stepHeader marks the start of an Advanced step after the first.
func (t *advancedTranscript) stepHeader(step int) {
	t.appendDelta(transcriptHeader, fmt.Sprintf("— Step %d —", step))
}

func (t *advancedTranscript) appendDelta(kind transcriptKind, d string) {
	if d == "" {
		return
	}
	t.mu.Lock()
	var live string
	n := len(t.segs)
	if n > 0 && t.segs[n-1].kind == kind && t.segs[n-1].call == t.call && kind != transcriptHeader {
		t.segs[n-1].text += d
		live = d
	} else {
		// A new segment starts on its own paragraph; providers' own leading separators are dropped.
		d = strings.TrimLeft(d, "\n")
		if d == "" {
			t.mu.Unlock()
			return
		}
		t.segs = append(t.segs, transcriptSegment{kind: kind, call: t.call, text: d})
		live = d
		if n > 0 {
			live = "\n\n" + d
		}
	}
	out := t.out
	t.mu.Unlock()
	if out != nil {
		out(live)
	}
}

// discardCallNative drops the reasoning the current call streamed (a provider retry or the
// truncation fallback discarded that attempt) and re-sends the live transcript.
func (t *advancedTranscript) discardCallNative() {
	t.mu.Lock()
	kept := t.segs[:0]
	for _, s := range t.segs {
		if s.kind == transcriptNative && s.call == t.call {
			continue
		}
		kept = append(kept, s)
	}
	t.segs = kept
	full := t.renderLocked(-1)
	out, reset := t.out, t.reset
	t.mu.Unlock()
	if reset != nil {
		reset()
	}
	if out != nil && full != "" {
		out(full)
	}
}

// reasoning is the transcript to persist: everything except the reply text of finalCall.
func (t *advancedTranscript) reasoning(finalCall int) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.renderLocked(finalCall)
}

// textSince returns the reply text streamed in calls after afterCall, for the work log.
func (t *advancedTranscript) textSince(afterCall int) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	var parts []string
	for i := range t.segs {
		s := &t.segs[i]
		if s.kind == transcriptText && s.call > afterCall {
			if txt := strings.TrimSpace(s.text); txt != "" {
				parts = append(parts, txt)
			}
		}
	}
	return strings.Join(parts, "\n\n")
}

// renderLocked joins the segments, leaving out the reply text of excludeTextCall (-1: none) and a
// trailing step header with nothing after it.
func (t *advancedTranscript) renderLocked(excludeTextCall int) string {
	var parts []string
	for i := range t.segs {
		s := &t.segs[i]
		if s.kind == transcriptText && s.call == excludeTextCall {
			continue
		}
		if txt := strings.TrimSpace(s.text); txt != "" {
			parts = append(parts, txt)
		}
	}
	for len(parts) > 0 && strings.HasPrefix(parts[len(parts)-1], "— Step ") {
		parts = parts[:len(parts)-1]
	}
	return strings.Join(parts, "\n\n")
}
