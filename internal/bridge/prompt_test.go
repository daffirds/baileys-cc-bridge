package bridge

import (
	"strings"
	"testing"

	"github.com/daffirds/bailey-cc-bridge/internal/config"
	"github.com/daffirds/bailey-cc-bridge/internal/whatsapp"
)

func TestInstructions(t *testing.T) {
	me := &whatsapp.Me{ID: "628880000000@s.whatsapp.net"}
	got := instructions(&config.Chat{JID: me.ID, Name: "Me"}, me, "Owner")
	if !strings.Contains(got, `connected to the WhatsApp chat "Me" (Owner's own "Message yourself" chat, 628880000000@s.whatsapp.net) through Owner's account (+628880000000).`) {
		t.Errorf("default prompt filled in as\n%s", got)
	}
	if strings.Contains(got, "{") {
		t.Errorf("a placeholder was left:\n%s", got)
	}
	group := &config.Chat{JID: "120363000000000000@g.us", Name: "Team", Agent: config.Agent{SystemPrompt: "Help {chat} ({chat_kind}) for {owner}."}}
	if got := instructions(group, me, "Owner"); got != "Help Team (group) for Owner." {
		t.Errorf("custom prompt filled in as %q", got)
	}
}

func TestRenderMessage(t *testing.T) {
	b := &Bridge{me: &whatsapp.Me{ID: "628880000000@s.whatsapp.net", Name: "Owner"}, notes: newRing[note](10)}
	s := &session{b: b}
	got := s.renderOne(&whatsapp.Message{
		ID: "A1", Sender: "628120000000@s.whatsapp.net", PushName: "Budi", Timestamp: 1794840000,
		Kind: "text", Text: "@628880000000 check this", Forwarded: "many",
		Mentions: []whatsapp.Person{{JID: "628880000000@s.whatsapp.net"}, {JID: "123456789@lid", Name: "Ana"}},
		Quoted:   &whatsapp.Quoted{ID: "Q1", Sender: "628130000000@s.whatsapp.net", SenderName: "Cici", Text: "earlier"},
	})
	for _, want := range []string{
		`<message id="A1" from="Budi" number="+628120000000"`,
		`reply_to="Q1"`,
		`mentions_you="true"`,
		`mentions="Owner (+628880000000), Ana"`,
		`forwarded="many times"`,
		"<quote from=\"Cici (+628130000000)\">earlier</quote>\n@628880000000 check this\n</message>",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s in\n%s", want, got)
		}
	}

	b.notes.put("R1", note{from: "you", text: "on my way", own: true})
	got = s.renderOne(&whatsapp.Message{
		ID: "A2", Sender: "628120000000@s.whatsapp.net", PushName: "Budi", Kind: "reaction",
		Reaction: &whatsapp.Reaction{ID: "R1", Emoji: "👍"},
	})
	if !strings.Contains(got, `emoji="👍" to="R1" of="you" excerpt="on my way"/>`) {
		t.Errorf("reaction rendered as %s", got)
	}

	got = s.renderOne(&whatsapp.Message{ID: "A3", FromMe: true, Kind: "text", Text: "stop"})
	if !strings.Contains(got, `from="Owner" number="+628880000000" owner="true"`) {
		t.Errorf("owner message rendered as %s", got)
	}
}
