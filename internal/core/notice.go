package core

import "fmt"

// NoticeKind classifies the transient status line (the macOS TUI's single
// notice row) so front ends can style it consistently and an iOS client can
// decide whether a notice deserves system-level presentation.
type NoticeKind string

const (
	// NoticeInfo is neutral progress-adjacent information.
	NoticeInfo NoticeKind = "Info"
	// NoticeProgress marks an operation in flight ("Connecting…",
	// "Testing: 3/10 nodes").
	NoticeProgress NoticeKind = "Progress"
	// NoticeSuccess confirms a completed action ("Config validated and
	// generated; disconnect and reconnect to apply").
	NoticeSuccess NoticeKind = "Success"
	// NoticeWarning flags a degraded-but-recovering situation, such as the
	// probe entering Unverified ("TUN running; probe did not pass yet,
	// retrying"). Warnings never claim a hard failure.
	NoticeWarning NoticeKind = "Warning"
	// NoticeError reports a failed action ("Save failed: …"). The text must
	// carry the cause; the front end must not swallow it.
	NoticeError NoticeKind = "Error"
)

// Notice is one transient user-facing message. Notices replace each other in
// the status line; they are not a log. Keep texts short and actionable, and
// never include credentials or node addresses (skills.md invariant 2).
type Notice struct {
	Kind NoticeKind
	Text string
}

// String renders the notice text (satisfies %v/%s formatting).
func (n Notice) String() string { return n.Text }

// Infof builds an informational notice.
func Infof(format string, args ...any) Notice {
	return Notice{Kind: NoticeInfo, Text: fmt.Sprintf(format, args...)}
}

// Progressf builds an in-flight notice.
func Progressf(format string, args ...any) Notice {
	return Notice{Kind: NoticeProgress, Text: fmt.Sprintf(format, args...)}
}

// Successf builds a completion notice.
func Successf(format string, args ...any) Notice {
	return Notice{Kind: NoticeSuccess, Text: fmt.Sprintf(format, args...)}
}

// Warningf builds a degraded-but-recovering notice.
func Warningf(format string, args ...any) Notice {
	return Notice{Kind: NoticeWarning, Text: fmt.Sprintf(format, args...)}
}

// Errorf builds a failure notice.
func Errorf(format string, args ...any) Notice {
	return Notice{Kind: NoticeError, Text: fmt.Sprintf(format, args...)}
}

// FromError builds an error notice from an error value, preserving its
// message. A nil error yields the zero Notice (callers usually branch on the
// error before calling).
func FromError(err error) Notice {
	if err == nil {
		return Notice{}
	}
	return Notice{Kind: NoticeError, Text: err.Error()}
}
