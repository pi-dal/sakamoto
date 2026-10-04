package core

import (
	"errors"
	"strings"
	"testing"
)

func TestNoticeConstructors(t *testing.T) {
	cases := []struct {
		got  Notice
		want NoticeKind
	}{
		{Infof("imported %d rules", 3), NoticeInfo},
		{Progressf("Connecting…"), NoticeProgress},
		{Successf("Config validated and generated"), NoticeSuccess},
		{Warningf("TUN running; probe did not pass yet, retrying"), NoticeWarning},
		{Errorf("Save failed: %v", errors.New("disk full")), NoticeError},
	}
	for _, tc := range cases {
		if tc.got.Kind != tc.want {
			t.Fatalf("kind = %s, want %s (text %q)", tc.got.Kind, tc.want, tc.got.Text)
		}
		if strings.TrimSpace(tc.got.Text) == "" {
			t.Fatalf("empty notice text for kind %s", tc.got.Kind)
		}
	}
}

func TestNoticeFormatting(t *testing.T) {
	n := Errorf("save failed: %s", "readonly")
	if n.String() != "save failed: readonly" {
		t.Fatalf("String() = %q", n.String())
	}
}

func TestFromError(t *testing.T) {
	cause := errors.New("generation failed")
	got := FromError(cause)
	if got.Kind != NoticeError || got.Text != "generation failed" {
		t.Fatalf("FromError = %+v", got)
	}
	if zero := FromError(nil); zero != (Notice{}) {
		t.Fatalf("FromError(nil) = %+v, want zero Notice", zero)
	}
}
