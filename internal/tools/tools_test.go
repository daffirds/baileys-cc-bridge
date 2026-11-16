package tools

import (
	"slices"
	"testing"
)

func TestWithMentions(t *testing.T) {
	text, jids, err := withMentions("thanks @6281234567890!", []string{"+62 812-3456-7890", "6289876543210"})
	if err != nil {
		t.Fatal(err)
	}
	if want := "thanks @6281234567890! @6289876543210"; text != want {
		t.Errorf("text = %q, want %q", text, want)
	}
	if want := []string{"6281234567890@s.whatsapp.net", "6289876543210@s.whatsapp.net"}; !slices.Equal(jids, want) {
		t.Errorf("jids = %v, want %v", jids, want)
	}
	if _, _, err := withMentions("hi", []string{"Budi"}); err == nil {
		t.Error("accepted a name as a number")
	}
}
